package api

import (
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/auth"
	"ortalyq/backend/internal/db"
)

type registerReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Timezone string `json:"timezone"`
}

func validTimezone(tz string) bool {
	if tz == "" || len(tz) > 64 {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// Простейшее ограничение частоты попыток входа по IP.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

var loginLimiter = &limiter{hits: map[string][]time.Time{}}

func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	list := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			list = append(list, t)
		}
	}
	if len(list) >= max {
		l.hits[key] = list
		return false
	}
	l.hits[key] = append(list, now)
	return true
}

func (s *Server) register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if !loginLimiter.allow("reg:"+c.ClientIP(), 20, time.Hour) {
		fail(c, 429, "Слишком много попыток, попробуйте позже")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	if _, err := mail.ParseAddress(req.Email); err != nil || len(req.Email) > 200 {
		fail(c, 400, "Укажите корректный email")
		return
	}
	if utf8.RuneCountInString(req.Password) < 8 || len(req.Password) > 200 {
		fail(c, 400, "Пароль должен содержать не менее 8 символов")
		return
	}
	if n := utf8.RuneCountInString(req.Name); n < 2 || n > 80 {
		fail(c, 400, "Укажите имя (от 2 до 80 символов)")
		return
	}
	if req.Role != "student" && req.Role != "teacher" {
		fail(c, 400, "Выберите роль: ученик или преподаватель")
		return
	}
	if !validTimezone(req.Timezone) {
		req.Timezone = "Asia/Almaty"
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		internal(c, err)
		return
	}
	tx, err := s.db.Begin(c)
	if err != nil {
		internal(c, err)
		return
	}
	defer tx.Rollback(c)
	var id string
	err = tx.QueryRow(c, `INSERT INTO users(email, password_hash, role, name, timezone) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		req.Email, hash, req.Role, req.Name, req.Timezone).Scan(&id)
	if err != nil {
		if code, _ := db.ConstraintName(err); code == "23505" {
			fail(c, 409, "Пользователь с таким email уже существует")
			return
		}
		internal(c, err)
		return
	}
	if req.Role == "teacher" {
		if _, err := tx.Exec(c, `INSERT INTO teacher_profiles(user_id) VALUES ($1)`, id); err != nil {
			internal(c, err)
			return
		}
	}
	if err := tx.Commit(c); err != nil {
		internal(c, err)
		return
	}
	if err := s.startSession(c, id); err != nil {
		internal(c, err)
		return
	}
	c.JSON(201, gin.H{"user": gin.H{"id": id, "email": req.Email, "name": req.Name, "role": req.Role, "timezone": req.Timezone}})
}

func (s *Server) startSession(c *gin.Context, userID string) error {
	raw, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	ttl := time.Duration(s.cfg.SessionTTLHrs) * time.Hour
	if _, err := s.db.Exec(c, `INSERT INTO sessions(token_hash, user_id, expires_at, user_agent) VALUES ($1,$2,$3,$4)`,
		hash, userID, time.Now().Add(ttl), truncate(c.GetHeader("User-Agent"), 200)); err != nil {
		return err
	}
	s.setCookie(c, raw, int(ttl.Seconds()))
	return nil
}

func (s *Server) setCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(2) // http.SameSiteLaxMode
	c.SetCookie(sessionCookie, value, maxAge, "/", "", s.cfg.CookieSecure, true)
}

func (s *Server) login(c *gin.Context) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if !loginLimiter.allow("login:"+c.ClientIP(), 30, 10*time.Minute) {
		fail(c, 429, "Слишком много попыток входа, попробуйте через несколько минут")
		return
	}
	var id, hash, role, name, tz string
	var blocked bool
	err := s.db.QueryRow(c, `SELECT id, password_hash, role, name, timezone, blocked FROM users WHERE lower(email)=lower($1)`,
		strings.TrimSpace(req.Email)).Scan(&id, &hash, &role, &name, &tz, &blocked)
	if err != nil {
		if !db.IsNoRows(err) {
			internal(c, err)
			return
		}
		auth.DummyCheck(req.Password)
		fail(c, 401, "Неверный email или пароль")
		return
	}
	if !auth.CheckPassword(hash, req.Password) || blocked {
		fail(c, 401, "Неверный email или пароль")
		return
	}
	// Удаляем просроченные сессии пользователя
	s.db.Exec(c, `DELETE FROM sessions WHERE user_id=$1 AND expires_at < now()`, id)
	if err := s.startSession(c, id); err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"user": gin.H{"id": id, "email": strings.ToLower(req.Email), "name": name, "role": role, "timezone": tz}})
}

func (s *Server) logout(c *gin.Context) {
	if raw, err := c.Cookie(sessionCookie); err == nil && raw != "" {
		s.db.Exec(c, `DELETE FROM sessions WHERE token_hash=$1`, auth.HashToken(raw))
	}
	s.setCookie(c, "", -1)
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) me(c *gin.Context) {
	u := currentUser(c)
	if u == nil {
		c.JSON(200, gin.H{"user": nil})
		return
	}
	var city, phone string
	s.db.QueryRow(c, `SELECT city, phone FROM users WHERE id=$1`, u.ID).Scan(&city, &phone)
	c.JSON(200, gin.H{"user": gin.H{"id": u.ID, "email": u.Email, "name": u.Name, "role": u.Role, "timezone": u.Timezone, "city": city, "phone": phone}})
}

var phoneJunk = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", "\u00a0", "")

// normalizePhone приводит казахстанский номер к виду +7XXXXXXXXXX. Пустая строка допустима.
func normalizePhone(v string) (string, bool) {
	v = phoneJunk.Replace(strings.TrimSpace(v))
	if v == "" {
		return "", true
	}
	if strings.HasPrefix(v, "8") && len(v) == 11 {
		v = "+7" + v[1:]
	}
	if len(v) != 12 || !strings.HasPrefix(v, "+7") {
		return "", false
	}
	for _, r := range v[1:] {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return v, true
}

func (s *Server) updateMe(c *gin.Context) {
	u := currentUser(c)
	var req struct {
		Name     *string `json:"name"`
		Timezone *string `json:"timezone"`
		City     *string `json:"city"`
		Phone    *string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if l := utf8.RuneCountInString(n); l < 2 || l > 80 {
			fail(c, 400, "Имя должно содержать от 2 до 80 символов")
			return
		}
		if _, err := s.db.Exec(c, `UPDATE users SET name=$1 WHERE id=$2`, n, u.ID); err != nil {
			internal(c, err)
			return
		}
	}
	if req.Timezone != nil {
		if !validTimezone(*req.Timezone) {
			fail(c, 400, "Неизвестный часовой пояс")
			return
		}
		if _, err := s.db.Exec(c, `UPDATE users SET timezone=$1 WHERE id=$2`, *req.Timezone, u.ID); err != nil {
			internal(c, err)
			return
		}
	}
	if req.City != nil {
		city := strings.TrimSpace(*req.City)
		if utf8.RuneCountInString(city) > 60 {
			fail(c, 400, "Название города слишком длинное")
			return
		}
		if _, err := s.db.Exec(c, `UPDATE users SET city=$1 WHERE id=$2`, city, u.ID); err != nil {
			internal(c, err)
			return
		}
	}
	if req.Phone != nil {
		phone, ok := normalizePhone(*req.Phone)
		if !ok {
			fail(c, 400, "Телефон в формате +7 XXX XXX XX XX")
			return
		}
		if _, err := s.db.Exec(c, `UPDATE users SET phone=$1 WHERE id=$2`, phone, u.ID); err != nil {
			internal(c, err)
			return
		}
	}
	c.JSON(200, gin.H{"ok": true})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
