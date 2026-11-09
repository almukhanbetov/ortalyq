package api

import (
	"context"
	"log/slog"
	"time"
)

// RunJobs — фоновые задачи: авто-завершение занятий, истечение запросов на запись,
// опрос статусов Egress и очистка сессий.
func (s *Server) RunJobs(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) tick(ctx context.Context) {
	late := time.Duration(s.cfg.JoinLateMinutes) * time.Minute

	rows, err := s.db.Query(ctx, `UPDATE lessons SET status='completed', ended_at=coalesce(ended_at, now())
		WHERE status='live' AND NOT always_open AND ends_at < $1 RETURNING id`, time.Now().Add(-late))
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
			s.stopActiveRecordings(ctx, id)
			s.hub.Flush(id)
			s.hub.Broadcast(id, map[string]any{"type": "lesson.status", "status": "completed"})
			slog.Info("lesson auto-completed", "lesson", id)
		}
	}

	s.db.Exec(ctx, `UPDATE lessons SET status='cancelled', cancel_reason='Занятие не состоялось'
		WHERE status='scheduled' AND NOT always_open AND ends_at < $1`, time.Now().Add(-late))

	rows, err = s.db.Query(ctx, `UPDATE recordings SET status='declined', error='Время ожидания согласия истекло', updated_at=now()
		WHERE status='awaiting_consent' AND created_at < now() - interval '3 minutes' RETURNING id, lesson_id`)
	if err == nil {
		type pair struct{ id, lesson string }
		var list []pair
		for rows.Next() {
			var p pair
			if rows.Scan(&p.id, &p.lesson) == nil {
				list = append(list, p)
			}
		}
		rows.Close()
		for _, p := range list {
			s.broadcastRecording(ctx, p.lesson, p.id)
		}
	}

	s.refreshActiveRecordings(ctx, "")
	s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
}
