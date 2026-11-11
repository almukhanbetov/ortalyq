package api

// «Учебная мастерская»: совместный учебный текст (документ Yjs «workshop», см. realtime/code.go),
// разборы выделенных фрагментов и домашние задания с помощью ИИ.
//
// Права (проверяются здесь, на сервере):
//   - всё — только участникам урока (lessonAccess: посторонним 404);
//   - режим, запросы к ИИ, правка, публикация и удаление — только преподаватель этого урока;
//   - запросы к ИИ и смена режима — только пока открыт класс (classProblem); после завершения урока
//     опубликованные материалы остаются доступны участникам;
//   - ученик получает только опубликованные версии. Черновики и ответы к заданиям не попадают в ответы
//     ученику и в сообщения WebSocket: для ученика используются отдельные структуры без этих полей,
//     а по WebSocket рассылается только «workshop.changed» без содержимого.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/workshop"
)

var workshopAILimiter = &limiter{hits: map[string][]time.Time{}}

// aiInFlight — не больше одного запроса к ИИ на урок одновременно (защита от повторных нажатий).
type aiInFlight struct {
	mu   sync.Mutex
	busy map[string]bool
}

func (f *aiInFlight) acquire(lessonID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[lessonID] {
		return false
	}
	f.busy[lessonID] = true
	return true
}

func (f *aiInFlight) release(lessonID string) {
	f.mu.Lock()
	delete(f.busy, lessonID)
	f.mu.Unlock()
}

// SetAIProvider подменяет провайдера ИИ (тесты). nil — ИИ недоступен.
func (s *Server) SetAIProvider(p workshop.Provider) { s.ai = p }

func maxBytes(c *gin.Context, limit int64) io.ReadCloser {
	return http.MaxBytesReader(c.Writer, c.Request.Body, limit)
}

func isTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// ---- Представления ----

// studentAnalysis — то, что видит ученик: только опубликованная версия. Полей черновика нет.
type studentAnalysis struct {
	ID          string             `json:"id"`
	Mode        string             `json:"mode"`
	Action      string             `json:"action"`
	ActionLabel string             `json:"actionLabel"`
	Level       string             `json:"level"`
	Fragment    string             `json:"fragment"`
	Published   *workshop.Analysis `json:"published"`
	PublishedAt *time.Time         `json:"publishedAt"`
}

