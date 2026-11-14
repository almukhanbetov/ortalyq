package api

// «Пробный урок» — ознакомительная демонстрация виртуального класса без регистрации.
//
// Используются те же механизмы, что и в уроке (WebSocket-хаб доски, кода и учебного текста, LiveKit,
// исполнитель кода), но в отдельной временной комнате на каждый запуск:
//   - демонстрация не создаёт строк в lessons, users, availability, не меняет расписание, бронирования,
//     цены и оплаты;
//   - доступ — по отдельной cookie участника (ortalyq_demo) с Path=/api/demo/rooms/<id>: браузер отправляет её
//     только в API своей демо-комнаты, а обработчики уроков её не читают. Сессия демо не даёт доступа ни
//     к урокам, ни к данным пользователей, ни к другой демо-комнате;
//   - роли назначает сервер: создатель — «преподаватель», принявший приглашение — «ученик»; третьему
//     участнику отказывает уникальный индекс (room_id, role);
//   - длительность — DEMO_MINUTES по серверному времени; по истечении сервер закрывает доступ, отключает
//     участников, удаляет комнату LiveKit и освобождает память (доска, код, текст, чат хранятся только в памяти);
//   - запись видео и запросы к ИИ в демо не выполняются; вместо ИИ показывается подготовленный пример.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/auth"
	"ortalyq/backend/internal/coderun"
	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/realtime"
	"ortalyq/backend/internal/workshop"
)

const demoCookie = "ortalyq_demo"

var (
	demoCreateLimiter = &limiter{hits: map[string][]time.Time{}}
	demoAcceptLimiter = &limiter{hits: map[string][]time.Time{}}
	demoFailLimiter   = &limiter{hits: map[string][]time.Time{}}
	demoActionLimiter = &limiter{hits: map[string][]time.Time{}}
)

const (
	demoChatLimit       = 200
	demoChatRunes       = 1000
	demoRunsPerMinute   = 6
	demoAcceptPerRoom   = 10 // попыток принять приглашение в одну комнату
	demoAcceptPerIP     = 20 // за 10 минут
	demoFailedPerIP     = 60 // неудачных обращений к демо-комнатам за 10 минут
	demoNameRunes       = 40
	demoDefaultTeacher  = "Преподаватель (демо)"
	demoDefaultStudent  = "Ученик (демо)"
	demoClosedMessage   = "Демонстрация завершена — доступ закрыт"
	demoExpiredMessage  = "Время демонстрации истекло — доступ закрыт"
	demoNotFoundMessage = "Демонстрация не найдена или ссылка устарела"
)

type demoRoom struct {
	ID        string
	Mode      string
	CreatedAt time.Time
	ExpiresAt time.Time
	EndedAt   *time.Time
	EndReason string
}

type demoParticipant struct {
	ID   string
	Role string
	Name string
}

// demoMemory — содержимое демонстраций, которое не хранится в БД: чат, последний запуск кода,
// счётчик запусков и таймеры окончания. Удаляется при завершении демонстрации.
type demoMemory struct {
	mu       sync.Mutex
	chat     map[string][]map[string]any
	chatSeq  int64
	lastRun  map[string]*codeRun
	runCount map[string]int
	timers   map[string]*time.Timer
}

func newDemoMemory() *demoMemory {
	return &demoMemory{chat: map[string][]map[string]any{}, lastRun: map[string]*codeRun{}, runCount: map[string]int{}, timers: map[string]*time.Timer{}}
}

func (m *demoMemory) forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.chat, id)
	delete(m.lastRun, id)
	delete(m.runCount, id)
	if t := m.timers[id]; t != nil {
		t.Stop()
		delete(m.timers, id)
	}
}

func demoKey(roomID string) string        { return realtime.EphemeralPrefix + roomID }
func demoCookiePath(roomID string) string { return "/api/demo/rooms/" + roomID }
func demoLiveKitRoom(roomID string) string {
	return "demo-" + roomID
}

func (s *Server) demoInviteToken(roomID string) string { return s.signer.MAC("demo-invite", roomID) }

// clientKey — ключ ограничения частоты по IP; сам IP не сохраняется.
func (s *Server) clientKey(c *gin.Context) string { return s.signer.MAC("demo-ip", c.ClientIP())[:22] }

