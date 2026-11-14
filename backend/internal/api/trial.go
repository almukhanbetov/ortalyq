package api

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/db"
)

// Бесплатный ознакомительный урок — обычная запись lessons (тот же класс, права, материалы и завершение),
// которую преподаватель начинает в любой момент без записи в расписании:
//   - is_trial=true, always_open=true, price=0, status='live' с момента создания (started_at — фактическое начало);
//   - вход определяется состоянием урока (идёт / завершён), а не временем: см. classProblem и always_open;
//   - ученика ещё нет (student_id пуст) — он присоединяется по ссылке-приглашению под своим аккаунтом;
//     после этого доступ — обычный lessonAccess (только преподаватель и этот ученик);
//   - интервалы расписания и платных бронирований не занимает (NOT is_trial в запросах занятости и EXCLUDE);
//   - завершает преподаватель обычной кнопкой (endLesson): ended_at и ends_at = фактическое окончание.

const trialSubject = "Ознакомительный урок"

// trialCode — код приглашения: HMAC от id урока на ключе APP_SECRET. Сам по себе доступа не даёт —
// только право занять единственное место ученика, пока оно свободно и урок идёт.
func (s *Server) trialCode(lessonID string) string {
	return s.signer.MAC("trial-invite", lessonID)[:32]
}

func (s *Server) trialCodeOK(lessonID, code string) bool {
	return isUUID(lessonID) && len(code) == 32 && subtle.ConstantTimeCompare([]byte(code), []byte(s.trialCode(lessonID))) == 1
}

// trialInvite — ссылка-приглашение для преподавателя, пока урок идёт и ученик не присоединился.
func (s *Server) trialInvite(l *lessonRow, role string) gin.H {
	if !l.IsTrial || role != "teacher" || l.StudentID != "" || (l.Status != "scheduled" && l.Status != "live") {
		return nil
	}
	return gin.H{"path": "/trial/" + l.ID + "#" + s.trialCode(l.ID)}
}

// activeLessonConflict — урок преподавателя, который идёт прямо сейчас: ознакомительный урок, ещё не
// завершённый, или обычный урок, время которого сейчас (запланированный или начатый). Постоянно открытые
// демо-классы (seed) не считаются.
func (s *Server) activeLessonConflict(c *gin.Context, teacherID string, now time.Time) (string, string, bool, error) {
	var id, subject string
	var trial bool
	err := s.db.QueryRow(c, `SELECT id, subject, is_trial FROM lessons WHERE teacher_id=$1 AND status IN ('scheduled','live') AND (
			is_trial OR (NOT always_open AND starts_at <= $2 AND ends_at > $2))
		ORDER BY is_trial DESC, starts_at LIMIT 1`, teacherID, now).Scan(&id, &subject, &trial)
	if db.IsNoRows(err) {
		return "", "", false, nil
	}
	return id, subject, trial, err
}

func conflictResponse(c *gin.Context, id, subject string, trial bool) {
	msg := fmt.Sprintf("У вас уже идёт урок «%s». Откройте его — новый урок не создан.", subject)
	if trial {
		msg = "У вас уже идёт бесплатный ознакомительный урок. Откройте его или завершите — второй урок не создан."
	}
	c.AbortWithStatusJSON(409, gin.H{"error": msg, "lessonId": id, "isTrial": trial})
}

// POST /api/lessons/trial — начать бесплатный ознакомительный урок (только преподаватель).
// Повторное нажатие (и одновременные запросы) не создают второй урок: возвращается 409 с id идущего урока.
func (s *Server) startTrialLesson(c *gin.Context) {
	u := currentUser(c)
	now := time.Now()
	if id, subject, trial, err := s.activeLessonConflict(c, u.ID, now); err != nil {
		internal(c, err)
		return
	} else if id != "" {
		conflictResponse(c, id, subject, trial)
		return
	}
	start := now.Truncate(time.Second)
	var id string
	// ends_at — заглушка до завершения (интервалы расписания урок не занимает); при завершении — фактическое окончание.
	err := s.db.QueryRow(c, `INSERT INTO lessons(teacher_id, subject, starts_at, ends_at, status, started_at, price, always_open, is_trial)
		VALUES ($1, $2, $3, $4, 'live', $3, 0, true, true) RETURNING id`, u.ID, trialSubject, start, start.Add(time.Second)).Scan(&id)
	if err != nil {
		if code, _ := db.ConstraintName(err); code == "23505" { // lessons_one_active_trial: параллельное нажатие
			if cid, subject, trial, err2 := s.activeLessonConflict(c, u.ID, now); err2 == nil && cid != "" {
				conflictResponse(c, cid, subject, trial)
				return
			}
		}
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"id": id})
}

