package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/realtime"
)

func (s *Server) chatHistory(ctx context.Context, lessonID string, limit int) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `SELECT * FROM (
		SELECT m.id, m.user_id, u.name, m.body, m.created_at FROM chat_messages m JOIN users u ON u.id = m.user_id
		WHERE m.lesson_id=$1 ORDER BY m.id DESC LIMIT $2) t ORDER BY id`, lessonID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var uid, name, body string
		var at time.Time
		if err := rows.Scan(&id, &uid, &name, &body, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "userId": uid, "userName": name, "body": body, "createdAt": at})
	}
	return out, rows.Err()
}

func (s *Server) storeChat(ctx context.Context, lessonID, userID, body string) (map[string]any, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("Пустое сообщение")
	}
	if utf8.RuneCountInString(body) > 2000 {
		return nil, errors.New("Сообщение слишком длинное (максимум 2000 символов)")
	}
	var id int64
	var at time.Time
	var name string
	err := s.db.QueryRow(ctx, `WITH ins AS (INSERT INTO chat_messages(lesson_id, user_id, body) VALUES ($1,$2,$3) RETURNING id, created_at, user_id)
		SELECT ins.id, ins.created_at, u.name FROM ins JOIN users u ON u.id = ins.user_id`, lessonID, userID, body).Scan(&id, &at, &name)
	if err != nil {
		return nil, errors.New("Не удалось сохранить сообщение")
	}
	return map[string]any{"id": id, "userId": userID, "userName": name, "body": body, "createdAt": at}, nil
}

func (s *Server) lessonWS(c *gin.Context) {
	l, role, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if l.Status == "cancelled" {
		fail(c, 409, "Занятие отменено")
		return
	}
	if l.Status == "completed" {
		fail(c, 409, "Занятие завершено")
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
	u := currentUser(c)
	chat, _ := s.chatHistory(c, l.ID, realtime.ChatLimit())
	hello := map[string]any{
		"me":           map[string]string{"id": u.ID, "name": u.Name, "role": role},
		"chat":         chat,
		"lessonStatus": l.Status,
		"recording":    s.activeRecording(c, l.ID),
	}
	// Контекст запроса gin завершится вместе с соединением.
	s.hub.Serve(c.Request.Context(), conn, l.ID, &realtime.Client{UserID: u.ID, Name: u.Name, Role: role}, hello, s.storeChat)
}

func (s *Server) getBoard(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	els, rev := s.hub.BoardSnapshot(c, l.ID)
	c.JSON(200, gin.H{"elements": json.RawMessage(els), "revision": rev})
}

func (s *Server) getChat(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	msgs, err := s.chatHistory(c, l.ID, 1000)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"messages": msgs})
}
