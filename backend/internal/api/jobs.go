package api

import (
	"context"
	"log/slog"
	"time"

	"ortalyq/backend/internal/lk"
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
	// Урок заканчивается в ends_at независимо от опозданий: автоматического продления нет.
	// Идущее занятие завершается, класс закрывается (комната LiveKit удаляется).
	now := time.Now()
	rows, err := s.db.Query(ctx, `UPDATE lessons SET status='completed', ended_at=coalesce(ended_at, now())
		WHERE status='live' AND NOT always_open AND ends_at <= $1 RETURNING id`, now)
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
			s.hub.CloseLesson(id)
			s.hub.Flush(id)
			s.hub.Broadcast(id, map[string]any{"type": "lesson.status", "status": "completed"})
			if s.lk != nil {
				rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if err := s.lk.DeleteRoom(rctx, lk.RoomName(id)); err != nil {
					slog.Warn("delete room", "lesson", id, "err", err)
				}
				cancel()
			}
			slog.Info("lesson auto-completed", "lesson", id)
		}
	}

	// Никто не вошёл в класс до окончания — занятие не состоялось (время в прошлом, освобождать нечего).
	s.db.Exec(ctx, `UPDATE lessons SET status='cancelled', cancel_reason='Занятие не состоялось'
		WHERE status='scheduled' AND NOT always_open AND ends_at <= $1`, now)

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