// trialFromInvite проверяет код приглашения и загружает урок. Неверный код и посторонний урок — 404.
func (s *Server) trialFromInvite(c *gin.Context, code string) (*lessonRow, bool) {
	id := c.Param("id")
	if !trialAcceptLimiter.allow("ip:"+c.ClientIP(), 30, 10*time.Minute) {
		fail(c, 429, "Слишком много попыток — подождите несколько минут")
		return nil, false
	}
	if !s.trialCodeOK(id, strings.TrimSpace(code)) {
		fail(c, 404, "Приглашение не найдено. Проверьте, что ссылка скопирована целиком.")
		return nil, false
	}
	l, err := scanLesson(s.db.QueryRow(c, lessonSelect+" WHERE l.id = $1 AND l.is_trial", id))
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Приглашение не найдено. Проверьте, что ссылка скопирована целиком.")
		} else {
			internal(c, err)
		}
		return nil, false
	}
	return &l, true
}

var trialAcceptLimiter = &limiter{hits: map[string][]time.Time{}}

// trialInviteState — что видит открывший приглашение: урок, преподаватель и можно ли присоединиться.
func trialInviteState(l *lessonRow, me *ctxUser) gin.H {
	state := "open"
	switch {
	case l.Status == "completed" || l.Status == "cancelled":
		state = "ended"
	case me != nil && me.ID == l.TeacherID:
		state = "owner"
	case me != nil && me.ID == l.StudentID:
		state = "joined"
	case l.StudentID != "":
		state = "taken"
	}
	return gin.H{"lessonId": l.ID, "teacherName": l.TeacherName, "subject": l.Subject, "status": l.Status, "startedAt": l.StartedAt, "state": state}
}

// POST /api/trial/:id/invite — сведения о приглашении (по коду; вход не обязателен).
func (s *Server) trialInviteInfo(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	c.ShouldBindJSON(&req)
	l, ok := s.trialFromInvite(c, req.Code)
	if !ok {
		return
	}
	c.JSON(200, trialInviteState(l, currentUser(c)))
}

// POST /api/trial/:id/accept — ученик занимает место в ознакомительном уроке. Повторный приём тем же
// учеником ничего не меняет; второй ученик и посторонние получают отказ. Место занимается атомарно.
func (s *Server) acceptTrialInvite(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	c.ShouldBindJSON(&req)
	l, ok := s.trialFromInvite(c, req.Code)
	if !ok {
		return
	}
	u := currentUser(c)
	st := trialInviteState(l, u)["state"]
	switch st {
	case "joined":
		c.JSON(200, gin.H{"lessonId": l.ID})
		return
	case "ended":
		fail(c, 410, "Урок уже завершён — вход закрыт")
		return
	case "owner":
		fail(c, 409, "Это ваш урок — откройте его из кабинета")
		return
	case "taken":
		fail(c, 409, "К уроку уже присоединился другой ученик")
		return
	}
	if u.Role != "student" {
		fail(c, 403, "Присоединиться к уроку может только ученик — войдите под аккаунтом ученика")
		return
	}
	if !trialAcceptLimiter.allow("lesson:"+l.ID, 20, time.Hour) {
		fail(c, 429, "Слишком много попыток — подождите")
		return
	}
	tag, err := s.db.Exec(c, `UPDATE lessons SET student_id=$1
		WHERE id=$2 AND is_trial AND student_id IS NULL AND status IN ('scheduled','live')`, u.ID, l.ID)
	if err != nil {
		internal(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		// Кто-то успел раньше, или урок завершили между проверкой и записью.
		var status string
		var student *string
		if err := s.db.QueryRow(c, `SELECT status, student_id::text FROM lessons WHERE id=$1`, l.ID).Scan(&status, &student); err != nil {
			internal(c, err)
			return
		}
		switch {
		case student != nil && *student == u.ID:
			c.JSON(200, gin.H{"lessonId": l.ID})
		case status == "completed" || status == "cancelled":
			fail(c, 410, "Урок уже завершён — вход закрыт")
		default:
			fail(c, 409, "К уроку уже присоединился другой ученик")
		}
		return
	}
	// Преподаватель в классе обновит сведения об уроке (имя ученика, приглашение больше не нужно).
	s.hub.Broadcast(l.ID, map[string]any{"type": "lesson.participants"})
	c.JSON(200, gin.H{"lessonId": l.ID})
}
