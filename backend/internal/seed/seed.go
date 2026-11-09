// Package seed создаёт вымышленные демонстрационные данные. Все записи помечены is_demo=true
// и пересоздаются при каждом запуске.
package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ortalyq/backend/internal/auth"
)

const (
	StudentEmail  = "student@demo.ortalyq.local"
	Student2Email = "student2@demo.ortalyq.local"
	TeacherEmail  = "teacher@demo.ortalyq.local"
)

func Run(ctx context.Context, pool *pgxpool.Pool, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Удаляем прежние демо-данные (занятия с участием демо-пользователей удаляются каскадно со всем содержимым).
	if _, err := tx.Exec(ctx, `DELETE FROM lessons WHERE is_demo OR teacher_id IN (SELECT id FROM users WHERE is_demo) OR student_id IN (SELECT id FROM users WHERE is_demo)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM reviews WHERE teacher_id IN (SELECT id FROM users WHERE is_demo) OR student_id IN (SELECT id FROM users WHERE is_demo)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chat_messages WHERE user_id IN (SELECT id FROM users WHERE is_demo)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE is_demo`); err != nil {
		return err
	}

	tz := DemoTimezone
	ids := map[string]string{}
	for _, t := range teachers {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name, timezone, city, phone, is_demo) VALUES ($1,$2,'teacher',$3,$4,$5,$6,true) RETURNING id`,
			t.email, hash, t.name, tz, t.city, t.phone).Scan(&id); err != nil {
			return fmt.Errorf("insert teacher: %w", err)
		}
		ids[t.email] = id
		if _, err := tx.Exec(ctx, `INSERT INTO teacher_profiles(user_id, headline, bio, subjects, price, experience_years) VALUES ($1,$2,$3,$4,$5,$6)`,
			id, t.headline, t.bio, t.subjects, t.price, t.exp); err != nil {
			return err
		}
	}
	for _, st := range students {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name, timezone, city, phone, is_demo) VALUES ($1,$2,'student',$3,$4,$5,$6,true) RETURNING id`,
			st.email, hash, st.name, tz, st.city, st.phone).Scan(&id); err != nil {
			return err
		}
		ids[st.email] = id
	}

	now := time.Now().UTC().Truncate(time.Minute)
	loc, _ := time.LoadLocation(tz)
	day := func(offset int, hour int) time.Time {
		d := now.In(loc).AddDate(0, 0, offset)
		return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, loc).UTC()
	}

	// Окна доступности на 14 дней вперёд
	for i, t := range teachers {
		for d := 0; d < 14; d++ {
			if (d+i)%7 == 6 {
				continue // выходной
			}
			from, to := day(d, 9+i%3), day(d, 13+i%3)
			if d%2 == 1 {
				from, to = day(d, 15), day(d, 20)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3)`, ids[t.email], from, to); err != nil {
				return err
			}
		}
	}

	// Объявления
	for i, l := range listings {
		if _, err := tx.Exec(ctx, `INSERT INTO listings(author_id, kind, subject, title, body, price, is_demo, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,true,$7,$7)`,
			ids[l.author], l.kind, l.subject, l.title, l.body, l.price, now.Add(-time.Duration(i*5)*time.Hour)); err != nil {
			return err
		}
	}

	teacher, student, student2 := ids[TeacherEmail], ids[StudentEmail], ids[Student2Email]

	// 1) Демо-класс для показа: вход открыт сразу и в любое время (always_open).
	if err := createDemoRoom(ctx, tx, teacher, student); err != nil {
		return err
	}
	// 2) Будущие занятия
	if _, err := tx.Exec(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, note, starts_at, ends_at, price, is_demo) VALUES ($1,$2,'Физика','Механика: законы Ньютона',$3,$4,$5,true)`,
		teacher, student, day(2, 16), day(2, 17), teachers[0].price); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, note, starts_at, ends_at, price, is_demo) VALUES ($1,$2,'Математика','Подготовка к контрольной',$3,$4,$5,true)`,
		teacher, student2, day(3, 17), day(3, 18), teachers[0].price); err != nil {
		return err
	}
	// 3) Завершённое занятие с доской, чатом, материалами-заданием и отзывом
	pastStart := day(-3, 17)
	var pastID string
	if err := tx.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, note, starts_at, ends_at, status, started_at, ended_at, price, is_demo)
		VALUES ($1,$2,'Математика','Линейные уравнения',$3,$4,'completed',$3,$4,$5,true) RETURNING id`,
		teacher, student, pastStart, pastStart.Add(time.Hour), teachers[0].price).Scan(&pastID); err != nil {
		return err
	}
	board, _ := json.Marshal(demoBoard())
	if _, err := tx.Exec(ctx, `INSERT INTO boards(lesson_id, elements, revision) VALUES ($1,$2,1)`, pastID, board); err != nil {
		return err
	}
	chat := []struct {
		who  string
		body string
		min  int
	}{
		{teacher, "Добрый вечер! Сегодня разбираем линейные уравнения.", 1},
		{student, "Здравствуйте! Готов.", 2},
		{teacher, "Посмотрите на доску: перенесём слагаемые с x в левую часть.", 5},
		{student, "Получилось x = 4", 12},
		{teacher, "Верно! Домашнее задание добавила на страницу урока.", 55},
	}
	for _, m := range chat {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_messages(lesson_id, user_id, body, created_at) VALUES ($1,$2,$3,$4)`,
			pastID, m.who, m.body, pastStart.Add(time.Duration(m.min)*time.Minute)); err != nil {
			return err
		}
	}
	var hwID string
	if err := tx.QueryRow(ctx, `INSERT INTO homeworks(lesson_id, title, description, due_at, created_at) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		pastID, "Решить 5 линейных уравнений", "1) 3x + 7 = 22\n2) 5 − 2x = 11\n3) 4(x − 1) = 2x + 6\n4) x/3 + 2 = 5\n5) 0,5x − 1,5 = 2,5\n\nЗапишите ход решения.",
		pastStart.Add(4*24*time.Hour), pastStart.Add(58*time.Minute)).Scan(&hwID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO homework_submissions(homework_id, student_id, body, status, teacher_comment, grade, submitted_at, reviewed_at)
		VALUES ($1,$2,$3,'reviewed',$4,5,$5,$6)`, hwID, student,
		"1) x = 5\n2) x = −3\n3) x = 5\n4) x = 9\n5) x = 8", "Отлично, всё верно! В №3 можно было записать раскрытие скобок подробнее.",
		pastStart.Add(30*time.Hour), pastStart.Add(40*time.Hour)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO reviews(lesson_id, student_id, teacher_id, rating, body, created_at) VALUES ($1,$2,$3,5,$4,$5)`,
		pastID, student, teacher, "Демо-отзыв: всё понятно объяснила, доска очень помогает!", pastStart.Add(2*time.Hour)); err != nil {
		return err
	}
	// Ещё одно завершённое занятие с заданием, ожидающим ответа ученика
	past2 := day(-1, 16)
	var past2ID string
	if err := tx.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, note, starts_at, ends_at, status, started_at, ended_at, price, is_demo)
		VALUES ($1,$2,'Физика','Кинематика',$3,$4,'completed',$3,$4,$5,true) RETURNING id`,
		teacher, student, past2, past2.Add(time.Hour), teachers[0].price).Scan(&past2ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO homeworks(lesson_id, title, description, due_at) VALUES ($1,$2,$3,$4)`,
		past2ID, "Задачи на равноускоренное движение", "Решите задачи 3–7 из конспекта. Ответ можно прислать текстом или фото решения.", now.Add(3*24*time.Hour)); err != nil {
		return err
	}
	// Отзывы для других преподавателей (рейтинг в каталоге)
	for i, t := range teachers[1:] {
		st := []string{student2, ids["student3@demo.ortalyq.local"]}[i%2]
		ps := day(-5-i, 15)
		var lid string
		if err := tx.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, status, started_at, ended_at, price, is_demo)
			VALUES ($1,$2,$3,$4,$5,'completed',$4,$5,$6,true) RETURNING id`, ids[t.email], st, t.subjects[0], ps, ps.Add(time.Hour), t.price).Scan(&lid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO reviews(lesson_id, student_id, teacher_id, rating, body) VALUES ($1,$2,$3,$4,$5)`,
			lid, st, ids[t.email], 4+i%2, "Демо-отзыв: занятие прошло продуктивно."); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func ptr[T any](v T) *T { return &v }

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const DemoRoomNote = "Демо-класс: вход открыт в любое время"

