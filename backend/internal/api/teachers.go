package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/db"
)

var defaultSubjects = []string{"Математика", "Английский язык", "Физика", "Химия", "Русский язык", "Информатика", "Биология", "История", "Казахский язык"}

func (s *Server) subjects(c *gin.Context) {
	set := map[string]bool{}
	for _, v := range defaultSubjects {
		set[v] = true
	}
	rows, err := s.db.Query(c, `SELECT DISTINCT unnest(subjects) FROM teacher_profiles UNION SELECT DISTINCT subject FROM listings WHERE NOT hidden`)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil && v != "" {
			set[v] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	cities := []string{"Алматы", "Астана", "Шымкент", "Караганда"}
	seenCity := map[string]bool{}
	for _, v := range cities {
		seenCity[v] = true
	}
	if crow, err := s.db.Query(c, `SELECT DISTINCT city FROM users WHERE role='teacher' AND city <> '' ORDER BY city`); err == nil {
		for crow.Next() {
			var v string
			if crow.Scan(&v) == nil && !seenCity[v] {
				seenCity[v] = true
				cities = append(cities, v)
			}
		}
		crow.Close()
	}
	c.JSON(200, gin.H{"subjects": out, "cities": cities})
}

type teacherCard struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Headline    string   `json:"headline"`
	Bio         string   `json:"bio"`
	Subjects    []string `json:"subjects"`
	Price       int      `json:"price"`
	Experience  int      `json:"experienceYears"`
	PhotoURL    *string  `json:"photoUrl"`
	Rating      *float64 `json:"rating"`
	ReviewCount int      `json:"reviewCount"`
	IsDemo      bool     `json:"isDemo"`
	Timezone    string   `json:"timezone"`
	City        string   `json:"city"`
}

const teacherSelect = `
	SELECT u.id, u.name, p.headline, p.bio, p.subjects, p.price, p.experience_years, p.photo_key IS NOT NULL,
	       (SELECT avg(rating)::float8 FROM reviews r WHERE r.teacher_id = u.id),
	       (SELECT count(*) FROM reviews r WHERE r.teacher_id = u.id),
	       u.is_demo, u.timezone, u.city
	FROM users u JOIN teacher_profiles p ON p.user_id = u.id
	WHERE u.role = 'teacher' AND NOT u.blocked`

func scanTeacher(row interface{ Scan(...any) error }) (teacherCard, error) {
	var t teacherCard
	var hasPhoto bool
	err := row.Scan(&t.ID, &t.Name, &t.Headline, &t.Bio, &t.Subjects, &t.Price, &t.Experience, &hasPhoto, &t.Rating, &t.ReviewCount, &t.IsDemo, &t.Timezone, &t.City)
	if hasPhoto {
		u := "/api/teachers/" + t.ID + "/photo"
		t.PhotoURL = &u
	}
	if t.Subjects == nil {
		t.Subjects = []string{}
	}
	return t, err
}

func (s *Server) listTeachers(c *gin.Context) {
	q := teacherSelect
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		q += fmt.Sprintf(" AND "+cond, len(args))
	}
	if v := strings.TrimSpace(c.Query("subject")); v != "" {
		add("$%d = ANY(p.subjects)", v)
	}
	if v := strings.TrimSpace(c.Query("city")); v != "" {
		add("u.city = $%d", v)
	}
	if v, err := strconv.Atoi(c.Query("minPrice")); err == nil {
		add("p.price >= $%d", v)
	}
	if v, err := strconv.Atoi(c.Query("maxPrice")); err == nil {
		add("p.price <= $%d", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		add("(u.name ILIKE '%%' || $%d || '%%')", v)
	}
	// Показываем только преподавателей с заполненным профилем
	q += " AND cardinality(p.subjects) > 0"
	switch c.Query("sort") {
	case "price_asc":
		q += " ORDER BY p.price ASC, u.name"
	case "price_desc":
		q += " ORDER BY p.price DESC, u.name"
	default:
		q += " ORDER BY (SELECT avg(rating) FROM reviews r WHERE r.teacher_id = u.id) DESC NULLS LAST, u.name"
	}
	rows, err := s.db.Query(c, q, args...)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	out := []teacherCard{}
	for rows.Next() {
		t, err := scanTeacher(rows)
		if err != nil {
			internal(c, err)
			return
		}
		out = append(out, t)
	}
	c.JSON(200, gin.H{"teachers": out})
}

