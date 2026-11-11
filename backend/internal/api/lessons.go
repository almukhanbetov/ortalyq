package api

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/lk"
)

// ---- Доступность преподавателя ----

// myAvailability — расписание преподавателя: интервалы доступности, уже занятые занятиями интервалы
// и рассчитанное свободное время (доступность минус занятия, с текущего момента).
func (s *Server) myAvailability(c *gin.Context) {
	teacherID := currentUser(c).ID
	now := time.Now()
	rows, err := s.db.Query(c, `SELECT id, starts_at, ends_at FROM availability WHERE teacher_id=$1 AND ends_at > $2 ORDER BY starts_at`, teacherID, now)
	if err != nil {
		internal(c, err)
		return
	}
	out := []gin.H{}
	var avail []span
	for rows.Next() {
		var id string
		var a, b time.Time
		if err := rows.Scan(&id, &a, &b); err != nil {
			rows.Close()
			internal(c, err)
			return
		}
		out = append(out, gin.H{"id": id, "startsAt": a, "endsAt": b})
		avail = append(avail, span{a, b})
	}
	rows.Close()
	// Занятые интервалы — те же, что вычитаются при записи (все неотменённые уроки, ещё не закончившиеся).
	lrows, err := s.db.Query(c, `SELECT l.id, l.starts_at, l.ends_at, l.status, l.subject, st.name, l.always_open FROM lessons l
		JOIN users st ON st.id = l.student_id
		WHERE l.teacher_id=$1 AND l.status <> 'cancelled' AND l.ends_at > $2 ORDER BY l.starts_at`, teacherID, now)
	if err != nil {
		internal(c, err)
		return
	}
	booked := []gin.H{}
	var busy []span
	for lrows.Next() {
		var id, status, subject, student string
		var a, b time.Time
		var open bool
		if err := lrows.Scan(&id, &a, &b, &status, &subject, &student, &open); err != nil {
			lrows.Close()
			internal(c, err)
			return
		}
		booked = append(booked, gin.H{"id": id, "startsAt": a, "endsAt": b, "status": status, "subject": subject, "studentName": student, "alwaysOpen": open})
		busy = append(busy, span{a, b})
	}
	lrows.Close()
	free := []gin.H{}
	for _, f := range futureFree(avail, busy, now) {
		free = append(free, gin.H{"startsAt": f.From, "endsAt": f.To})
	}
	c.JSON(200, gin.H{"availability": out, "lessons": booked, "free": free})
}