type teacherAnalysis struct {
	studentAnalysis
	Draft     workshop.Analysis `json:"draft"`
	Model     string            `json:"model"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// studentHomework — только опубликованные повторение и упражнения. Ответов и черновика нет.
type studentHomework struct {
	ID          string                  `json:"id"`
	Mode        string                  `json:"mode"`
	Level       string                  `json:"level"`
	Published   *workshop.HomeworkDraft `json:"published"`
	PublishedAt *time.Time              `json:"publishedAt"`
}

type teacherHomework struct {
	studentHomework
	Draft      workshop.HomeworkDraft `json:"draft"`
	AnswerKey  workshop.AnswerKey     `json:"answerKey"`
	SourceText string                 `json:"sourceText"`
	Model      string                 `json:"model"`
	CreatedAt  time.Time              `json:"createdAt"`
	UpdatedAt  time.Time              `json:"updatedAt"`
}

func actionLabel(mode, action string) string {
	if m, ok := workshop.ModeByID(mode); ok {
		if a, ok := m.Action(action); ok {
			return a.Label
		}
	}
	for _, m := range workshop.Modes() {
		if a, ok := m.Action(action); ok {
			return a.Label
		}
	}
	return action
}

func unmarshalPtr[T any](raw []byte) *T {
	if len(raw) == 0 {
		return nil
	}
	var v T
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return &v
}

func (s *Server) loadAnalyses(ctx context.Context, lessonID string, teacher bool) (any, error) {
	if !teacher {
		// Ученику черновик даже не читается из БД.
		rows, err := s.db.Query(ctx, `SELECT id, mode, action, level, fragment, published, published_at
			FROM workshop_analyses WHERE lesson_id=$1 AND published IS NOT NULL ORDER BY created_at`, lessonID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []studentAnalysis{}
		for rows.Next() {
			var a studentAnalysis
			var pub []byte
			if err := rows.Scan(&a.ID, &a.Mode, &a.Action, &a.Level, &a.Fragment, &pub, &a.PublishedAt); err != nil {
				return nil, err
			}
			a.ActionLabel = actionLabel(a.Mode, a.Action)
			a.Published = unmarshalPtr[workshop.Analysis](pub)
			out = append(out, a)
		}
		return out, rows.Err()
	}
	rows, err := s.db.Query(ctx, analysisSelect+` WHERE lesson_id=$1 ORDER BY created_at`, lessonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []teacherAnalysis{}
	for rows.Next() {
		a, err := scanTeacherAnalysis(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const analysisSelect = `SELECT id, mode, action, level, fragment, published, published_at, draft, model, created_at, updated_at FROM workshop_analyses`

func scanTeacherAnalysis(row interface{ Scan(...any) error }) (teacherAnalysis, error) {
	var a teacherAnalysis
	var pub, draft []byte
	err := row.Scan(&a.ID, &a.Mode, &a.Action, &a.Level, &a.Fragment, &pub, &a.PublishedAt, &draft, &a.Model, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	a.ActionLabel = actionLabel(a.Mode, a.Action)
	a.Published = unmarshalPtr[workshop.Analysis](pub)
	if d := unmarshalPtr[workshop.Analysis](draft); d != nil {
		a.Draft = *d
	}
	return a, nil
}

const homeworkSelect = `SELECT id, mode, level, published, published_at, draft, answer_key, source_text, model, created_at, updated_at FROM workshop_homework`

func scanTeacherHomework(row interface{ Scan(...any) error }) (teacherHomework, error) {
	var h teacherHomework
	var pub, draft, key []byte
	err := row.Scan(&h.ID, &h.Mode, &h.Level, &pub, &h.PublishedAt, &draft, &key, &h.SourceText, &h.Model, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return h, err
	}
	h.Published = unmarshalPtr[workshop.HomeworkDraft](pub)
	if d := unmarshalPtr[workshop.HomeworkDraft](draft); d != nil {
		h.Draft = *d
	}
	if k := unmarshalPtr[workshop.AnswerKey](key); k != nil {
		h.AnswerKey = *k
	}
	return h, nil
}

func (s *Server) loadWorkshopHomework(ctx context.Context, lessonID string, teacher bool) (any, error) {
	if !teacher {
		// Ученику ответы и черновик даже не читаются из БД.
		rows, err := s.db.Query(ctx, `SELECT id, mode, level, published, published_at
			FROM workshop_homework WHERE lesson_id=$1 AND published IS NOT NULL ORDER BY created_at`, lessonID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []studentHomework{}
		for rows.Next() {
			var h studentHomework
			var pub []byte
			if err := rows.Scan(&h.ID, &h.Mode, &h.Level, &pub, &h.PublishedAt); err != nil {
				return nil, err
			}
			h.Published = unmarshalPtr[workshop.HomeworkDraft](pub)
			out = append(out, h)
		}
		return out, rows.Err()
	}
	rows, err := s.db.Query(ctx, homeworkSelect+` WHERE lesson_id=$1 ORDER BY created_at`, lessonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []teacherHomework{}
	for rows.Next() {
		h, err := scanTeacherHomework(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Server) workshopSettings(ctx context.Context, lessonID string) (mode string, noticeAccepted bool, err error) {
	var ack *time.Time
	err = s.db.QueryRow(ctx, `SELECT mode, ai_notice_ack_at FROM workshop_settings WHERE lesson_id=$1`, lessonID).Scan(&mode, &ack)
	if db.IsNoRows(err) {
		return workshop.DefaultMode, false, nil
	}
	if _, ok := workshop.ModeByID(mode); !ok {
		mode = workshop.DefaultMode // режим убран из конфигурации — работаем в общем
	}
	return mode, ack != nil, err
}

// teacherOnly — доступ к уроку и роль преподавателя ЭТОГО урока (роль пользователя в системе недостаточна).
func (s *Server) workshopTeacher(c *gin.Context) (*lessonRow, bool) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return nil, false
	}
	if role != "teacher" {
		fail(c, 403, "Это действие доступно только преподавателю урока")
		return nil, false
	}
	return l, true
}

// ---- Обработчики ----

// GET /lessons/:id/workshop — состояние мастерской для текущего участника.
func (s *Server) getWorkshop(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	teacher := role == "teacher"
	mode, notice, err := s.workshopSettings(c, l.ID)
	if err != nil {
		internal(c, err)
		return
	}
	analyses, err := s.loadAnalyses(c, l.ID, teacher)
	if err != nil {
		internal(c, err)
		return
	}
	hw, err := s.loadWorkshopHomework(c, l.ID, teacher)
	if err != nil {
		internal(c, err)
		return
	}
	closed := s.classProblem(l, time.Now())
	out := gin.H{
		"role":         role,
		"subject":      l.Subject,
		"mode":         mode,
		"modes":        workshop.Modes(),
		"levels":       workshop.Levels(),
		"editable":     closed == "",
		"closedReason": closed,
		"analyses":     analyses,
		"homework":     hw,
		"limits":       gin.H{"fragmentChars": workshop.MaxFragmentRunes, "homeworkTextChars": workshop.MaxHomeworkTextRunes},
	}
	if teacher {
		out["suggestedMode"] = workshop.SuggestMode(l.Subject)
		out["ai"] = gin.H{"configured": s.ai != nil, "problem": s.aiProblem(), "noticeAccepted": notice}
	}
	c.JSON(200, out)
}

func (s *Server) aiProblem() string {
	if s.ai != nil {
		return ""
	}
	if p := s.cfg.AIProblem(); p != "" {
		return p
	}
	return "ИИ не настроен на сервере"
}

// GET /lessons/:id/workshop/doc — сохранённый учебный текст (журнал Yjs) для просмотра после урока.
func (s *Server) getWorkshopDoc(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	ups, seq := s.hub.WorkshopDocUpdates(c, l.ID)
	c.JSON(200, gin.H{"updates": ups, "seq": seq})
}

// PUT /lessons/:id/workshop/mode {mode}
func (s *Server) setWorkshopMode(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok {
		return
	}
	if msg := s.classProblem(l, time.Now()); msg != "" {
		fail(c, 409, "Режим меняется только во время урока: "+msg)
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if _, ok := workshop.ModeByID(in.Mode); !ok {
		fail(c, 400, "Такого режима нет")
		return
	}
	if _, err := s.db.Exec(c, `INSERT INTO workshop_settings(lesson_id, mode, updated_by) VALUES ($1,$2,$3)
		ON CONFLICT (lesson_id) DO UPDATE SET mode=$2, updated_by=$3, updated_at=now()`, l.ID, in.Mode, currentUser(c).ID); err != nil {
		internal(c, err)
		return
	}
	s.hub.Broadcast(l.ID, gin.H{"type": "workshop.changed"})
	c.JSON(200, gin.H{"mode": in.Mode})
}

// POST /lessons/:id/workshop/ai-notice — преподаватель подтвердил, что выбранный текст передаётся внешнему ИИ.
func (s *Server) ackWorkshopNotice(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok {
		return
	}
	if _, err := s.db.Exec(c, `INSERT INTO workshop_settings(lesson_id, ai_notice_ack_at, updated_by) VALUES ($1, now(), $2)
		ON CONFLICT (lesson_id) DO UPDATE SET ai_notice_ack_at=coalesce(workshop_settings.ai_notice_ack_at, now())`, l.ID, currentUser(c).ID); err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// aiPreflight — общие проверки перед запросом к ИИ. Возвращает режим и функцию освобождения.
func (s *Server) aiPreflight(c *gin.Context, l *lessonRow) (*workshop.Mode, func(), bool) {
	if msg := s.classProblem(l, time.Now()); msg != "" {
		fail(c, 409, "Запросы к ИИ доступны только во время урока: "+msg)
		return nil, nil, false
	}
	if s.ai == nil {
		c.AbortWithStatusJSON(503, gin.H{"error": "Функции ИИ недоступны: " + s.aiProblem(), "code": "ai_not_configured"})
		return nil, nil, false
	}
	modeID, notice, err := s.workshopSettings(c, l.ID)
	if err != nil {
		internal(c, err)
		return nil, nil, false
	}
	if !notice {
		c.AbortWithStatusJSON(428, gin.H{"error": "Подтвердите, что выбранный текст будет передан внешнему ИИ", "code": "notice_required"})
		return nil, nil, false
	}
	u := currentUser(c)
	if !workshopAILimiter.allow("u:"+u.ID, s.cfg.WorkshopAIPerMinute, time.Minute) {
		fail(c, 429, "Слишком частые запросы к ИИ — подождите минуту")
		return nil, nil, false
	}
	if !workshopAILimiter.allow("l:"+l.ID, s.cfg.WorkshopAIPerHour, time.Hour) {
		fail(c, 429, "Исчерпан лимит запросов к ИИ для этого урока на ближайший час")
		return nil, nil, false
	}
	if !s.aiBusy.acquire(l.ID) {
		fail(c, 409, "Предыдущий запрос к ИИ ещё выполняется")
		return nil, nil, false
	}
	m, _ := workshop.ModeByID(modeID)
	return m, func() { s.aiBusy.release(l.ID) }, true
}

func (s *Server) aiFailed(c *gin.Context, lessonID, what string, start time.Time, err error) {
	pe := workshop.AsProviderError(err)
	// В журнал — только урок, вид запроса, категория и время. Без текста, ответа провайдера и ключа.
	slog.Warn("workshop ai", "lesson", lessonID, "what", what, "kind", pe.Kind, "providerStatus", pe.Status, "ms", time.Since(start).Milliseconds())
	status := 502
	if pe.Kind == workshop.ErrTimeout {
		status = 504
	}
	c.AbortWithStatusJSON(status, gin.H{"error": pe.UserMessage(), "code": string(pe.Kind)})
}

func validText(s string, max int) (string, string) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	switch {
	case s == "":
		return "", "Выделите фрагмент текста"
	case !utf8.ValidString(s):
		return "", "Текст должен быть в UTF-8"
	case utf8.RuneCountInString(s) > max:
		return "", "Слишком большой фрагмент: максимум " + itoa(max) + " символов"
	}
	return s, ""
}

// POST /lessons/:id/workshop/analyses {action, level, fragment}
func (s *Server) createAnalysis(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok {
		return
	}
	var in struct {
		Action   string `json:"action"`
		Level    string `json:"level"`
		Fragment string `json:"fragment"`
	}
	if err := bindLimited(c, &in, 64<<10); err != nil {
		return
	}
	fragment, msg := validText(in.Fragment, workshop.MaxFragmentRunes)
	if msg != "" {
		fail(c, 400, msg)
		return
	}
	lv, ok := workshop.LevelByID(in.Level)
	if !ok {
		fail(c, 400, "Выберите сложность")
		return
	}
	m, release, ok := s.aiPreflight(c, l)
	if !ok {
		return
	}
	defer release()
	act, ok := m.Action(in.Action)
	if !ok {
		fail(c, 400, "Это действие недоступно в режиме «"+m.Label+"»")
		return
	}
	start := time.Now()
	res, model, err := workshop.RunAnalysis(c.Request.Context(), s.ai, m, act, lv, l.Subject, fragment)
	if err != nil {
		s.aiFailed(c, l.ID, "analysis", start, err)
		return
	}
	slog.Info("workshop ai", "lesson", l.ID, "what", "analysis", "mode", m.ID, "action", act.ID, "ms", time.Since(start).Milliseconds())
	draft, _ := json.Marshal(res)
	a, err := scanTeacherAnalysis(s.db.QueryRow(c, `INSERT INTO workshop_analyses(lesson_id, created_by, mode, action, level, fragment, draft, model)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8) RETURNING id, mode, action, level, fragment, published, published_at, draft, model, created_at, updated_at`,
		l.ID, currentUser(c).ID, m.ID, act.ID, lv.ID, fragment, string(draft), model))
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"analysis": a})
}

func bindLimited(c *gin.Context, v any, limit int64) error {
	c.Request.Body = maxBytes(c, limit)
	if err := c.ShouldBindJSON(v); err != nil {
		if isTooLarge(err) {
			fail(c, 413, "Слишком большой запрос")
		} else {
			fail(c, 400, "Некорректный запрос")
		}
		return err
	}
	return nil
}

// editable — правка и публикация материалов: урок не отменён (после завершения — можно).
func editableLesson(c *gin.Context, l *lessonRow) bool {
	if l.Status == "cancelled" {
		fail(c, 409, "Занятие отменено")
		return false
	}
	return true
}

func (s *Server) analysisForTeacher(c *gin.Context, l *lessonRow) (*teacherAnalysis, bool) {
	aid := c.Param("aid")
	if !isUUID(aid) {
		fail(c, 404, "Разбор не найден")
		return nil, false
	}
	// Поиск только среди разборов этого урока: чужой разбор по id недоступен.
	a, err := scanTeacherAnalysis(s.db.QueryRow(c, analysisSelect+` WHERE id=$1 AND lesson_id=$2`, aid, l.ID))
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Разбор не найден")
		} else {
			internal(c, err)
		}
		return nil, false
	}
	return &a, true
}

// PUT /lessons/:id/workshop/analyses/:aid {draft}
func (s *Server) updateAnalysis(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok || !editableLesson(c, l) {
		return
	}
	a, ok := s.analysisForTeacher(c, l)
	if !ok {
		return
	}
	var in struct {
		Draft workshop.Analysis `json:"draft"`
	}
	if err := bindLimited(c, &in, 256<<10); err != nil {
		return
	}
	verdictAction := false
	if m, ok := workshop.ModeByID(a.Mode); ok {
		if act, ok := m.Action(a.Action); ok {
			verdictAction = act.Verdict
		}
	}
	if err := workshop.NormalizeAnalysis(&in.Draft, verdictAction); err != nil {
		fail(c, 400, "Разбор должен содержать хотя бы один раздел (до 10 разделов, до 6000 символов в разделе)")
		return
	}
	draft, _ := json.Marshal(in.Draft)
	upd, err := scanTeacherAnalysis(s.db.QueryRow(c, `UPDATE workshop_analyses SET draft=$3::jsonb, updated_at=now() WHERE id=$1 AND lesson_id=$2
		RETURNING id, mode, action, level, fragment, published, published_at, draft, model, created_at, updated_at`, a.ID, l.ID, string(draft)))
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"analysis": upd})
}

// POST /lessons/:id/workshop/analyses/:aid/publish | /unpublish
func (s *Server) publishAnalysis(publish bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		l, ok := s.workshopTeacher(c)
		if !ok || !editableLesson(c, l) {
			return
		}
		a, ok := s.analysisForTeacher(c, l)
		if !ok {
			return
		}
		q := `UPDATE workshop_analyses SET published=draft, published_at=now(), updated_at=now() WHERE id=$1 AND lesson_id=$2`
		if !publish {
			q = `UPDATE workshop_analyses SET published=NULL, published_at=NULL, updated_at=now() WHERE id=$1 AND lesson_id=$2`
		}
		upd, err := scanTeacherAnalysis(s.db.QueryRow(c, q+` RETURNING id, mode, action, level, fragment, published, published_at, draft, model, created_at, updated_at`, a.ID, l.ID))
		if err != nil {
			internal(c, err)
			return
		}
		s.hub.Broadcast(l.ID, gin.H{"type": "workshop.changed"})
		c.JSON(200, gin.H{"analysis": upd})
	}
}

// DELETE /lessons/:id/workshop/analyses/:aid
func (s *Server) deleteAnalysis(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok || !editableLesson(c, l) {
		return
	}
	a, ok := s.analysisForTeacher(c, l)
	if !ok {
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM workshop_analyses WHERE id=$1 AND lesson_id=$2`, a.ID, l.ID); err != nil {
		internal(c, err)
		return
	}
	if a.Published != nil {
		s.hub.Broadcast(l.ID, gin.H{"type": "workshop.changed"})
	}
	c.JSON(200, gin.H{"ok": true})
}

