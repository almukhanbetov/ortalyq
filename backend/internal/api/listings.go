package api

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/db"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

type listing struct {
	ID           string    `json:"id"`
	AuthorID     string    `json:"authorId"`
	AuthorName   string    `json:"authorName"`
	AuthorRole   string    `json:"authorRole"`
	Kind         string    `json:"kind"`
	Subject      string    `json:"subject"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	Price        *int      `json:"price"`
	Hidden       bool      `json:"hidden"`
	HiddenReason string    `json:"hiddenReason,omitempty"`
	IsDemo       bool      `json:"isDemo"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

const listingSelect = `SELECT l.id, l.author_id, u.name, u.role, l.kind, l.subject, l.title, l.body, l.price, l.hidden, l.hidden_reason, l.is_demo, l.created_at, l.updated_at
	FROM listings l JOIN users u ON u.id = l.author_id`

func (s *Server) queryListings(c *gin.Context, where string, args ...any) ([]listing, error) {
	rows, err := s.db.Query(c, listingSelect+" WHERE "+where+" ORDER BY l.created_at DESC LIMIT 200", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []listing{}
	for rows.Next() {
		var l listing
		if err := rows.Scan(&l.ID, &l.AuthorID, &l.AuthorName, &l.AuthorRole, &l.Kind, &l.Subject, &l.Title, &l.Body, &l.Price, &l.Hidden, &l.HiddenReason, &l.IsDemo, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func listingFilters(c *gin.Context, where string, args []any) (string, []any) {
	if k := c.Query("kind"); k == "offer" || k == "request" {
		args = append(args, k)
		where += fmt.Sprintf(" AND l.kind = $%d", len(args))
	}
	if sub := strings.TrimSpace(c.Query("subject")); sub != "" {
		args = append(args, sub)
		where += fmt.Sprintf(" AND l.subject = $%d", len(args))
	}
	return where, args
}

func (s *Server) listListings(c *gin.Context) {
	where, args := listingFilters(c, "NOT l.hidden AND NOT u.blocked", nil)
	out, err := s.queryListings(c, where, args...)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"listings": out})
}

func (s *Server) myListings(c *gin.Context) {
	out, err := s.queryListings(c, "l.author_id = $1", currentUser(c).ID)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"listings": out})
}

type listingReq struct {
	Subject string `json:"subject"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Price   *int   `json:"price"`
}

func (r *listingReq) validate() string {
	r.Subject, r.Title, r.Body = strings.TrimSpace(r.Subject), strings.TrimSpace(r.Title), strings.TrimSpace(r.Body)
	switch {
	case r.Subject == "" || utf8.RuneCountInString(r.Subject) > 40:
		return "Укажите предмет"
	case utf8.RuneCountInString(r.Title) < 3 || utf8.RuneCountInString(r.Title) > 120:
		return "Заголовок должен содержать от 3 до 120 символов"
	case utf8.RuneCountInString(r.Body) < 10 || utf8.RuneCountInString(r.Body) > 3000:
		return "Описание должно содержать от 10 до 3000 символов"
	case r.Price != nil && (*r.Price < 0 || *r.Price > 1_000_000):
		return "Некорректная стоимость"
	}
	return ""
}

func (s *Server) createListing(c *gin.Context) {
	u := currentUser(c)
	var req listingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if msg := req.validate(); msg != "" {
		fail(c, 400, msg)
		return
	}
	// Тип объявления определяется ролью: преподаватель предлагает занятия, ученик ищет преподавателя.
	kind := "request"
	if u.Role == "teacher" {
		kind = "offer"
	}
	var id string
	err := s.db.QueryRow(c, `INSERT INTO listings(author_id, kind, subject, title, body, price) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		u.ID, kind, req.Subject, req.Title, req.Body, req.Price).Scan(&id)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) ownListing(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !isUUID(id) {
		fail(c, 404, "Объявление не найдено")
		return "", false
	}
	var author string
	if err := s.db.QueryRow(c, `SELECT author_id FROM listings WHERE id=$1`, id).Scan(&author); err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Объявление не найдено")
		} else {
			internal(c, err)
		}
		return "", false
	}
	if author != currentUser(c).ID {
		fail(c, 403, "Можно изменять только свои объявления")
		return "", false
	}
	return id, true
}

func (s *Server) updateListing(c *gin.Context) {
	id, ok := s.ownListing(c)
	if !ok {
		return
	}
	var req listingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if msg := req.validate(); msg != "" {
		fail(c, 400, msg)
		return
	}
	if _, err := s.db.Exec(c, `UPDATE listings SET subject=$1, title=$2, body=$3, price=$4, updated_at=now() WHERE id=$5`,
		req.Subject, req.Title, req.Body, req.Price, id); err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) deleteListing(c *gin.Context) {
	id, ok := s.ownListing(c)
	if !ok {
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM listings WHERE id=$1`, id); err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---- Администрирование ----

func (s *Server) adminListings(c *gin.Context) {
	where, args := listingFilters(c, "true", nil)
	if c.Query("hidden") == "true" {
		where += " AND l.hidden"
	}
	out, err := s.queryListings(c, where, args...)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"listings": out})
}

func (s *Server) adminHideListing(c *gin.Context) {
	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	if !isUUID(c.Param("id")) {
		fail(c, 404, "Объявление не найдено")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "Скрыто модератором"
	}
	tag, err := s.db.Exec(c, `UPDATE listings SET hidden=true, hidden_reason=$1 WHERE id=$2`, truncate(reason, 300), c.Param("id"))
	if err != nil {
		internal(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 404, "Объявление не найдено")
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) adminUnhideListing(c *gin.Context) {
	if !isUUID(c.Param("id")) {
		fail(c, 404, "Объявление не найдено")
		return
	}
	if _, err := s.db.Exec(c, `UPDATE listings SET hidden=false, hidden_reason='' WHERE id=$1`, c.Param("id")); err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) adminUsers(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT id, email, name, role, is_demo, created_at FROM users ORDER BY created_at DESC LIMIT 300`)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, email, name, role string
		var demo bool
		var at time.Time
		if err := rows.Scan(&id, &email, &name, &role, &demo, &at); err != nil {
			internal(c, err)
			return
		}
		out = append(out, gin.H{"id": id, "email": email, "name": name, "role": role, "isDemo": demo, "createdAt": at})
	}
	c.JSON(200, gin.H{"users": out})
}

func (s *Server) adminStats(c *gin.Context) {
	var users, teachers, students, lessons, listings, hidden, recordings int
	err := s.db.QueryRow(c, `SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM users WHERE role='teacher'),
		(SELECT count(*) FROM users WHERE role='student'),
		(SELECT count(*) FROM lessons),
		(SELECT count(*) FROM listings),
		(SELECT count(*) FROM listings WHERE hidden),
		(SELECT count(*) FROM recordings WHERE status='ready')`).Scan(&users, &teachers, &students, &lessons, &listings, &hidden, &recordings)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"users": users, "teachers": teachers, "students": students, "lessons": lessons, "listings": listings, "hiddenListings": hidden, "readyRecordings": recordings})
}
