package api

// Обращения «Не вижу урок / Не могу войти» с устройства пользователя и их разбор администратором.
//
// Браузер выполняет проверки (сессия, загрузка занятий, серверное время, собственные уроки) и по желанию
// пользователя отправляет сводку. Сервер:
//   - принимает только перечисленные поля (белый список имён проверок, коды ошибок и ID запросов по шаблону,
//     страница — только путь без параметров), без паролей, токенов, cookies и тел запросов/ответов;
//   - повторно проверяет СОБСТВЕННЫЕ уроки пользователя теми же правилами (ownLessons → lessonScopeCond,
//     classProblem) и предлагает причину; если её нет — «Причина не установлена»;
//   - фиксирует обращение в существующем журнале diagnostic_reports (без дублей по отпечатку).

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/db"
)

var deviceReportLimiter = &limiter{hits: map[string][]time.Time{}}

var (
	probeNames   = map[string]bool{"session": true, "config": true, "server_time": true, "lesson_check": true, "lessons_upcoming": true, "lessons_history": true, "slots": true, "join": true}
	outcomes     = map[string]bool{"lesson_found": true, "entry_not_open": true, "lesson_ended": true, "lesson_cancelled": true, "no_lessons": true, "load_failed": true, "session_expired": true, "server_unreachable": true, "clock_skew": true, "unknown": true}
	errCodeRe    = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	requestIDRe  = regexp.MustCompile(`^[0-9a-f]{6,32}$`)
	versionRe    = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,40}$`)
	clockSkewMax = int64(2 * time.Minute / time.Millisecond)
)

type clientProbe struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`    // HTTP-статус; 0 — нет ответа (сеть)
	ErrorCode string `json:"errorCode"` // например network, timeout, http_500
	RequestID string `json:"requestId"` // X-Request-ID ответа
	Ms        int    `json:"ms"`
}

type clientReport struct {
	FrontendVersion    string        `json:"frontendVersion"`
	BackendVersion     string        `json:"backendVersion"`
	Page               string        `json:"page"`
	CheckedAt          time.Time     `json:"checkedAt"` // по часам устройства
	ClockSkewMs        *int64        `json:"clockSkewMs"`
	ServerTimeReceived bool          `json:"serverTimeReceived"`
	Online             bool          `json:"online"`
	Probes             []clientProbe `json:"probes"`
	LessonID           string        `json:"lessonId"`
	TeacherID          string        `json:"teacherId"`
	Outcome            string        `json:"outcome"`
}

// safePath — только путь страницы: без схемы, хоста, параметров и фрагмента (там могут быть токены).
func safePath(p string) string {
	u, err := url.Parse(strings.TrimSpace(p))
	if err != nil || u.Path == "" || !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	path := u.Path
	if utf8.RuneCountInString(path) > 200 {
		path = string([]rune(path)[:200])
	}
	return path
}

func clean(v string, re *regexp.Regexp) string {
	if re.MatchString(v) {
		return v
	}
	return ""
}

// sanitize оставляет только разрешённые поля и значения.
func (r *clientReport) sanitize() {
	r.FrontendVersion = clean(r.FrontendVersion, versionRe)
	r.BackendVersion = clean(r.BackendVersion, versionRe)
	r.Page = safePath(r.Page)
	if !outcomes[r.Outcome] {
		r.Outcome = "unknown"
	}
	if !isUUID(r.LessonID) {
		r.LessonID = ""
	}
	if !isUUID(r.TeacherID) {
		r.TeacherID = ""
	}
	if r.ClockSkewMs != nil && (*r.ClockSkewMs > 10*365*24*3600*1000 || *r.ClockSkewMs < -10*365*24*3600*1000) {
		r.ClockSkewMs = nil
	}
	probes := []clientProbe{}
	for _, p := range r.Probes {
		if !probeNames[p.Name] || len(probes) >= 12 {
			continue
		}
		p.ErrorCode = clean(p.ErrorCode, errCodeRe)
		p.RequestID = clean(p.RequestID, requestIDRe)
		if p.Status < 0 || p.Status > 599 {
			p.Status = 0
		}
		if p.Ms < 0 || p.Ms > 600000 {
			p.Ms = 0
		}
		probes = append(probes, p)
	}
	r.Probes = probes
}

// deviceCause — серверная оценка причины по собственным урокам и результатам проверок браузера.
type deviceCause struct {
	Codes       []string `json:"codes"`
	Established bool     `json:"established"`
	Text        string   `json:"text"`
}