// POST /lessons/:id/workshop/homework {text, level}
func (s *Server) createWorkshopHomework(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok {
		return
	}
	var in struct {
		Text  string `json:"text"`
		Level string `json:"level"`
	}
	if err := bindLimited(c, &in, 128<<10); err != nil {
		return
	}
	text, msg := validText(in.Text, workshop.MaxHomeworkTextRunes)
	if msg != "" {
		if text == "" && strings.TrimSpace(in.Text) == "" {
			msg = "Выделите учебный текст для задания (или оставьте выделение пустым, чтобы взять весь текст)"
		}
		fail(c, 400, msg)
		return
	}
	lv, ok := workshop.LevelByID(in.Level)
	if !ok {
		fail(c, 400, "Выберите сложность")
		return
	}
	m, release, ok := s.aiPreflight(c, l)
	if !ok {
		return
	}
	defer release()
	// Только опубликованные разборы этого урока (последние MaxPublishedForHW).
	rows, err := s.db.Query(c, `SELECT * FROM (SELECT mode, action, fragment, published, created_at FROM workshop_analyses
		WHERE lesson_id=$1 AND published IS NOT NULL ORDER BY created_at DESC LIMIT $2) t ORDER BY created_at`, l.ID, workshop.MaxPublishedForHW)
	if err != nil {
		internal(c, err)
		return
	}
	var published []workshop.PublishedAnalysis
	for rows.Next() {
		var mode, action, fragment string
		var pub []byte
		var at time.Time
		if err := rows.Scan(&mode, &action, &fragment, &pub, &at); err != nil {
			rows.Close()
			internal(c, err)
			return
		}
		if p := unmarshalPtr[workshop.Analysis](pub); p != nil {
			published = append(published, workshop.PublishedAnalysis{Action: actionLabel(mode, action), Fragment: fragment, Result: *p})
		}
	}
	rows.Close()
	start := time.Now()
	draft, key, model, err := workshop.RunHomework(c.Request.Context(), s.ai, m, lv, l.Subject, text, published)
	if err != nil {
		s.aiFailed(c, l.ID, "homework", start, err)
		return
	}
	slog.Info("workshop ai", "lesson", l.ID, "what", "homework", "mode", m.ID, "analyses", len(published), "ms", time.Since(start).Milliseconds())
	db1, _ := json.Marshal(draft)
	db2, _ := json.Marshal(key)
	h, err := scanTeacherHomework(s.db.QueryRow(c, `INSERT INTO workshop_homework(lesson_id, created_by, mode, level, source_text, draft, answer_key, model)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8) RETURNING id, mode, level, published, published_at, draft, answer_key, source_text, model, created_at, updated_at`,
		l.ID, currentUser(c).ID, m.ID, lv.ID, text, string(db1), string(db2), model))
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"homework": h})
}