func (s *Server) addAvailability(c *gin.Context) {
	var req struct {
		StartsAt time.Time `json:"startsAt"`
		EndsAt   time.Time `json:"endsAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Укажите начало и конец интервала")
		return
	}
	switch {
	case !req.EndsAt.After(req.StartsAt):
		fail(c, 400, "Конец интервала должен быть позже начала")
		return
	case req.EndsAt.Sub(req.StartsAt) < 30*time.Minute:
		fail(c, 400, "Интервал должен быть не короче 30 минут")
		return
	case req.EndsAt.Sub(req.StartsAt) > 14*time.Hour:
		fail(c, 400, "Интервал не может быть длиннее 14 часов")
		return
	case req.EndsAt.Before(time.Now()):
		fail(c, 400, "Интервал уже прошёл")
		return
	case req.StartsAt.After(time.Now().Add(90 * 24 * time.Hour)):
		fail(c, 400, "Можно планировать не более чем на 90 дней вперёд")
		return
	}
	var id string
	err := s.db.QueryRow(c, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3) RETURNING id`,
		currentUser(c).ID, req.StartsAt.UTC(), req.EndsAt.UTC()).Scan(&id)
	if err != nil {
		if code, _ := db.ConstraintName(err); code == "23P01" {
			fail(c, 409, "Интервал пересекается с уже добавленным")
			return
		}
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) deleteAvailability(c *gin.Context) {
	if !isUUID(c.Param("id")) {
		fail(c, 404, "Интервал не найден")
		return
	}
	tag, err := s.db.Exec(c, `DELETE FROM availability WHERE id=$1 AND teacher_id=$2`, c.Param("id"), currentUser(c).ID)
	if err != nil {
		internal(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 404, "Интервал не найден")
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---- Бронирование ----

func (s *Server) bookLesson(c *gin.Context) {
	u := currentUser(c)
	var req struct {
		TeacherID string    `json:"teacherId"`
		StartsAt  time.Time `json:"startsAt"`
		Duration  int       `json:"duration"`
		Subject   string    `json:"subject"`
		Note      string    `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !isUUID(req.TeacherID) {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if !validDuration(req.Duration) {
		fail(c, 400, "Недопустимая длительность")
		return
	}
	// Начало — любая минута (сетки 30 минут нет); время не сдвигается, недопустимое отклоняется с причиной.
	start := req.StartsAt.UTC()
	end := start.Add(time.Duration(req.Duration) * time.Minute)
	loc := userLocation(u.Timezone)
	now := time.Now()
	switch {
	case req.StartsAt.IsZero():
		fail(c, 400, "Укажите дату и время начала")
		return
	case start.Second() != 0 || start.Nanosecond() != 0:
		fail(c, 400, "Время начала указывается с точностью до минуты (ЧЧ:ММ)")
		return
	case !start.After(now):
		fail(c, 400, fmt.Sprintf("Время начала уже прошло (сейчас %s, %s). Выберите время в будущем.", hm(now, loc), loc.String()))
		return
	case start.After(now.Add(90 * 24 * time.Hour)):
		fail(c, 400, "Записаться можно не более чем на 90 дней вперёд")
		return
	}
	if utf8.RuneCountInString(req.Note) > 1000 {
		fail(c, 400, "Комментарий слишком длинный")
		return
	}
	var subjects []string
	var price int
	err := s.db.QueryRow(c, `SELECT p.subjects, p.price FROM teacher_profiles p JOIN users u ON u.id=p.user_id
		WHERE p.user_id=$1 AND u.role='teacher' AND NOT u.blocked`, req.TeacherID).Scan(&subjects, &price)
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Преподаватель не найден")
			return
		}
		internal(c, err)
		return
	}
	subject := strings.TrimSpace(req.Subject)
	okSubject := false
	for _, sub := range subjects {
		if sub == subject {
			okSubject = true
		}
	}
	if !okSubject {
		if len(subjects) == 0 {
			fail(c, 400, "Преподаватель ещё не указал предметы")
			return
		}
		subject = subjects[0]
	}
	// Весь урок должен входить в доступность (соседние окна объединяются).
	avail, err := s.spans(c, `SELECT starts_at, ends_at FROM availability WHERE teacher_id=$1 AND ends_at >= $2 AND starts_at <= $3`, req.TeacherID, start, end)
	if err != nil {
		internal(c, err)
		return
	}
	if msg := availabilityProblem(avail, start, end, loc); msg != "" {
		fail(c, 409, msg)
		return
	}
	// Понятная причина пересечения. Окончательную защиту (в т.ч. от одновременных запросов) даёт EXCLUDE в БД ниже.
	var bs, be time.Time
	err = s.db.QueryRow(c, `SELECT starts_at, ends_at FROM lessons WHERE teacher_id=$1 AND status <> 'cancelled'
		AND tstzrange(starts_at, ends_at) && tstzrange($2, $3) ORDER BY starts_at LIMIT 1`, req.TeacherID, start, end).Scan(&bs, &be)
	if err == nil {
		fail(c, 409, fmt.Sprintf("Это время пересекается с другим занятием преподавателя (%s–%s). Выберите другое время.", hm(bs, loc), hm(be, loc)))
		return
	} else if !db.IsNoRows(err) {
		internal(c, err)
		return
	}
	err = s.db.QueryRow(c, `SELECT starts_at, ends_at FROM lessons WHERE student_id=$1 AND status <> 'cancelled'
		AND tstzrange(starts_at, ends_at) && tstzrange($2, $3) ORDER BY starts_at LIMIT 1`, u.ID, start, end).Scan(&bs, &be)
	if err == nil {
		fail(c, 409, fmt.Sprintf("У вас уже есть занятие %s %s–%s, которое пересекается с выбранным временем.", dm(bs, loc), hm(bs, loc), hm(be, loc)))
		return
	} else if !db.IsNoRows(err) {
		internal(c, err)
		return
	}
	total := price * req.Duration / 60
	var id string
	// Пересечения исключаются ограничениями EXCLUDE в БД — это атомарно и при одновременных запросах.
	err = s.db.QueryRow(c, `INSERT INTO lessons(teacher_id, student_id, subject, note, starts_at, ends_at, price)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, req.TeacherID, u.ID, subject, strings.TrimSpace(req.Note), start, end, total).Scan(&id)
	if err != nil {
		code, constraint := db.ConstraintName(err)
		if code == "23P01" {
			if constraint == "lessons_student_no_overlap" {
				fail(c, 409, "У вас уже есть занятие в это время")
			} else {
				fail(c, 409, "Это время только что занял другой ученик. Выберите другое время.")
			}
			return
		}
		if code == "23514" {
			fail(c, 400, "Нельзя записаться к самому себе")
			return
		}
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"id": id})
}

// ---- Чтение занятий ----

type lessonRow struct {
	ID              string     `json:"id"`
	TeacherID       string     `json:"teacherId"`
	TeacherName     string     `json:"teacherName"`
	StudentID       string     `json:"studentId"`
	StudentName     string     `json:"studentName"`
	Subject         string     `json:"subject"`
	Note            string     `json:"note"`
	StartsAt        time.Time  `json:"startsAt"`
	EndsAt          time.Time  `json:"endsAt"`
	Status          string     `json:"status"`
	StartedAt       *time.Time `json:"startedAt"`
	EndedAt         *time.Time `json:"endedAt"`
	CancelReason    string     `json:"cancelReason"`
	Price           int        `json:"price"`
	IsDemo          bool       `json:"isDemo"`
	HasTeacherPhoto bool       `json:"hasTeacherPhoto"`
	AlwaysOpen      bool       `json:"alwaysOpen"`
}

const lessonSelect = `SELECT l.id, l.teacher_id, t.name, l.student_id, st.name, l.subject, l.note, l.starts_at, l.ends_at, l.status,
	l.started_at, l.ended_at, l.cancel_reason, l.price, l.is_demo, (tp.photo_key IS NOT NULL), l.always_open
	FROM lessons l JOIN users t ON t.id = l.teacher_id JOIN users st ON st.id = l.student_id
	LEFT JOIN teacher_profiles tp ON tp.user_id = l.teacher_id`

func scanLesson(row interface{ Scan(...any) error }) (lessonRow, error) {
	var l lessonRow
	err := row.Scan(&l.ID, &l.TeacherID, &l.TeacherName, &l.StudentID, &l.StudentName, &l.Subject, &l.Note, &l.StartsAt, &l.EndsAt,
		&l.Status, &l.StartedAt, &l.EndedAt, &l.CancelReason, &l.Price, &l.IsDemo, &l.HasTeacherPhoto, &l.AlwaysOpen)
	return l, err
}

func (s *Server) myLessons(c *gin.Context) {
	u := currentUser(c)
	where := "l.student_id = $1"
	if u.Role == "teacher" {
		where = "l.teacher_id = $1"
	} else if u.Role == "admin" {
		c.JSON(200, gin.H{"lessons": []any{}})
		return
	}
	q := lessonSelect + " WHERE " + where
	// Граница «предстоящие / история» — момент окончания урока ($2, абсолютное время):
	// урок 17:20–18:20 остаётся в предстоящих (в том числе идущий) до 18:20 и переходит в историю ровно в 18:20.
	// Постоянно открытые демо-уроки не стареют.
	args := []any{u.ID}
	switch c.Query("scope") {
	case "upcoming":
		q += " AND " + lessonScopeCond("upcoming", "$2") + " ORDER BY l.always_open DESC, l.starts_at ASC"
		args = append(args, time.Now())
	case "history":
		q += " AND " + lessonScopeCond("history", "$2") + " ORDER BY l.starts_at DESC"
		args = append(args, time.Now())
	default:
		q += " ORDER BY l.starts_at DESC"
	}
	q += " LIMIT 200"
	rows, err := s.db.Query(c, q, args...)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	out := []lessonRow{}
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			internal(c, err)
			return
		}
		out = append(out, l)
	}
	c.JSON(200, gin.H{"lessons": out})
}