func createDemoRoom(ctx context.Context, tx execer, teacher, student string) error {
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	_, err := tx.Exec(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, note, starts_at, ends_at, price, is_demo, always_open)
		VALUES ($1,$2,'Математика',$3,$4,$5,$6,true,true)`, teacher, student, DemoRoomNote, start, start.Add(60*time.Minute), teachers[0].price)
	return err
}

// ResetDemoRoom пересоздаёт только демо-класс (чистая доска и чат), не трогая остальные данные.
func ResetDemoRoom(ctx context.Context, pool *pgxpool.Pool) error {
	var teacher, student string
	if err := pool.QueryRow(ctx, `SELECT (SELECT id FROM users WHERE email=$1 AND is_demo), (SELECT id FROM users WHERE email=$2 AND is_demo)`,
		TeacherEmail, StudentEmail).Scan(&teacher, &student); err != nil {
		return fmt.Errorf("демо-аккаунты не найдены — сначала выполните seed")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM lessons WHERE always_open AND is_demo`); err != nil {
		return err
	}
	return createDemoRoom(ctx, pool, teacher, student)
}

// demoBoard — минимальные элементы Excalidraw; недостающие поля восстанавливает клиент (restoreElements).
func demoBoard() []map[string]any {
	r := rand.New(rand.NewSource(42))
	base := func(id, typ string, x, y, w, h float64) map[string]any {
		return map[string]any{
			"id": id, "type": typ, "x": x, "y": y, "width": w, "height": h, "angle": 0,
			"strokeColor": "#5b21b6", "backgroundColor": "transparent", "fillStyle": "solid",
			"strokeWidth": 2, "strokeStyle": "solid", "roughness": 1, "opacity": 100,
			"groupIds": []any{}, "frameId": nil, "roundness": nil, "seed": r.Intn(1 << 30),
			"version": 2, "versionNonce": r.Intn(1 << 30), "isDeleted": false, "boundElements": nil,
			"updated": 1700000000000, "link": nil, "locked": false,
		}
	}
	text := func(id string, x, y float64, s string, size float64) map[string]any {
		e := base(id, "text", x, y, float64(len([]rune(s)))*size*0.55, size*1.25)
		e["text"], e["originalText"], e["fontSize"], e["fontFamily"] = s, s, size, 5
		e["textAlign"], e["verticalAlign"], e["containerId"], e["lineHeight"], e["autoResize"] = "left", "top", nil, 1.25, true
		e["strokeColor"] = "#1f2937"
		return e
	}
	rect := base("demo-rect", "rectangle", 80, 150, 420, 170)
	rect["backgroundColor"] = "#ede9fe"
	rect["roundness"] = map[string]any{"type": 3}
	arrow := base("demo-arrow", "arrow", 520, 235, 160, 0)
	arrow["points"] = []any{[]any{0, 0}, []any{160, 0}}
	arrow["endArrowhead"] = "arrow"
	arrow["startArrowhead"] = nil
	return []map[string]any{
		text("demo-title", 80, 60, "Линейные уравнения", 36),
		rect,
		text("demo-eq1", 110, 180, "3x + 7 = 2x + 11", 28),
		text("demo-eq2", 110, 240, "3x − 2x = 11 − 7", 28),
		arrow,
		text("demo-answer", 700, 215, "x = 4", 36),
	}
}

// CreateAdmin создаёт администратора или меняет пароль существующего.
func CreateAdmin(ctx context.Context, pool *pgxpool.Pool, email, name, password string) error {
	if len(password) < 12 {
		return fmt.Errorf("пароль администратора должен быть не короче 12 символов")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	var id string
	err = pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=lower($1)`, email).Scan(&id)
	if err == pgx.ErrNoRows {
		_, err = pool.Exec(ctx, `INSERT INTO users(email, password_hash, role, name) VALUES ($1,$2,'admin',$3)`, email, hash, name)
		return err
	}
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `UPDATE users SET password_hash=$1, role='admin' WHERE id=$2`, hash, id)
	return err
}