func (s *Server) homeworkForTeacher(c *gin.Context, l *lessonRow) (*teacherHomework, bool) {
	hid := c.Param("hid")
	if !isUUID(hid) {
		fail(c, 404, "Задание не найдено")
		return nil, false
	}
	h, err := scanTeacherHomework(s.db.QueryRow(c, homeworkSelect+` WHERE id=$1 AND lesson_id=$2`, hid, l.ID))
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Задание не найдено")
		} else {
			internal(c, err)
		}
		return nil, false
	}
	return &h, true
}

const homeworkReturning = ` RETURNING id, mode, level, published, published_at, draft, answer_key, source_text, model, created_at, updated_at`

// PUT /lessons/:id/workshop/homework/:hid {draft, answerKey}
func (s *Server) updateWorkshopHomework(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok || !editableLesson(c, l) {
		return
	}
	h, ok := s.homeworkForTeacher(c, l)
	if !ok {
		return
	}
	var in struct {
		Draft     workshop.HomeworkDraft `json:"draft"`
		AnswerKey workshop.AnswerKey     `json:"answerKey"`
	}
	if err := bindLimited(c, &in, 256<<10); err != nil {
		return
	}
	if err := workshop.NormalizeHomework(&in.Draft, &in.AnswerKey); err != nil {
		fail(c, 400, "Задание должно содержать повторение темы и ровно 5 упражнений с инструкциями и ответами")
		return
	}
	d, _ := json.Marshal(in.Draft)
	k, _ := json.Marshal(in.AnswerKey)
	upd, err := scanTeacherHomework(s.db.QueryRow(c, `UPDATE workshop_homework SET draft=$3::jsonb, answer_key=$4::jsonb, updated_at=now()
		WHERE id=$1 AND lesson_id=$2`+homeworkReturning, h.ID, l.ID, string(d), string(k)))
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"homework": upd})
}

