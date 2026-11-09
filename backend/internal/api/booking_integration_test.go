package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ortalyq/backend/internal/auth"
)

// Интеграционный тест записи на урок (HTTP + PostgreSQL). Требует TEST_DATABASE_URL.
// Преподаватель доступен 10:00–14:00 (Asia/Almaty) через 3 дня; время начала — любая минута.

type bookEnv struct {
	*tenv
	ids     map[string]string
	day     time.Time
	teacher string
}

func setupBooking(t *testing.T) *bookEnv {
	e := setupCode(t) // общая инфраструктура: БД, миграции, сервер
	ctx := context.Background()
	b := &bookEnv{tenv: e, ids: map[string]string{}}
	sfx := fmt.Sprint(time.Now().UnixNano())
	for _, u := range []struct{ key, role, tz string }{
		{"t1", "teacher", "Asia/Almaty"}, {"t2", "teacher", "Asia/Almaty"},
		{"a", "student", "Asia/Almaty"}, {"b", "student", "Asia/Almaty"}, {"msk", "student", "Europe/Moscow"},
	} {
		var id string
		if err := e.pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name, timezone) VALUES ($1,'x',$2,$3,$4) RETURNING id`,
			u.key+sfx+"@book.test", u.role, "Тест "+u.key, u.tz).Scan(&id); err != nil {
			t.Fatal(err)
		}
		raw, hash, _ := auth.NewToken()
		if _, err := e.pool.Exec(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1,$2, now() + interval '1 hour')`, hash, id); err != nil {
			t.Fatal(err)
		}
		b.ids[u.key], e.cookies[u.key] = id, raw
		if u.role == "teacher" {
			if _, err := e.pool.Exec(ctx, `INSERT INTO teacher_profiles(user_id, subjects, price) VALUES ($1, '{Математика}', 6000)`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	b.day = time.Now().In(almaty).AddDate(0, 0, 3)
	for _, tch := range []string{"t1", "t2"} {
		if _, err := e.pool.Exec(ctx, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3)`,
			b.ids[tch], at(b.day, "10:00"), at(b.day, "14:00")); err != nil {
			t.Fatal(err)
		}
	}
	b.teacher = b.ids["t1"]
	return b
}

func (b *bookEnv) book(who, teacher string, start time.Time, dur int) (int, string) {
	code, body := b.req(who, "POST", "/api/lessons", map[string]any{"teacherId": teacher, "startsAt": start.Format(time.RFC3339Nano), "duration": dur, "subject": "Математика"})
	msg, _ := body["error"].(string)
	return code, msg
}

func TestBookingAnyMinute(t *testing.T) {
	b := setupBooking(t)
	ctx := context.Background()
	var availBefore string
	b.pool.QueryRow(ctx, `SELECT string_agg(id::text||starts_at::text||ends_at::text, ',' ORDER BY id) FROM availability WHERE teacher_id IN ($1,$2)`, b.ids["t1"], b.ids["t2"]).Scan(&availBefore)

	// Свободные интервалы до записи: одно окно, начало с 10:00 до 13:00 (урок 60 мин)
	code, body := b.req("a", "GET", "/api/teachers/"+b.teacher+"/slots?duration=60", nil)
	if code != 200 {
		t.Fatalf("slots: %d", code)
	}
	var found bool
	for _, x := range body["intervals"].([]any) {
		iv := x.(map[string]any)
		if iv["startsAt"] == at(b.day, "10:00").Format(time.RFC3339) && iv["lastStartAt"] == at(b.day, "13:00").Format(time.RFC3339) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ожидался интервал 10:00–14:00 с последним началом 13:00: %v", body["intervals"])
	}

	type tc struct {
		name, who, teacher, hhmm string
		dur, code                int
		msg                      string
	}
	for _, c := range []tc{
		{"нестандартные минуты 10:05", "a", b.teacher, "10:05", 60, 201, ""},
		{"пересечение с занятием преподавателя", "b", b.teacher, "10:42", 60, 409, "другим занятием преподавателя (10:05–11:05)"},
		{"сразу после чужого занятия 11:05", "b", b.teacher, "11:05", 60, 201, ""},
		{"последнее возможное начало 13:00", "a", b.teacher, "13:00", 60, 201, ""},
		{"выход за границу интервала 13:01", "b", b.teacher, "13:01", 60, 409, "Последнее возможное начало — 13:00"},
		{"вне доступности 15:10", "b", b.teacher, "15:10", 30, 409, "Преподаватель недоступен"},
		{"раньше начала доступности 09:50", "b", b.teacher, "09:50", 30, 409, "Преподаватель недоступен"},
		{"пересечение с занятием ученика у другого преподавателя", "a", b.ids["t2"], "10:30", 60, 409, "У вас уже есть занятие"},
		{"причина в часовом поясе ученика (Москва = Алматы − 2 ч)", "msk", b.ids["t2"], "13:01", 60, 409, "Последнее возможное начало — 11:00"},
	} {
		code, msg := b.book(c.who, c.teacher, at(b.day, c.hhmm), c.dur)
		if code != c.code || !strings.Contains(msg, c.msg) {
			t.Errorf("%s: %d %q (ожидалось %d, %q)", c.name, code, msg, c.code, c.msg)
		}
	}

	// Прошедшее время и секунды
	if code, msg := b.book("b", b.teacher, time.Now().Add(-time.Hour).Truncate(time.Minute), 30); code != 400 || !strings.Contains(msg, "уже прошло") {
		t.Errorf("прошлое: %d %q", code, msg)
	}
	if code, msg := b.book("b", b.teacher, at(b.day, "12:20").Add(30*time.Second), 30); code != 400 || !strings.Contains(msg, "точностью до минуты") {
		t.Errorf("секунды: %d %q", code, msg)
	}

	// Одновременное бронирование одного времени (12:17, 30 мин) восемью запросами двух учеников
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = b.book([]string{"a", "b"}[i%2], b.teacher, at(b.day, "12:17"), 30)
		}(i)
	}
	wg.Wait()
	created, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 409:
			conflict++
		}
	}
	if created != 1 || conflict != 7 {
		t.Fatalf("одновременно: создано %d, конфликтов %d (%v)", created, conflict, codes)
	}

	// Свободное время после записей: под 30 мин в этот день места нет
	_, body = b.req("b", "GET", "/api/teachers/"+b.teacher+"/slots?duration=30", nil)
	for _, x := range body["intervals"].([]any) {
		iv := x.(map[string]any)
		s, _ := time.Parse(time.RFC3339, iv["startsAt"].(string))
		if s.In(almaty).Format("2006-01-02") == b.day.Format("2006-01-02") {
			t.Errorf("в этот день не должно остаться интервалов ≥30 мин: %v", iv)
		}
	}
	if n := len(body["myLessons"].([]any)); n < 1 {
		t.Errorf("myLessons ученика b пуст")
	}

	// Ровно 4 занятия у преподавателя, расписание не изменено
	var n int
	b.pool.QueryRow(ctx, `SELECT count(*) FROM lessons WHERE teacher_id=$1 AND status <> 'cancelled'`, b.teacher).Scan(&n)
	var availAfter string
	b.pool.QueryRow(ctx, `SELECT string_agg(id::text||starts_at::text||ends_at::text, ',' ORDER BY id) FROM availability WHERE teacher_id IN ($1,$2)`, b.ids["t1"], b.ids["t2"]).Scan(&availAfter)
	if n != 4 || availAfter != availBefore {
		t.Fatalf("занятий %d (ожидалось 4), расписание изменено: %v", n, availAfter != availBefore)
	}
}