func assessDevice(r *clientReport, lessons []ownLessonCheck, selected *ownLessonCheck, now time.Time, tz string) deviceCause {
	var codes, texts []string
	add := func(code, text string) {
		codes = append(codes, code)
		texts = append(texts, text)
	}
	for _, p := range r.Probes {
		switch {
		case p.Status == 0 && !p.OK:
			add("network_error", fmt.Sprintf("Нет связи с сервером при проверке «%s» (%s).", p.Name, p.ErrorCode))
		case p.Status >= 500:
			add("api_error", fmt.Sprintf("Сервер вернул ошибку %d при проверке «%s» (запрос %s).", p.Status, p.Name, p.RequestID))
		}
	}
	if selected != nil {
		switch {
		case selected.Status == "cancelled":
			add("lesson_cancelled", "Урок отменён"+map[bool]string{true: " (" + selected.CancelReason + ")", false: ""}[selected.CancelReason != ""]+".")
		case selected.List == "history":
			add("lesson_ended", "Урок уже закончился и находится в «Истории».")
		case !selected.CanEnter && now.Before(selected.EntryOpensAt):
			add("entry_not_open", "Вход в класс ещё не открыт — откроется "+selected.EntryOpensAt.In(userLocation(tz)).Format("02.01 15:04")+".")
		case !selected.CanEnter:
			add("entry_closed", "Вход закрыт: "+selected.EntryProblem)
		}
	} else if len(lessons) == 0 {
		add("no_own_lessons", "У этого аккаунта нет подходящих занятий — возможно, урок записан на другой аккаунт (это нужно проверить по ID, а не по имени).")
	}
	if r.ClockSkewMs != nil && (*r.ClockSkewMs > clockSkewMax || *r.ClockSkewMs < -clockSkewMax) {
		add("clock_skew", fmt.Sprintf("Часы устройства отличаются от серверных на %d мин (доступ определяет сервер; на устройстве может неверно отображаться статус).", *r.ClockSkewMs/60000))
	}
	sort.Strings(codes)
	c := deviceCause{Codes: codes}
	if len(texts) == 0 {
		c.Text = "Причина не установлена: урок найден, загрузка и вход по серверным правилам работают."
		return c
	}
	c.Established = true
	c.Text = strings.Join(texts, " ")
	return c
}

// POST /api/me/diagnostics — сводка проверки с устройства; возвращает номер обращения.
func (s *Server) myDeviceReport(c *gin.Context) {
	u := currentUser(c)
	if u.Role != "student" && u.Role != "teacher" {
		fail(c, 403, "Отправка доступна ученикам и преподавателям")
		return
	}
	if !deviceReportLimiter.allow(u.ID, 5, time.Minute) {
		fail(c, 429, "Слишком частая отправка — подождите минуту")
		return
	}
	var r clientReport
	if err := bindLimited(c, &r, 16<<10); err != nil {
		return
	}
	r.sanitize()
	now := time.Now()
	lessons, _, err := s.ownLessons(c, u, r.LessonID, r.TeacherID, now)
	if err != nil {
		internal(c, err)
		return
	}
	var selected *ownLessonCheck
	if r.LessonID != "" && len(lessons) > 0 {
		selected = &lessons[0] // ownLessons по lessonId возвращает только собственный урок
	} else {
		r.LessonID = "" // чужой или несуществующий — не сохраняем
		for i := range lessons {
			if lessons[i].List == "upcoming" {
				selected = &lessons[i]
				break
			}
		}
	}
	cause := assessDevice(&r, lessons, selected, now, u.Timezone)
	var lessonID *string
	summary := []gin.H{}
	for _, l := range lessons {
		summary = append(summary, gin.H{"id": l.ID, "list": l.List, "status": l.Status, "canEnter": l.CanEnter, "startsAt": l.StartsAt, "endsAt": l.EndsAt})
	}
	if selected != nil {
		lessonID = &selected.ID
	}
	ua := c.GetHeader("User-Agent")
	if utf8.RuneCountInString(ua) > 300 {
		ua = string([]rune(ua)[:300])
	}
	client := gin.H{"report": r, "browser": ua, "receivedAt": now.UTC()}
	details := gin.H{"role": u.Role, "cause": cause, "ownLessons": summary, "serverTime": now.UTC()}
	day := now.In(userLocation(u.Timezone)).Format("2006-01-02")
	fp := fingerprint("user_device_check", u.ID, day, r.LessonID, r.Outcome, strings.Join(cause.Codes, ","))
	cj, _ := json.Marshal(client)
	dj, _ := json.Marshal(details)
	var code string
	var hits int
	for i := 0; i < 3; i++ {
		err = s.db.QueryRow(c, `INSERT INTO diagnostic_reports(code, kind, fingerprint, user_id, lesson_id, details, client, app_version)
			VALUES ($1,'user_device_check',$2,$3,$4,$5::jsonb,$6::jsonb,$7)
			ON CONFLICT (fingerprint) WHERE status='open'
			DO UPDATE SET hits = diagnostic_reports.hits + 1, last_seen_at = now(), updated_at = now(),
				details = EXCLUDED.details, client = EXCLUDED.client, app_version = EXCLUDED.app_version
			RETURNING code, hits`, newReportCode(), fp, u.ID, lessonID, string(dj), string(cj), r.FrontendVersion+"/"+s.cfg.AppVersion).Scan(&code, &hits)
		if cc, constraint := db.ConstraintName(err); cc == "23505" && constraint == "diagnostic_reports_code_key" {
			continue
		}
		break
	}
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"code": code, "hits": hits, "status": "saved", "cause": cause})
}