// lessonScopeCond — условие SQL списка «Предстоящие» или «История» (единственное место правила;
// его используют выдача /api/lessons и диагностика). nowArg — параметр с текущим моментом сервера.
func lessonScopeCond(scope, nowArg string) string {
	switch scope {
	case "upcoming":
		return "l.status IN ('scheduled','live') AND (l.always_open OR l.ends_at > " + nowArg + ")"
	case "history":
		return "(l.status IN ('completed','cancelled') OR (NOT l.always_open AND l.ends_at <= " + nowArg + "))"
	}
	return "true"
}

func (s *Server) myStudents(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT st.id, st.name, count(*) FILTER (WHERE l.status='completed'),
		count(*) FILTER (WHERE l.status IN ('scheduled','live')), max(l.starts_at)
		FROM lessons l JOIN users st ON st.id = l.student_id
		WHERE l.teacher_id = $1 AND l.status <> 'cancelled' GROUP BY st.id, st.name ORDER BY max(l.starts_at) DESC`, currentUser(c).ID)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, name string
		var done, upcoming int
		var last time.Time
		if err := rows.Scan(&id, &name, &done, &upcoming, &last); err != nil {
			internal(c, err)
			return
		}
		out = append(out, gin.H{"id": id, "name": name, "completed": done, "upcoming": upcoming, "lastLessonAt": last})
	}
	c.JSON(200, gin.H{"students": out})
}

// lessonAccess загружает занятие и проверяет, что текущий пользователь — его участник.
// Посторонние получают 404, чтобы не раскрывать существование занятия.
func (s *Server) lessonAccess(c *gin.Context) (*lessonRow, string, bool) {
	u := currentUser(c)
	id := c.Param("id")
	if !isUUID(id) {
		fail(c, 404, "Занятие не найдено")
		return nil, "", false
	}
	l, err := scanLesson(s.db.QueryRow(c, lessonSelect+" WHERE l.id = $1", id))
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Занятие не найдено")
		} else {
			internal(c, err)
		}
		return nil, "", false
	}
	switch u.ID {
	case l.TeacherID:
		return &l, "teacher", true
	case l.StudentID:
		return &l, "student", true
	}
	fail(c, 404, "Занятие не найдено")
	return nil, "", false
}

// joinWindow — полуинтервал [opens, closes), в котором открыт вход в класс:
// за JoinEarlyMinutes до начала и до момента окончания урока. Опоздание не сдвигает и не продлевает урок:
// ученик, вошедший в 17:25 на урок 17:20–18:20, занимается до 18:20.
func (s *Server) joinWindow(l *lessonRow) (time.Time, time.Time) {
	if l.AlwaysOpen {
		// Демо-класс открыт всегда
		return time.Now().Add(-24 * time.Hour), time.Now().Add(24 * time.Hour)
	}
	return l.StartsAt.Add(-time.Duration(s.cfg.JoinEarlyMinutes) * time.Minute), l.EndsAt
}

// classProblem объясняет, почему в класс занятия сейчас войти нельзя; "" — можно.
// Единая серверная проверка для видеосвязи, WebSocket класса (доска, чат, код) и запуска кода.
func (s *Server) classProblem(l *lessonRow, now time.Time) string {
	switch l.Status {
	case "cancelled":
		return "Занятие отменено — вход в класс закрыт"
	case "completed":
		return "Занятие завершено — вход в класс закрыт"
	}
	if l.AlwaysOpen {
		return "" // демо-класс открыт в любое время
	}
	opens, closes := s.joinWindow(l)
	if now.Before(opens) {
		return "Вход в класс откроется за " + itoa(s.cfg.JoinEarlyMinutes) + " минут до начала"
	}
	if !now.Before(closes) {
		return "Время занятия истекло — вход в класс закрыт"
	}
	return ""
}

func (s *Server) getLesson(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	s.refreshActiveRecordings(c, l.ID)
	opens, closes := s.joinWindow(l)
	canJoin := s.classProblem(l, time.Now()) == ""

	materials, err := s.lessonMaterials(c, l.ID)
	if err != nil {
		internal(c, err)
		return
	}
	homework, err := s.lessonHomework(c, l.ID)
	if err != nil {
		internal(c, err)
		return
	}
	recordings, err := s.lessonRecordings(c, l.ID)
	if err != nil {
		internal(c, err)
		return
	}
	var review gin.H
	var rating int
	var rbody string
	var rat time.Time
	if err := s.db.QueryRow(c, `SELECT rating, body, created_at FROM reviews WHERE lesson_id=$1`, l.ID).Scan(&rating, &rbody, &rat); err == nil {
		review = gin.H{"rating": rating, "body": rbody, "createdAt": rat}
	}
	var boardRev int64
	var boardCount int
	s.db.QueryRow(c, `SELECT revision, (SELECT count(*) FROM jsonb_array_elements(elements) e WHERE NOT coalesce((e->>'isDeleted')::boolean,false)) FROM boards WHERE lesson_id=$1`, l.ID).Scan(&boardRev, &boardCount)
	var chatCount int
	s.db.QueryRow(c, `SELECT count(*) FROM chat_messages WHERE lesson_id=$1`, l.ID).Scan(&chatCount)

	c.JSON(200, gin.H{
		"lesson":       l,
		"myRole":       role,
		"canJoin":      canJoin,
		"joinOpensAt":  opens,
		"joinClosesAt": closes,
		"materials":    materials,
		"homework":     homework,
		"recordings":   recordings,
		"review":       review,
		"board":        gin.H{"revision": boardRev, "elementCount": boardCount},
		"chatCount":    chatCount,
	})
}

func (s *Server) cancelLesson(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if l.Status != "scheduled" {
		fail(c, 409, "Отменить можно только запланированное занятие")
		return
	}
	// Отменяется только будущее занятие: начавшееся (или прошедшее) время освобождать незачем —
	// записаться на него всё равно нельзя, а неначавшиеся уроки закрывает фоновая задача.
	now := time.Now()
	if !l.AlwaysOpen && !l.StartsAt.After(now) {
		fail(c, 409, "Занятие уже началось — отменить можно только до начала")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	// После смены статуса интервал сразу свободен: запросы занятости и EXCLUDE-ограничения
	// не учитывают status='cancelled'. Занятие не удаляется — остаётся в истории.
	tag, err := s.db.Exec(c, `UPDATE lessons SET status='cancelled', cancelled_by=$1, cancel_reason=$2
		WHERE id=$3 AND status='scheduled' AND (always_open OR starts_at > $4)`,
		currentUser(c).ID, truncate(strings.TrimSpace(req.Reason), 300), l.ID, now)
	if err != nil {
		internal(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 409, "Статус занятия изменился, обновите страницу")
		return
	}
	s.hub.CloseLesson(l.ID)
	s.hub.Broadcast(l.ID, map[string]any{"type": "lesson.status", "status": "cancelled"})
	c.JSON(200, gin.H{"ok": true})
}

// joinLesson выдаёт токен LiveKit только участнику занятия и только во временном окне.
func (s *Server) joinLesson(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if msg := s.classProblem(l, time.Now()); msg != "" {
		fail(c, 409, msg)
		return
	}
	_, closes := s.joinWindow(l)
	if s.lk == nil {
		fail(c, 503, "Видеосвязь не настроена: задайте LIVEKIT_URL, LIVEKIT_API_KEY и LIVEKIT_API_SECRET")
		return
	}
	u := currentUser(c)
	ttl := time.Until(closes) + 10*time.Minute
	if ttl > 4*time.Hour {
		ttl = 4 * time.Hour
	}
	token, err := s.lk.JoinToken(lk.RoomName(l.ID), u.ID, u.Name, ttl)
	if err != nil {
		internal(c, err)
		return
	}
	if l.Status == "scheduled" {
		if _, err := s.db.Exec(c, `UPDATE lessons SET status='live', started_at=coalesce(started_at, now()) WHERE id=$1 AND status='scheduled'`, l.ID); err != nil {
			internal(c, err)
			return
		}
		s.hub.Broadcast(l.ID, map[string]any{"type": "lesson.status", "status": "live"})
	}
	c.JSON(200, gin.H{
		"token":    token,
		"url":      s.cfg.LiveKitPublicURL, // пусто → клиент использует wss://<текущий хост>
		"room":     lk.RoomName(l.ID),
		"identity": u.ID,
		"role":     role,
	})
}

func (s *Server) endLesson(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Завершить занятие может только преподаватель")
		return
	}
	if l.Status == "completed" {
		c.JSON(200, gin.H{"ok": true})
		return
	}
	if l.Status == "cancelled" {
		fail(c, 409, "Занятие отменено")
		return
	}
	s.stopActiveRecordings(c, l.ID)
	if _, err := s.db.Exec(c, `UPDATE lessons SET status='completed', ended_at=now(), started_at=coalesce(started_at, now()) WHERE id=$1`, l.ID); err != nil {
		internal(c, err)
		return
	}
	s.hub.CloseLesson(l.ID)
	s.hub.Flush(l.ID)
	s.hub.Broadcast(l.ID, map[string]any{"type": "lesson.status", "status": "completed"})
	if s.lk != nil {
		room := lk.RoomName(l.ID)
		go func() {
			time.Sleep(4 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.lk.DeleteRoom(ctx, room); err != nil {
				slog.Warn("delete room", "room", room, "err", err)
			}
		}()
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) createReview(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "student" {
		fail(c, 403, "Отзыв оставляет ученик")
		return
	}
	if l.Status != "completed" {
		fail(c, 409, "Оценить можно только завершённое занятие")
		return
	}
	var req struct {
		Rating int    `json:"rating"`
		Body   string `json:"body"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Rating < 1 || req.Rating > 5 {
		fail(c, 400, "Поставьте оценку от 1 до 5")
		return
	}
	if utf8.RuneCountInString(req.Body) > 2000 {
		fail(c, 400, "Отзыв слишком длинный")
		return
	}
	_, err := s.db.Exec(c, `INSERT INTO reviews(lesson_id, student_id, teacher_id, rating, body) VALUES ($1,$2,$3,$4,$5)`,
		l.ID, l.StudentID, l.TeacherID, req.Rating, strings.TrimSpace(req.Body))
	if err != nil {
		if code, _ := db.ConstraintName(err); code == "23505" {
			fail(c, 409, "Вы уже оставили отзыв об этом занятии")
			return
		}
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"ok": true})
}

func itoa(n int) string { return strconv.Itoa(n) }
