package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/livekit/protocol/livekit"

	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/lk"
)

type recording struct {
	ID               string     `json:"id"`
	Status           string     `json:"status"`
	TeacherConsentAt *time.Time `json:"teacherConsentAt"`
	StudentConsentAt *time.Time `json:"studentConsentAt"`
	SizeBytes        *int64     `json:"sizeBytes"`
	DurationMs       *int64     `json:"durationMs"`
	Error            string     `json:"error"`
	StartedAt        *time.Time `json:"startedAt"`
	EndedAt          *time.Time `json:"endedAt"`
	CreatedAt        time.Time  `json:"createdAt"`
	egressID         *string
	lessonID         string
}

const recordingSelect = `SELECT id, status, teacher_consent_at, student_consent_at, size_bytes, duration_ms, error, started_at, ended_at, created_at, egress_id, lesson_id FROM recordings`

func scanRecording(row interface{ Scan(...any) error }) (recording, error) {
	var r recording
	err := row.Scan(&r.ID, &r.Status, &r.TeacherConsentAt, &r.StudentConsentAt, &r.SizeBytes, &r.DurationMs, &r.Error, &r.StartedAt, &r.EndedAt, &r.CreatedAt, &r.egressID, &r.lessonID)
	return r, err
}

func (s *Server) lessonRecordings(ctx context.Context, lessonID string) ([]recording, error) {
	rows, err := s.db.Query(ctx, recordingSelect+` WHERE lesson_id=$1 AND status <> 'declined' ORDER BY created_at`, lessonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []recording{}
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ActiveRecording — текущая запись занятия (для состояния видеокласса).
func (s *Server) activeRecording(ctx context.Context, lessonID string) *recording {
	r, err := scanRecording(s.db.QueryRow(ctx, recordingSelect+` WHERE lesson_id=$1 AND status IN ('awaiting_consent','starting','recording','processing') ORDER BY created_at DESC LIMIT 1`, lessonID))
	if err != nil {
		return nil
	}
	return &r
}

func (s *Server) broadcastRecording(ctx context.Context, lessonID, recID string) {
	r, err := scanRecording(s.db.QueryRow(ctx, recordingSelect+` WHERE id=$1`, recID))
	if err != nil {
		return
	}
	s.hub.Broadcast(lessonID, map[string]any{"type": "recording", "recording": r})
}

func (s *Server) requestRecording(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Запись запускает преподаватель")
		return
	}
	if p := s.cfg.RecordingProblem(); p != "" {
		fail(c, 503, "Запись недоступна. "+p)
		return
	}
	if l.Status != "live" {
		fail(c, 409, "Запись можно начать только во время занятия")
		return
	}
	var id string
	err := s.db.QueryRow(c, `INSERT INTO recordings(lesson_id, requested_by, status, teacher_consent_at) VALUES ($1,$2,'awaiting_consent', now()) RETURNING id`,
		l.ID, currentUser(c).ID).Scan(&id)
	if err != nil {
		if code, _ := db.ConstraintName(err); code == "23505" {
			fail(c, 409, "Запись уже запрошена или идёт")
			return
		}
		internal(c, err)
		return
	}
	s.broadcastRecording(c, l.ID, id)
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) consentRecording(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	var req struct {
		Accept bool `json:"accept"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	rid := c.Param("rid")
	if !isUUID(rid) {
		fail(c, 404, "Запись не найдена")
		return
	}
	col := "student_consent_at"
	if role == "teacher" {
		col = "teacher_consent_at"
	}
	var status string
	if req.Accept {
		err := s.db.QueryRow(c, `UPDATE recordings SET `+col+`=coalesce(`+col+`, now()), updated_at=now()
			WHERE id=$1 AND lesson_id=$2 AND status='awaiting_consent' RETURNING status`, rid, l.ID).Scan(&status)
		if err != nil {
			fail(c, 409, "Запрос на запись больше не актуален")
			return
		}
	} else {
		tag, err := s.db.Exec(c, `UPDATE recordings SET status='declined', error='Участник отказался от записи', updated_at=now()
			WHERE id=$1 AND lesson_id=$2 AND status='awaiting_consent'`, rid, l.ID)
		if err != nil {
			internal(c, err)
			return
		}
		if tag.RowsAffected() == 0 {
			fail(c, 409, "Запрос на запись больше не актуален")
			return
		}
		s.broadcastRecording(c, l.ID, rid)
		c.JSON(200, gin.H{"status": "declined"})
		return
	}
	// Оба согласились? Атомарно переводим в 'starting', чтобы Egress запустился ровно один раз.
	var started bool
	err := s.db.QueryRow(c, `UPDATE recordings SET status='starting', updated_at=now()
		WHERE id=$1 AND status='awaiting_consent' AND teacher_consent_at IS NOT NULL AND student_consent_at IS NOT NULL RETURNING true`, rid).Scan(&started)
	if err != nil && !db.IsNoRows(err) {
		internal(c, err)
		return
	}
	if started {
		if err := s.startEgress(c, l.ID, rid); err != nil {
			slog.Error("start egress", "err", err)
			s.db.Exec(c, `UPDATE recordings SET status='failed', error=$1, updated_at=now() WHERE id=$2`, "Не удалось запустить запись: "+truncate(err.Error(), 300), rid)
			s.broadcastRecording(c, l.ID, rid)
			fail(c, 502, "Не удалось запустить запись. Проверьте, что сервис LiveKit Egress запущен.")
			return
		}
	}
	s.broadcastRecording(c, l.ID, rid)
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) recorderTemplateURL(lessonID string) string {
	tok := s.signer.Sign("rec", lessonID, 8*time.Hour)
	return fmt.Sprintf("%s/recorder/%s/%s", s.cfg.RecordingTemplateBase, lessonID, tok)
}

func (s *Server) startEgress(ctx context.Context, lessonID, recID string) error {
	key := fmt.Sprintf("lessons/%s/recordings/%s.mp4", lessonID, recID)
	info, err := s.lk.StartRecording(ctx, lk.RoomName(lessonID), s.recorderTemplateURL(lessonID), key, lk.S3Target{
		AccessKey: s.cfg.S3AccessKey,
		Secret:    s.cfg.S3SecretKey,
		Endpoint:  s.cfg.EgressS3Endpoint,
		Bucket:    s.cfg.S3Bucket,
		Region:    s.cfg.EgressS3Region,
	})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE recordings SET egress_id=$1, object_key=$2, updated_at=now() WHERE id=$3`, info.EgressId, key, recID)
	if err == nil {
		s.applyEgressInfo(ctx, info)
	}
	return err
}

func (s *Server) stopRecording(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if role != "teacher" {
		fail(c, 403, "Остановить запись может преподаватель")
		return
	}
	r, err := scanRecording(s.db.QueryRow(c, recordingSelect+` WHERE id=$1 AND lesson_id=$2`, c.Param("rid"), l.ID))
	if err != nil || !isUUID(c.Param("rid")) {
		fail(c, 404, "Запись не найдена")
		return
	}
	s.stopOne(c, &r)
	s.broadcastRecording(c, l.ID, r.ID)
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) stopOne(ctx context.Context, r *recording) {
	switch r.Status {
	case "awaiting_consent":
		s.db.Exec(ctx, `UPDATE recordings SET status='declined', error='Запрос отменён', updated_at=now() WHERE id=$1`, r.ID)
	case "starting", "recording":
		if r.egressID == nil || s.lk == nil {
			s.db.Exec(ctx, `UPDATE recordings SET status='failed', error='Запись не была запущена', updated_at=now() WHERE id=$1`, r.ID)
			return
		}
		s.db.Exec(ctx, `UPDATE recordings SET status='processing', ended_at=now(), updated_at=now() WHERE id=$1`, r.ID)
		info, err := s.lk.StopRecording(ctx, *r.egressID)
		if err != nil {
			slog.Warn("stop egress", "egress", *r.egressID, "err", err)
			// Egress мог уже завершиться сам — подтянем актуальный статус
			if info, err2 := s.lk.GetEgress(ctx, *r.egressID); err2 == nil && info != nil {
				s.applyEgressInfo(ctx, info)
			}
			return
		}
		s.applyEgressInfo(ctx, info)
	}
}

func (s *Server) stopActiveRecordings(ctx context.Context, lessonID string) {
	rows, err := s.db.Query(ctx, recordingSelect+` WHERE lesson_id=$1 AND status IN ('awaiting_consent','starting','recording')`, lessonID)
	if err != nil {
		return
	}
	var list []recording
	for rows.Next() {
		if r, err := scanRecording(rows); err == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	for i := range list {
		s.stopOne(ctx, &list[i])
		s.broadcastRecording(ctx, lessonID, list[i].ID)
	}
}

// applyEgressInfo переводит статус записи в соответствии с состоянием Egress.
func (s *Server) applyEgressInfo(ctx context.Context, info *livekit.EgressInfo) {
	if info == nil {
		return
	}
	var recID, lessonID, cur string
	var key *string
	if err := s.db.QueryRow(ctx, `SELECT id, lesson_id, status, object_key FROM recordings WHERE egress_id=$1`, info.EgressId).Scan(&recID, &lessonID, &cur, &key); err != nil {
		return
	}
	if cur == "ready" || cur == "failed" || cur == "declined" {
		return
	}
	next := cur
	var errMsg string
	var size, dur *int64
	switch info.Status {
	case livekit.EgressStatus_EGRESS_STARTING:
		next = "starting"
	case livekit.EgressStatus_EGRESS_ACTIVE:
		next = "recording"
	case livekit.EgressStatus_EGRESS_ENDING:
		next = "processing"
	case livekit.EgressStatus_EGRESS_COMPLETE:
		next = "ready"
		if len(info.FileResults) > 0 {
			f := info.FileResults[0]
			sz, d := f.Size, f.Duration/int64(time.Millisecond)
			size, dur = &sz, &d
		}
		// Убеждаемся, что файл действительно лежит в хранилище
		if s.store != nil && key != nil {
			if st, err := s.store.Stat(ctx, *key); err != nil || st.Size == 0 {
				next, errMsg = "failed", "Файл записи не найден в хранилище"
			} else if size == nil || *size == 0 {
				sz := st.Size
				size = &sz
			}
		}
	case livekit.EgressStatus_EGRESS_FAILED, livekit.EgressStatus_EGRESS_ABORTED, livekit.EgressStatus_EGRESS_LIMIT_REACHED:
		next = "failed"
		errMsg = info.Error
		if errMsg == "" {
			errMsg = info.Status.String()
		}
	}
	if next == cur && size == nil {
		return
	}
	switch next {
	case "recording":
		s.db.Exec(ctx, `UPDATE recordings SET status=$1, started_at=coalesce(started_at, now()), updated_at=now() WHERE id=$2`, next, recID)
	default:
		s.db.Exec(ctx, `UPDATE recordings SET status=$1, size_bytes=coalesce($2, size_bytes), duration_ms=coalesce($3, duration_ms),
			error=CASE WHEN $4 = '' THEN error ELSE $4 END,
			ended_at=CASE WHEN $1 IN ('processing','ready','failed') THEN coalesce(ended_at, now()) ELSE ended_at END,
			updated_at=now() WHERE id=$5`, next, size, dur, truncate(errMsg, 500), recID)
	}
	s.broadcastRecording(ctx, lessonID, recID)
}

// refreshActiveRecordings опрашивает Egress для незавершённых записей (страховка, если вебхук не дошёл).
func (s *Server) refreshActiveRecordings(ctx context.Context, lessonID string) {
	if s.lk == nil {
		return
	}
	q := `SELECT egress_id FROM recordings WHERE status IN ('starting','recording','processing') AND egress_id IS NOT NULL`
	args := []any{}
	if lessonID != "" {
		q += ` AND lesson_id=$1`
		args = append(args, lessonID)
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		info, err := s.lk.GetEgress(cctx, id)
		cancel()
		if err == nil && info != nil {
			s.applyEgressInfo(ctx, info)
		}
	}
}

func (s *Server) recordingLink(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	var key *string
	var status string
	if !isUUID(c.Param("rid")) || s.db.QueryRow(c, `SELECT object_key, status FROM recordings WHERE id=$1 AND lesson_id=$2`, c.Param("rid"), l.ID).Scan(&key, &status) != nil {
		fail(c, 404, "Запись не найдена")
		return
	}
	if status != "ready" || key == nil {
		fail(c, 409, "Запись ещё не готова")
		return
	}
	name := fmt.Sprintf("Занятие %s %s.mp4", l.Subject, l.StartsAt.Format("2006-01-02"))
	c.JSON(200, gin.H{"url": s.signedLink(*key, name, "video/mp4"), "expiresIn": int(linkTTL.Seconds())})
}

func (s *Server) livekitWebhook(c *gin.Context) {
	if s.lk == nil {
		fail(c, 404, "LiveKit не настроен")
		return
	}
	ev, err := s.lk.ReceiveWebhook(c.Request)
	if err != nil {
		fail(c, 401, "Неверная подпись")
		return
	}
	if ev.EgressInfo != nil {
		s.applyEgressInfo(c, ev.EgressInfo)
	}
	c.JSON(200, gin.H{"ok": true})
}

// recorderState — состояние для шаблона записи (Egress), доступ по подписанному токену.
func (s *Server) recorderState(c *gin.Context) {
	lessonID, err := s.signer.Verify("rec", c.Param("token"))
	if err != nil {
		fail(c, 403, "Недействительный токен шаблона записи")
		return
	}
	elements, rev := s.hub.BoardSnapshot(c, lessonID)
	var subject, teacher, student string
	s.db.QueryRow(c, `SELECT l.subject, t.name, coalesce(st.name, '') FROM lessons l JOIN users t ON t.id=l.teacher_id LEFT JOIN users st ON st.id=l.student_id WHERE l.id=$1`, lessonID).
		Scan(&subject, &teacher, &student)
	c.JSON(200, gin.H{"elements": json.RawMessage(elements), "revision": rev, "subject": subject, "teacherName": teacher, "studentName": student})
}