func (s *Server) setDemoCookie(c *gin.Context, roomID, value string, maxAge int) {
	c.SetSameSite(2) // http.SameSiteLaxMode
	c.SetCookie(demoCookie, value, maxAge, demoCookiePath(roomID), "", s.cfg.CookieSecure, true)
}

// demoProblem объясняет, почему демонстрация недоступна; "" — идёт.
func demoProblem(r *demoRoom, now time.Time) string {
	if r.EndedAt != nil {
		if r.EndReason == "expired" {
			return demoExpiredMessage
		}
		return demoClosedMessage
	}
	if !now.Before(r.ExpiresAt) {
		return demoExpiredMessage
	}
	return ""
}

func cleanDemoName(raw, def string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	if utf8.RuneCountInString(name) > demoNameRunes {
		name = string([]rune(name)[:demoNameRunes])
	}
	if utf8.RuneCountInString(name) < 2 {
		return def
	}
	return name + " (демо)"
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Server) loadDemoRoom(ctx context.Context, id string) (*demoRoom, error) {
	var r demoRoom
	err := s.db.QueryRow(ctx, `SELECT id, mode, created_at, expires_at, ended_at, end_reason FROM demo_rooms WHERE id=$1`, id).
		Scan(&r.ID, &r.Mode, &r.CreatedAt, &r.ExpiresAt, &r.EndedAt, &r.EndReason)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// demoParticipantFor — участник комнаты по cookie запроса; nil — cookie нет или она не от этой комнаты.
func (s *Server) demoParticipantFor(c *gin.Context, roomID string) (*demoParticipant, error) {
	raw, err := c.Cookie(demoCookie)
	if err != nil || raw == "" || len(raw) > 100 {
		return nil, nil
	}
	var p demoParticipant
	err = s.db.QueryRow(c, `SELECT id, role, name FROM demo_participants WHERE token_hash=$1 AND room_id=$2`, auth.HashToken(raw), roomID).
		Scan(&p.ID, &p.Role, &p.Name)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// demoAccess проверяет, что запрос пришёл от участника этой демо-комнаты. Остальным — 404
// (и 429 при множестве неудачных попыток с одного адреса).
func (s *Server) demoAccess(c *gin.Context) (*demoRoom, *demoParticipant, bool) {
	id := c.Param("id")
	notFound := func() {
		if !demoFailLimiter.allow(s.clientKey(c), demoFailedPerIP, 10*time.Minute) {
			fail(c, 429, "Слишком много неудачных попыток входа в демонстрацию — подождите несколько минут")
			return
		}
		fail(c, 404, demoNotFoundMessage)
	}
	if !isUUID(id) {
		notFound()
		return nil, nil, false
	}
	p, err := s.demoParticipantFor(c, id)
	if err != nil {
		internal(c, err)
		return nil, nil, false
	}
	if p == nil {
		notFound()
		return nil, nil, false
	}
	r, err := s.loadDemoRoom(c, id)
	if err != nil {
		if db.IsNoRows(err) {
			notFound()
		} else {
			internal(c, err)
		}
		return nil, nil, false
	}
	// Истекла, но ещё не закрыта (таймер не успел или сервер перезапускался) — закрываем сейчас.
	if r.EndedAt == nil && !time.Now().Before(r.ExpiresAt) {
		s.endDemo(context.WithoutCancel(c.Request.Context()), r.ID, "expired")
		if fresh, err := s.loadDemoRoom(c, id); err == nil {
			r = fresh
		}
	}
	return r, p, true
}

// demoState — состояние демонстрации для участника.
func (s *Server) demoState(c *gin.Context, r *demoRoom, p *demoParticipant) (gin.H, error) {
	rows, err := s.db.Query(c, `SELECT role, name FROM demo_participants WHERE room_id=$1 ORDER BY role DESC`, r.ID)
	if err != nil {
		return nil, err
	}
	participants := []gin.H{}
	hasStudent := false
	for rows.Next() {
		var role, name string
		if err := rows.Scan(&role, &name); err != nil {
			rows.Close()
			return nil, err
		}
		participants = append(participants, gin.H{"role": role, "name": name})
		hasStudent = hasStudent || role == "student"
	}
	rows.Close()
	problem := demoProblem(r, time.Now())
	status := "active"
	if problem != "" {
		status = "ended"
	}
	out := gin.H{
		"id":              r.ID,
		"mode":            r.Mode,
		"status":          status,
		"closedReason":    problem,
		"endReason":       r.EndReason,
		"createdAt":       r.CreatedAt,
		"expiresAt":       r.ExpiresAt,
		"durationMinutes": int(r.ExpiresAt.Sub(r.CreatedAt).Round(time.Minute) / time.Minute),
		"me":              gin.H{"id": p.ID, "name": p.Name, "role": p.Role},
		"participants":    participants,
		"features":        s.demoFeatures(),
	}
	if r.Mode == "pair" && p.Role == "teacher" && status == "active" {
		invite := gin.H{"accepted": hasStudent}
		if !hasStudent {
			invite["path"] = "/demo/join/" + r.ID + "#" + s.demoInviteToken(r.ID)
		}
		out["invite"] = invite
	}
	return out, nil
}

// demoFeatures — что реально работает в демонстрации на этом сервере (с причиной, если нет).
func (s *Server) demoFeatures() gin.H {
	codeProblem := ""
	switch {
	case s.runner == nil:
		codeProblem = "Сервис выполнения кода не настроен на этом сервере — код можно писать вместе, но запустить нельзя"
	case s.cfg.DemoCodeRuns == 0:
		codeProblem = "Запуск кода в пробном уроке отключён администратором — код можно писать вместе"
	}
	videoProblem := ""
	if s.lk == nil {
		videoProblem = "Видеосвязь не настроена на этом сервере — работают доска, текст, код и чат"
	}
	return gin.H{
		"video":         s.lk != nil,
		"videoProblem":  videoProblem,
		"codeRun":       codeProblem == "",
		"codeRunLimit":  s.cfg.DemoCodeRuns,
		"codeProblem":   codeProblem,
		"aiLive":        false,
		"aiProblem":     "В пробном уроке запросы к ИИ не отправляются — показан заранее подготовленный пример результата",
		"aiConfigured":  s.ai != nil,
		"recording":     false,
		"recordingNote": "Запись видео в пробном уроке отключена",
	}
}

// POST /api/demo/rooms {mode, name} — новая демонстрация (без регистрации).
func (s *Server) createDemo(c *gin.Context) {
	if !s.cfg.DemoEnabled {
		fail(c, 503, "Пробный урок отключён на этом сервере")
		return
	}
	var in struct {
		Mode string `json:"mode"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || (in.Mode != "solo" && in.Mode != "pair") {
		fail(c, 400, "Выберите режим: самостоятельно или вдвоём")
		return
	}
	key := s.clientKey(c)
	if !demoCreateLimiter.allow("m:"+key, 3, time.Minute) || !demoCreateLimiter.allow("h:"+key, s.cfg.DemoPerIPPerHour, time.Hour) {
		fail(c, 429, "Слишком много новых демонстраций с вашего адреса — подождите немного и попробуйте снова")
		return
	}
	now := time.Now()
	var active int
	if err := s.db.QueryRow(c, `SELECT count(*) FROM demo_rooms WHERE ended_at IS NULL AND expires_at > $1`, now).Scan(&active); err != nil {
		internal(c, err)
		return
	}
	if active >= s.cfg.DemoMaxActive {
		fail(c, 503, "Сейчас идёт слишком много демонстраций — попробуйте через несколько минут")
		return
	}
	raw, hash, err := auth.NewToken()
	if err != nil {
		internal(c, err)
		return
	}
	expires := now.Add(time.Duration(s.cfg.DemoMinutes) * time.Minute)
	tx, err := s.db.Begin(c)
	if err != nil {
		internal(c, err)
		return
	}
	defer tx.Rollback(c)
	var id string
	if err := tx.QueryRow(c, `INSERT INTO demo_rooms(mode, created_at, expires_at, creator_key) VALUES ($1,$2,$3,$4) RETURNING id`,
		in.Mode, now, expires, key).Scan(&id); err != nil {
		internal(c, err)
		return
	}
	name := cleanDemoName(in.Name, demoDefaultTeacher)
	var pid string
	if err := tx.QueryRow(c, `INSERT INTO demo_participants(room_id, role, name, token_hash) VALUES ($1,'teacher',$2,$3) RETURNING id`, id, name, hash).Scan(&pid); err != nil {
		internal(c, err)
		return
	}
	if err := tx.Commit(c); err != nil {
		internal(c, err)
		return
	}
	s.scheduleDemoEnd(id, expires)
	s.setDemoCookie(c, id, raw, s.cfg.DemoMinutes*60+300)
	slog.Info("demo created", "demo", id, "mode", in.Mode)
	r := &demoRoom{ID: id, Mode: in.Mode, CreatedAt: now, ExpiresAt: expires}
	st, err := s.demoState(c, r, &demoParticipant{ID: pid, Role: "teacher", Name: name})
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(201, st)
}

// GET /api/demo/rooms/:id — состояние демонстрации (только участнику).
func (s *Server) getDemo(c *gin.Context) {
	r, p, ok := s.demoAccess(c)
	if !ok {
		return
	}
	st, err := s.demoState(c, r, p)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, st)
}

// POST /api/demo/rooms/:id/accept {token, name} — второй участник по ссылке-приглашению.
func (s *Server) acceptDemo(c *gin.Context) {
	id := c.Param("id")
	key := s.clientKey(c)
	if !demoAcceptLimiter.allow("ip:"+key, demoAcceptPerIP, 10*time.Minute) || (isUUID(id) && !demoAcceptLimiter.allow("room:"+id, demoAcceptPerRoom, time.Hour)) {
		fail(c, 429, "Слишком много попыток входа по приглашению — подождите несколько минут")
		return
	}
	var in struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	c.ShouldBindJSON(&in)
	if !isUUID(id) || len(in.Token) > 100 || !hmac.Equal([]byte(in.Token), []byte(s.demoInviteToken(id))) {
		fail(c, 404, "Ссылка-приглашение недействительна. Попросите прислать её заново или начните свою демонстрацию.")
		return
	}
	r, err := s.loadDemoRoom(c, id)
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Ссылка-приглашение недействительна: демонстрация удалена")
		} else {
			internal(c, err)
		}
		return
	}
	if r.Mode != "pair" {
		fail(c, 404, "Ссылка-приглашение недействительна")
		return
	}
	// Тот же браузер уже участвует в этой демонстрации — просто входит, не занимая второе место.
	if p, err := s.demoParticipantFor(c, id); err != nil {
		internal(c, err)
		return
	} else if p != nil {
		st, err := s.demoState(c, r, p)
		if err != nil {
			internal(c, err)
			return
		}
		c.JSON(200, st)
		return
	}
	if msg := demoProblem(r, time.Now()); msg != "" {
		fail(c, 410, msg+". Начните свою демонстрацию — это бесплатно.")
		return
	}
	raw, hash, err := auth.NewToken()
	if err != nil {
		internal(c, err)
		return
	}
	name := cleanDemoName(in.Name, demoDefaultStudent)
	var pid string
	err = s.db.QueryRow(c, `INSERT INTO demo_participants(room_id, role, name, token_hash) VALUES ($1,'student',$2,$3) RETURNING id`, id, name, hash).Scan(&pid)
	if err != nil {
		if code, _ := db.ConstraintName(err); code == "23505" {
			fail(c, 409, "В этой демонстрации уже два участника — третьим войти нельзя. Начните свою демонстрацию.")
			return
		}
		internal(c, err)
		return
	}
	s.setDemoCookie(c, id, raw, int(time.Until(r.ExpiresAt).Seconds())+300)
	s.hub.Broadcast(demoKey(id), gin.H{"type": "demo.participants"})
	st, err := s.demoState(c, r, &demoParticipant{ID: pid, Role: "student", Name: name})
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(201, st)
}

// POST /api/demo/rooms/:id/join — токен LiveKit только в комнату этой демонстрации и до её окончания.
func (s *Server) joinDemo(c *gin.Context) {
	r, p, ok := s.demoAccess(c)
	if !ok {
		return
	}
	if msg := demoProblem(r, time.Now()); msg != "" {
		fail(c, 410, msg)
		return
	}
	if s.lk == nil {
		fail(c, 503, "Видеосвязь не настроена на этом сервере — в демонстрации работают доска, текст, код и чат")
		return
	}
	// Токен действует ровно до окончания демонстрации (не дольше): после срока по нему не войти.
	token, err := s.lk.JoinToken(demoLiveKitRoom(r.ID), "demo-"+p.ID, p.Name, time.Until(r.ExpiresAt))
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"token": token, "url": s.cfg.LiveKitPublicURL, "room": demoLiveKitRoom(r.ID), "identity": "demo-" + p.ID, "role": p.Role})
}

// GET /api/demo/rooms/:id/ws — доска, чат, код и учебный текст временной комнаты.
func (s *Server) demoWS(c *gin.Context) {
	r, p, ok := s.demoAccess(c)
	if !ok {
		return
	}
	if msg := demoProblem(r, time.Now()); msg != "" {
		fail(c, 410, msg)
		return
	}
	if origin := c.GetHeader("Origin"); origin != "" && !s.originAllowed(origin, c.Request.Host) {
		fail(c, 403, "Недопустимый источник запроса")
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	s.scheduleDemoEnd(r.ID, r.ExpiresAt)
	s.demo.mu.Lock()
	chat := append([]map[string]any{}, s.demo.chat[r.ID]...)
	s.demo.mu.Unlock()
	hello := map[string]any{
		"me":           map[string]string{"id": p.ID, "name": p.Name, "role": p.Role},
		"chat":         chat,
		"lessonStatus": "live",
		"recording":    nil,
		"demo":         map[string]any{"expiresAt": r.ExpiresAt},
	}
	name := p.Name
	store := func(ctx context.Context, key, userID, body string) (map[string]any, error) {
		return s.storeDemoChat(r.ID, userID, name, body)
	}
	// Учебный текст можно править до окончания демонстрации.
	s.hub.Serve(c.Request.Context(), conn, demoKey(r.ID), &realtime.Client{UserID: p.ID, Name: p.Name, Role: p.Role, EditUntil: r.ExpiresAt}, hello, store)
}

type demoError string

func (e demoError) Error() string { return string(e) }

func (s *Server) storeDemoChat(roomID, userID, name, body string) (map[string]any, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, demoError("Пустое сообщение")
	}
	if utf8.RuneCountInString(body) > demoChatRunes {
		return nil, demoError(fmt.Sprintf("Сообщение слишком длинное (в демонстрации максимум %d символов)", demoChatRunes))
	}
	if !demoActionLimiter.allow("chat:"+userID, 30, time.Minute) {
		return nil, demoError("Слишком много сообщений — подождите немного")
	}
	m := s.demo
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatSeq++
	msg := map[string]any{"id": m.chatSeq, "userId": userID, "userName": name, "body": body, "createdAt": time.Now()}
	list := append(m.chat[roomID], msg)
	if len(list) > demoChatLimit {
		list = list[len(list)-demoChatLimit:]
	}
	m.chat[roomID] = list
	return msg, nil
}

// GET /api/demo/rooms/:id/code
func (s *Server) getDemoCode(c *gin.Context) {
	r, _, ok := s.demoAccess(c)
	if !ok {
		return
	}
	s.demo.mu.Lock()
	var last any
	if run := s.demo.lastRun[r.ID]; run != nil {
		cp := *run
		last = cp
	}
	used := s.demo.runCount[r.ID]
	s.demo.mu.Unlock()
	f := s.demoFeatures()
	c.JSON(200, gin.H{
		"languages":        s.codeLanguages(),
		"runnerConfigured": f["codeRun"],
		"runnerProblem":    f["codeProblem"],
		"lastRun":          last,
		"limits":           s.codeInfoLimits(),
		"runsLeft":         max(s.cfg.DemoCodeRuns-used, 0),
	})
}

// POST /api/demo/rooms/:id/code/runs — запуск кода в демонстрации: тот же изолированный исполнитель,
// но с лимитом запусков на демонстрацию и по частоте. Результат хранится только в памяти.
func (s *Server) createDemoCodeRun(c *gin.Context) {
	r, p, ok := s.demoAccess(c)
	if !ok {
		return
	}
	if msg := demoProblem(r, time.Now()); msg != "" {
		fail(c, 409, "Запуск кода недоступен: "+msg)
		return
	}
	if s.runner == nil {
		c.AbortWithStatusJSON(503, gin.H{"error": "Сервис выполнения кода не настроен на этом сервере", "code": coderun.StatusNotConfigured})
		return
	}
	if s.cfg.DemoCodeRuns == 0 {
		c.AbortWithStatusJSON(503, gin.H{"error": "Запуск кода в пробном уроке отключён администратором", "code": coderun.StatusNotConfigured})
		return
	}
	lang, in, ok := s.decodeCodeRun(c)
	if !ok {
		return
	}
	if !demoActionLimiter.allow("run:"+r.ID, demoRunsPerMinute, time.Minute) || !codeRunLimiter.allow("demo-ip:"+s.clientKey(c), runsPerUserPerMinute, time.Minute) {
		fail(c, 429, "Слишком частые запуски — подождите немного")
		return
	}
	key := demoKey(r.ID)
	s.demo.mu.Lock()
	if s.demo.runCount[r.ID] >= s.cfg.DemoCodeRuns {
		s.demo.mu.Unlock()
		fail(c, 429, fmt.Sprintf("В пробном уроке можно запустить код %d раз — лимит исчерпан", s.cfg.DemoCodeRuns))
		return
	}
	s.demo.mu.Unlock()

	rs := s.runs
	rs.mu.Lock()
	if _, busy := rs.byLesson[key]; busy {
		rs.mu.Unlock()
		fail(c, 409, "Предыдущий запуск ещё выполняется")
		return
	}
	select {
	case rs.sem <- struct{}{}:
	default:
		rs.mu.Unlock()
		fail(c, 503, "Сервис выполнения занят — попробуйте через несколько секунд")
		return
	}
	runID := newUUID()
	rctx, cancel := context.WithTimeout(rs.ctx, 30*time.Second+time.Duration(s.cfg.CodeRunWallSeconds*float64(time.Second)))
	rs.byLesson[key] = runID
	rs.cancels[runID] = cancel
	rs.mu.Unlock()
	release := func() {
		rs.mu.Lock()
		delete(rs.byLesson, key)
		delete(rs.cancels, runID)
		rs.mu.Unlock()
		<-rs.sem
	}

	run := &codeRun{ID: runID, UserID: p.ID, UserName: p.Name, Language: lang.ID, Status: coderun.StatusRunning,
		SourceBytes: len(in.Source), StdinBytes: len(in.Stdin), CreatedAt: time.Now()}
	s.demo.mu.Lock()
	s.demo.runCount[r.ID]++
	s.demo.lastRun[r.ID] = run
	snapshot := *run
	s.demo.mu.Unlock()
	s.hub.Broadcast(key, gin.H{"type": "code.run", "run": snapshot})

	req := coderun.Request{Language: *lang, Source: in.Source, Stdin: in.Stdin, Limits: s.codeRunLimits()}
	roomID := r.ID
	go func() {
		defer release()
		defer cancel()
		res, err := s.runner.Run(rctx, req)
		if err != nil {
			slog.Warn("demo code run", "demo", roomID, "run", runID, "status", res.Status, "err", err)
		}
		now := time.Now()
		final := codeRun{ID: runID, UserID: snapshot.UserID, UserName: snapshot.UserName, Language: snapshot.Language, Status: res.Status,
			Stdout: res.Stdout, Stderr: res.Stderr, Message: res.Message, ExitCode: res.ExitCode, TimeMs: res.TimeMs, MemoryKB: res.MemoryKB,
			Truncated: res.Truncated, SourceBytes: snapshot.SourceBytes, StdinBytes: snapshot.StdinBytes, CreatedAt: snapshot.CreatedAt, FinishedAt: &now}
		s.demo.mu.Lock()
		if res.Status == coderun.StatusUnavailable {
			// Исполнитель не ответил: гостю — понятная причина без служебных подробностей (они в журнале),
			// попытка не засчитывается в лимит DEMO_CODE_RUNS.
			final.Message = "Сервис выполнения кода сейчас недоступен — код не запускался, попытка не засчитана. Код можно продолжать писать вместе; попробуйте запустить позже"
			if s.demo.runCount[roomID] > 0 {
				s.demo.runCount[roomID]--
			}
		}
		if cur := s.demo.lastRun[roomID]; cur != nil && cur.ID == runID {
			s.demo.lastRun[roomID] = &final
		}
		left := max(s.cfg.DemoCodeRuns-s.demo.runCount[roomID], 0)
		s.demo.mu.Unlock()
		s.hub.Broadcast(key, gin.H{"type": "code.run", "run": final, "runsLeft": left})
	}()
	c.JSON(202, gin.H{"run": snapshot})
}

// POST /api/demo/rooms/:id/code/runs/:rid/cancel
func (s *Server) cancelDemoCodeRun(c *gin.Context) {
	r, _, ok := s.demoAccess(c)
	if !ok {
		return
	}
	rid := c.Param("rid")
	s.runs.mu.Lock()
	cancel := s.runs.cancels[rid]
	active := isUUID(rid) && s.runs.byLesson[demoKey(r.ID)] == rid
	s.runs.mu.Unlock()
	if !active || cancel == nil {
		fail(c, 409, "Запуск уже завершён")
		return
	}
	cancel()
	c.JSON(200, gin.H{"ok": true})
}

// GET /api/demo/rooms/:id/workshop — мастерская демонстрации: совместный текст работает по WebSocket,
// запросы к ИИ не выполняются (пример результата показывает интерфейс).
func (s *Server) getDemoWorkshop(c *gin.Context) {
	r, p, ok := s.demoAccess(c)
	if !ok {
		return
	}
	closed := demoProblem(r, time.Now())
	out := gin.H{
		"role":         p.Role,
		"subject":      "Пробный урок",
		"mode":         "general",
		"modes":        workshop.Modes(),
		"levels":       workshop.Levels(),
		"editable":     closed == "",
		"closedReason": closed,
		"analyses":     []any{},
		"homework":     []any{},
		"limits":       gin.H{"fragmentChars": workshop.MaxFragmentRunes, "homeworkTextChars": workshop.MaxHomeworkTextRunes},
		"demo":         true,
	}
	if p.Role == "teacher" {
		out["ai"] = gin.H{"configured": false, "problem": "в пробном уроке запросы к ИИ не отправляются — ниже «Пример результата»", "noticeAccepted": false}
	}
	c.JSON(200, out)
}

// POST /api/demo/rooms/:id/end — организатор (роль «преподаватель») завершает демонстрацию для всех.
func (s *Server) endDemoHandler(c *gin.Context) {
	r, p, ok := s.demoAccess(c)
	if !ok {
		return
	}
	if p.Role != "teacher" {
		fail(c, 403, "Завершить демонстрацию для всех может только тот, кто её начал. Вы можете просто выйти.")
		return
	}
	if r.EndedAt == nil {
		s.endDemo(context.WithoutCancel(c.Request.Context()), r.ID, "ended")
	}
	c.JSON(200, gin.H{"ok": true})
}

// scheduleDemoEnd ставит таймер окончания (один на демонстрацию): в момент expiresAt сервер закрывает доступ.
func (s *Server) scheduleDemoEnd(id string, expires time.Time) {
	m := s.demo
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timers[id] != nil {
		return
	}
	m.timers[id] = time.AfterFunc(time.Until(expires), func() {
		s.endDemo(context.Background(), id, "expired")
	})
}

// endDemo закрывает демонстрацию: отмечает окончание в БД, отключает участников (WebSocket и LiveKit),
// останавливает запуск кода и удаляет содержимое из памяти. Повторный вызов безопасен.
func (s *Server) endDemo(ctx context.Context, id, reason string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tag, err := s.db.Exec(ctx, `UPDATE demo_rooms SET ended_at=least(now(), expires_at), end_reason=$2 WHERE id=$1 AND ended_at IS NULL`, id, reason)
	if err != nil {
		slog.Error("end demo", "demo", id, "err", err)
		return // доступ всё равно закрыт проверкой expires_at; повторим в фоновой задаче
	}
	if tag.RowsAffected() > 0 {
		slog.Info("demo ended", "demo", id, "reason", reason)
	}
	s.releaseDemo(id, reason)
}

func (s *Server) releaseDemo(id, reason string) {
	key := demoKey(id)
	s.runs.mu.Lock()
	if rid := s.runs.byLesson[key]; rid != "" {
		if cancel := s.runs.cancels[rid]; cancel != nil {
			cancel()
		}
	}
	s.runs.mu.Unlock()
	if reason == "" {
		reason = "ended"
	}
	s.hub.Release(key, gin.H{"type": "demo.ended", "reason": reason})
	s.demo.forget(id)
	if s.lk != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.lk.DeleteRoom(ctx, demoLiveKitRoom(id)); err != nil && !strings.Contains(err.Error(), "not found") {
				slog.Warn("delete demo room", "demo", id, "err", err)
			}
		}()
	}
}

// demoSweep — фоновая проверка: закрывает истёкшие демонстрации (например, после перезапуска сервера),
// освобождает оставшиеся в памяти комнаты и удаляет служебные записи старше DEMO_RETENTION_HOURS.
// demoVideoWebhook — вход в комнату LiveKit закончившейся демонстрации. Новые токены после окончания
// не выдаются, но у участника может остаться действующий токен (в том числе обновлённый самим LiveKit
// во время урока), и LiveKit создаст комнату заново. По событию room_started / participant_joined такая
// комната сразу удаляется; sweepDemoVideoRooms — запасной путь, если вебхук не дошёл.
func (s *Server) demoVideoWebhook(event, room string) {
	id, ok := strings.CutPrefix(room, "demo-")
	if !ok || !isUUID(id) || (event != "room_started" && event != "participant_joined") || s.lk == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var active bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM demo_rooms WHERE id=$1 AND ended_at IS NULL AND expires_at > now())`, id).Scan(&active); err != nil || active {
			return
		}
		if err := s.lk.DeleteRoom(ctx, room); err == nil {
			slog.Info("demo video room closed on join after end", "demo", id, "event", event)
		}
	}()
}

// sweepDemoVideoRooms удаляет комнаты LiveKit «demo-…» закончившихся демонстраций. Комната удаляется и при
// окончании, но участник с ещё действующим токеном LiveKit мог переподключиться напрямую к LiveKit, и тот
// создал бы комнату заново; фоновая задача закрывает такие комнаты не позже чем через один проход.
func (s *Server) sweepDemoVideoRooms(ctx context.Context, now time.Time) {
	if s.lk == nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	names, err := s.lk.RoomNames(lctx)
	if err != nil {
		return
	}
	var ids []string
	for _, n := range names {
		if id, ok := strings.CutPrefix(n, "demo-"); ok && isUUID(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	active := map[string]bool{}
	rows, err := s.db.Query(ctx, `SELECT id::text FROM demo_rooms WHERE id::text = ANY($1) AND ended_at IS NULL AND expires_at > $2`, ids, now)
	if err != nil {
		return
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			active[id] = true
		}
	}
	rows.Close()
	for _, id := range ids {
		if !active[id] {
			if err := s.lk.DeleteRoom(lctx, demoLiveKitRoom(id)); err == nil {
				slog.Info("demo video room closed by sweep", "demo", id)
			}
		}
	}
}

func (s *Server) demoSweep(ctx context.Context) {
	now := time.Now()
	rows, err := s.db.Query(ctx, `UPDATE demo_rooms SET ended_at=expires_at, end_reason='expired' WHERE ended_at IS NULL AND expires_at <= $1 RETURNING id`, now)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			slog.Info("demo ended", "demo", id, "reason", "expired")
			s.releaseDemo(id, "expired")
		}
	}
	if keys := s.hub.EphemeralKeys(); len(keys) > 0 {
		ids := make([]string, 0, len(keys))
		for _, k := range keys {
			ids = append(ids, strings.TrimPrefix(k, realtime.EphemeralPrefix))
		}
		active := map[string]bool{}
		if rows, err := s.db.Query(ctx, `SELECT id::text FROM demo_rooms WHERE id::text = ANY($1) AND ended_at IS NULL AND expires_at > $2`, ids, now); err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					active[id] = true
				}
			}
			rows.Close()
			for _, id := range ids {
				if !active[id] {
					s.releaseDemo(id, "ended")
				}
			}
		}
	}
	s.sweepDemoVideoRooms(ctx, now)
	retention := time.Duration(s.cfg.DemoRetentionHours) * time.Hour
	if tag, err := s.db.Exec(ctx, `DELETE FROM demo_rooms WHERE coalesce(ended_at, expires_at) < $1`, now.Add(-retention)); err == nil && tag.RowsAffected() > 0 {
		slog.Info("demo records deleted", "count", tag.RowsAffected())
	}
	for _, l := range []*limiter{demoCreateLimiter, demoAcceptLimiter, demoFailLimiter, demoActionLimiter} {
		l.prune(2 * time.Hour)
	}
}