func (s *Server) getTeacher(c *gin.Context) {
	if !isUUID(c.Param("id")) {
		fail(c, 404, "Преподаватель не найден")
		return
	}
	t, err := scanTeacher(s.db.QueryRow(c, teacherSelect+" AND u.id = $1", c.Param("id")))
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Преподаватель не найден")
			return
		}
		internal(c, err)
		return
	}
	rows, err := s.db.Query(c, `SELECT r.rating, r.body, r.created_at, u.name FROM reviews r JOIN users u ON u.id = r.student_id
		WHERE r.teacher_id = $1 ORDER BY r.created_at DESC LIMIT 20`, t.ID)
	if err != nil {
		internal(c, err)
		return
	}
	defer rows.Close()
	reviews := []gin.H{}
	for rows.Next() {
		var rating int
		var body, name string
		var at time.Time
		if err := rows.Scan(&rating, &body, &at, &name); err != nil {
			internal(c, err)
			return
		}
		reviews = append(reviews, gin.H{"rating": rating, "body": body, "createdAt": at, "studentName": firstName(name)})
	}
	c.JSON(200, gin.H{"teacher": t, "reviews": reviews})
}

func firstName(n string) string {
	parts := strings.Fields(n)
	if len(parts) == 0 {
		return "Ученик"
	}
	return parts[0]
}

// teacherSlots возвращает свободные времена начала занятия внутри окон доступности.
// teacherSlots — свободные интервалы преподавателя на 3 недели для урока выбранной длительности.
// Любая минута в [startsAt, lastStartAt] интервала — допустимое начало (сетки нет).
// Для вошедшего ученика дополнительно возвращаются его действующие занятия (myLessons),
// чтобы форма заранее предупреждала о пересечении.
func (s *Server) teacherSlots(c *gin.Context) {
	id := c.Param("id")
	if !isUUID(id) {
		fail(c, 404, "Преподаватель не найден")
		return
	}
	dur, _ := strconv.Atoi(c.DefaultQuery("duration", "60"))
	if !validDuration(dur) {
		fail(c, 400, "Недопустимая длительность")
		return
	}
	now := time.Now().UTC()
	horizon := now.Add(21 * 24 * time.Hour)
	avail, err := s.spans(c, `SELECT starts_at, ends_at FROM availability WHERE teacher_id=$1 AND ends_at > $2 AND starts_at < $3`, id, now, horizon)
	if err != nil {
		internal(c, err)
		return
	}
	busy, err := s.spans(c, `SELECT starts_at, ends_at FROM lessons WHERE teacher_id=$1 AND status <> 'cancelled' AND ends_at > $2 AND starts_at < $3`, id, now, horizon.Add(24*time.Hour))
	if err != nil {
		internal(c, err)
		return
	}
	mine := []gin.H{}
	if u := currentUser(c); u != nil && u.Role == "student" {
		own, err := s.spans(c, `SELECT starts_at, ends_at FROM lessons WHERE student_id=$1 AND status <> 'cancelled' AND ends_at > $2 AND starts_at < $3`, u.ID, now, horizon.Add(24*time.Hour))
		if err != nil {
			internal(c, err)
			return
		}
		for _, x := range own {
			mine = append(mine, gin.H{"startsAt": x.From, "endsAt": x.To})
		}
	}
	c.JSON(200, gin.H{
		"duration":      dur,
		"intervals":     freeIntervals(avail, busy, now, time.Duration(dur)*time.Minute),
		"myLessons":     mine,
		"earliestStart": earliestStart(now),
	})
}