// POST /lessons/:id/workshop/homework/:hid/publish | /unpublish — публикуется только draft (без answer_key).
func (s *Server) publishWorkshopHomework(publish bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		l, ok := s.workshopTeacher(c)
		if !ok || !editableLesson(c, l) {
			return
		}
		h, ok := s.homeworkForTeacher(c, l)
		if !ok {
			return
		}
		q := `UPDATE workshop_homework SET published=draft, published_at=now(), updated_at=now() WHERE id=$1 AND lesson_id=$2`
		if !publish {
			q = `UPDATE workshop_homework SET published=NULL, published_at=NULL, updated_at=now() WHERE id=$1 AND lesson_id=$2`
		}
		upd, err := scanTeacherHomework(s.db.QueryRow(c, q+homeworkReturning, h.ID, l.ID))
		if err != nil {
			internal(c, err)
			return
		}
		s.hub.Broadcast(l.ID, gin.H{"type": "workshop.changed"})
		c.JSON(200, gin.H{"homework": upd})
	}
}

// DELETE /lessons/:id/workshop/homework/:hid
func (s *Server) deleteWorkshopHomework(c *gin.Context) {
	l, ok := s.workshopTeacher(c)
	if !ok || !editableLesson(c, l) {
		return
	}
	h, ok := s.homeworkForTeacher(c, l)
	if !ok {
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM workshop_homework WHERE id=$1 AND lesson_id=$2`, h.ID, l.ID); err != nil {
		internal(c, err)
		return
	}
	if h.Published != nil {
		s.hub.Broadcast(l.ID, gin.H{"type": "workshop.changed"})
	}
	c.JSON(200, gin.H{"ok": true})
}