// ---- Администратор ----

var reviewStatuses = map[string]bool{"new": true, "in_progress": true, "resolved": true}

// GET /api/admin/diagnostics/reports?status=new|in_progress|resolved — обращения с данными браузера и сервера.
func (s *Server) adminDiagnosticReports(c *gin.Context) {
	where := ""
	args := []any{}
	if st := c.Query("status"); reviewStatuses[st] {
		where = "WHERE r.review_status=$1"
		args = append(args, st)
	}
	rows, err := s.db.Query(c, `SELECT r.code, r.kind, r.user_id, u.name, u.email, u.role, r.lesson_id, l.subject, l.starts_at, l.status,
		r.details, r.client, r.app_version, r.status, r.review_status, r.confirmed_cause, r.history, r.hits, r.created_at, r.last_seen_at
		FROM diagnostic_reports r LEFT JOIN users u ON u.id = r.user_id LEFT JOIN lessons l ON l.id = r.lesson_id
		`+where+` ORDER BY r.last_seen_at DESC LIMIT 200`, args...)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var code, kind, appVersion, status, review, cause string
		var uid, uname, email, urole, lid, subject, lstatus *string
		var starts *time.Time
		var details, client, history []byte
		var hits int
		var created, seen time.Time
		if err := rows.Scan(&code, &kind, &uid, &uname, &email, &urole, &lid, &subject, &starts, &lstatus, &details, &client, &appVersion, &status,
			&review, &cause, &history, &hits, &created, &seen); err != nil {
			internal(c, err)
			return
		}
		var user, lesson any
		if uid != nil {
			e := ""
			if email != nil {
				e = maskEmail(*email)
			}
			user = gin.H{"id": *uid, "name": deref(uname), "emailMasked": e, "role": deref(urole)}
		}
		if lid != nil {
			lesson = gin.H{"id": *lid, "subject": deref(subject), "startsAt": starts, "status": deref(lstatus)}
		}
		out = append(out, gin.H{"code": code, "kind": kind, "user": user, "lesson": lesson, "details": json.RawMessage(details),
			"client": json.RawMessage(client), "appVersion": appVersion, "status": status, "reviewStatus": review, "confirmedCause": cause,
			"history": json.RawMessage(history), "hits": hits, "createdAt": created, "lastSeenAt": seen})
	}
	c.JSON(200, gin.H{"reports": out})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// PATCH /api/admin/diagnostics/reports/:code {reviewStatus, confirmedCause, note}
// «Решено» — только с подтверждённой администратором причиной: существование записи в БД решением не считается.
// Смена статуса записывается в history. Данные занятий здесь не меняются.
func (s *Server) adminReviewReport(c *gin.Context) {
	var in struct {
		ReviewStatus   string `json:"reviewStatus"`
		ConfirmedCause string `json:"confirmedCause"`
		Note           string `json:"note"`
	}
	if err := bindLimited(c, &in, 8<<10); err != nil {
		return
	}
	in.ConfirmedCause, in.Note = strings.TrimSpace(in.ConfirmedCause), strings.TrimSpace(in.Note)
	switch {
	case !reviewStatuses[in.ReviewStatus]:
		fail(c, 400, "Статус: new, in_progress или resolved")
		return
	case in.ReviewStatus == "resolved" && utf8.RuneCountInString(in.ConfirmedCause) < 5:
		fail(c, 400, "Чтобы отметить «Решено», укажите подтверждённую причину")
		return
	case utf8.RuneCountInString(in.ConfirmedCause) > 1000 || utf8.RuneCountInString(in.Note) > 1000:
		fail(c, 400, "Слишком длинный текст")
		return
	}
	u := currentUser(c)
	var prev string
	if err := s.db.QueryRow(c, `SELECT review_status FROM diagnostic_reports WHERE code=$1`, c.Param("code")).Scan(&prev); err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Обращение не найдено")
			return
		}
		internal(c, err)
		return
	}
	entry, _ := json.Marshal([]gin.H{{"at": time.Now().UTC(), "by": u.ID, "from": prev, "to": in.ReviewStatus, "cause": in.ConfirmedCause, "note": in.Note}})
	// Решённое закрывается: новая проверка с тем же отпечатком создаст новое обращение. Повторное открытие
	// невозможно, если уже есть новое открытое обращение с тем же отпечатком.
	_, err := s.db.Exec(c, `UPDATE diagnostic_reports SET review_status=$2, confirmed_cause=$3,
		status = CASE WHEN $2='resolved' THEN 'closed' ELSE 'open' END,
		history = history || $4::jsonb, updated_at=now() WHERE code=$1`, c.Param("code"), in.ReviewStatus, in.ConfirmedCause, string(entry))
	if err != nil {
		if cc, _ := db.ConstraintName(err); cc == "23505" {
			fail(c, 409, "Есть новое открытое обращение с теми же данными — продолжите работу в нём")
			return
		}
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