func (s *Server) spans(c *gin.Context, q string, args ...any) ([]span, error) {
	rows, err := s.db.Query(c, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []span{}
	for rows.Next() {
		var x span
		if err := rows.Scan(&x.From, &x.To); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func validDuration(d int) bool { return d == 30 || d == 45 || d == 60 || d == 90 }

func (s *Server) teacherPhoto(c *gin.Context) {
	if s.store == nil || !isUUID(c.Param("id")) {
		fail(c, 404, "Нет фото")
		return
	}
	var key *string
	if err := s.db.QueryRow(c, `SELECT photo_key FROM teacher_profiles WHERE user_id=$1`, c.Param("id")).Scan(&key); err != nil || key == nil {
		fail(c, 404, "Нет фото")
		return
	}
	obj, info, err := s.store.Open(c, *key)
	if err != nil {
		fail(c, 404, "Нет фото")
		return
	}
	defer obj.Close()
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("Content-Type", info.ContentType)
	http.ServeContent(c.Writer, c.Request, "photo", info.LastModified, obj)
}

func (s *Server) myTeacherProfile(c *gin.Context) {
	u := currentUser(c)
	if u.Role != "teacher" {
		c.JSON(200, gin.H{"profile": nil})
		return
	}
	t, err := scanTeacher(s.db.QueryRow(c, `
	SELECT u.id, u.name, p.headline, p.bio, p.subjects, p.price, p.experience_years, p.photo_key IS NOT NULL,
	       (SELECT avg(rating)::float8 FROM reviews r WHERE r.teacher_id = u.id),
	       (SELECT count(*) FROM reviews r WHERE r.teacher_id = u.id), u.is_demo, u.timezone, u.city
	FROM users u JOIN teacher_profiles p ON p.user_id = u.id WHERE u.id = $1`, u.ID))
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"profile": t})
}

func (s *Server) updateTeacherProfile(c *gin.Context) {
	u := currentUser(c)
	var req struct {
		Headline   string   `json:"headline"`
		Bio        string   `json:"bio"`
		Subjects   []string `json:"subjects"`
		Price      int      `json:"price"`
		Experience int      `json:"experienceYears"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "Некорректный запрос")
		return
	}
	subjects := []string{}
	seen := map[string]bool{}
	for _, sub := range req.Subjects {
		sub = strings.TrimSpace(sub)
		if sub != "" && !seen[sub] && utf8.RuneCountInString(sub) <= 40 {
			seen[sub] = true
			subjects = append(subjects, sub)
		}
	}
	switch {
	case len(subjects) == 0 || len(subjects) > 8:
		fail(c, 400, "Укажите от 1 до 8 предметов")
		return
	case req.Price < 0 || req.Price > 1_000_000:
		fail(c, 400, "Некорректная стоимость")
		return
	case utf8.RuneCountInString(req.Headline) > 120 || utf8.RuneCountInString(req.Bio) > 3000:
		fail(c, 400, "Слишком длинный текст")
		return
	case req.Experience < 0 || req.Experience > 70:
		fail(c, 400, "Некорректный опыт")
		return
	}
	_, err := s.db.Exec(c, `INSERT INTO teacher_profiles(user_id, headline, bio, subjects, price, experience_years, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6, now())
		ON CONFLICT (user_id) DO UPDATE SET headline=$2, bio=$3, subjects=$4, price=$5, experience_years=$6, updated_at=now()`,
		u.ID, strings.TrimSpace(req.Headline), strings.TrimSpace(req.Bio), subjects, req.Price, req.Experience)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) uploadPhoto(c *gin.Context) {
	if s.store == nil {
		fail(c, 503, "Хранилище файлов не настроено")
		return
	}
	u := currentUser(c)
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "Файл не получен")
		return
	}
	if fh.Size > 5<<20 {
		fail(c, 413, "Фото должно быть не больше 5 МБ")
		return
	}
	f, err := fh.Open()
	if err != nil {
		internal(c, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 5<<20+1))
	if err != nil {
		internal(c, err)
		return
	}
	ct := http.DetectContentType(data)
	if ct != "image/jpeg" && ct != "image/png" && ct != "image/webp" {
		fail(c, 415, "Допустимы только изображения JPEG, PNG или WebP")
		return
	}
	key := fmt.Sprintf("photos/%s/%d", u.ID, time.Now().UnixNano())
	if err := s.store.Put(c, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		internal(c, err)
		return
	}
	var old *string
	s.db.QueryRow(c, `SELECT photo_key FROM teacher_profiles WHERE user_id=$1`, u.ID).Scan(&old)
	if _, err := s.db.Exec(c, `UPDATE teacher_profiles SET photo_key=$1 WHERE user_id=$2`, key, u.ID); err != nil {
		internal(c, err)
		return
	}
	if old != nil {
		s.store.Delete(c, *old)
	}
	c.JSON(200, gin.H{"photoUrl": "/api/teachers/" + u.ID + "/photo?v=" + strconv.FormatInt(time.Now().Unix(), 10)})
}
