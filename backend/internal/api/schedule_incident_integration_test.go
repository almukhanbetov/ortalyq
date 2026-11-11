package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"ortalyq/backend/internal/auth"
)

// Воспроизведение ситуации с производства (11.11.2026): у преподавателя урок с учеником К 12:00–13:00,
// урок с учеником Э 13:00–14:00, доступность 12:40–14:30 создана ПОСЛЕ записи К. Ученик К видит
// «Нет свободного времени». Времена — относительно текущего момента (12:55 → now).
func TestScheduleIncidentReproduction(t *testing.T) {
	e := setupCode(t)
	ctx := context.Background()
	user := func(key, role, name string) string {
		var id string
		if err := e.pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name) VALUES ($1,'x',$2,$3) RETURNING id`,
			key+"@incident.test", role, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		raw, hash, _ := auth.NewToken()
		e.pool.Exec(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1,$2, now() + interval '1 hour')`, hash, id)
		e.cookies[key] = raw
		return id
	}
	tch, k, el := user("arman", "teacher", "Арман"), user("kairat", "student", "Kairat"), user("elnur", "student", "Elnur")
	b := time.Now().Truncate(time.Minute).Add(-55 * time.Minute) // «12:00»
	at := func(min int) time.Time { return b.Add(time.Duration(min) * time.Minute) }
	lesson := func(student string, from, to int, status string) string {
		var id string
		if err := e.pool.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, status) VALUES ($1,$2,'Математика',$3,$4,$5) RETURNING id`,
			tch, student, at(from), at(to), status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	lk := lesson(k, 0, 60, "live")                                                                                       // 12:00–13:00, идёт
	le := lesson(el, 60, 120, "scheduled")                                                                               // 13:00–14:00
	e.pool.Exec(ctx, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3)`, tch, at(40), at(150)) // 12:40–14:30

	// 1. «Мои занятия» → «Предстоящие»: урок есть и у преподавателя, и у ученика К
	for _, who := range []string{"arman", "kairat"} {
		_, body := e.raw(who, "GET", "/api/lessons?scope=upcoming", nil)
		if !strings.Contains(body, lk) {
			t.Fatalf("%s: урок К должен быть в «Предстоящих»: %s", who, body)
		}
	}

	// 2. Запись на 60 мин: пусто, причина — свободно только 30 минут (14:00–14:30)
	code, body := e.req("kairat", "GET", "/api/teachers/"+tch+"/slots?duration=60", nil)
	if code != 200 || len(body["intervals"].([]any)) != 0 || body["emptyReason"] != slotsTooShort || body["maxFreeMinutes"].(float64) != 30 || len(body["emptyReasons"].([]any)) != 1 {
		t.Fatalf("slots 60: %d %v", code, body)
	}
	// Ученик видит свой урок у этого преподавателя, но не чужой урок и не имя другого ученика
	mine := body["myLessonsWithTeacher"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["id"] != lk {
		t.Fatalf("свой урок у преподавателя: %v", mine)
	}
	_, raw := e.raw("kairat", "GET", "/api/teachers/"+tch+"/slots?duration=60", nil)
	if strings.Contains(raw, le) || strings.Contains(raw, "Elnur") {
		t.Fatal("ученику нельзя раскрывать чужие занятия")
	}
	// 30 мин помещаются
	if _, body := e.req("kairat", "GET", "/api/teachers/"+tch+"/slots?duration=30", nil); len(body["intervals"].([]any)) != 1 || body["emptyReason"] != "" {
		t.Fatalf("slots 30: %v", body)
	}
	// Аноним — без своих уроков
	if _, body := e.req("", "GET", "/api/teachers/"+tch+"/slots?duration=60", nil); len(body["myLessonsWithTeacher"].([]any)) != 0 {
		t.Fatal("аноним")
	}

	// 3. Расписание преподавателя: доступность, занятые интервалы (с именами — это его ученики) и свободное время
	code, body = e.req("arman", "GET", "/api/my/availability", nil)
	if code != 200 || len(body["availability"].([]any)) != 1 || len(body["lessons"].([]any)) != 2 || len(body["free"].([]any)) != 1 {
		t.Fatalf("my availability: %d %v", code, body)
	}
	f := body["free"].([]any)[0].(map[string]any)
	if fs, _ := time.Parse(time.RFC3339, f["startsAt"].(string)); !fs.Equal(at(120)) {
		t.Fatalf("свободно с 14:00: %v", f)
	}

	// 4. Всё время занято → booked; интервалов нет → no_availability
	lesson(el, 120, 150, "scheduled")
	if _, body := e.req("kairat", "GET", "/api/teachers/"+tch+"/slots?duration=30", nil); body["emptyReason"] != slotsBooked {
		t.Fatalf("booked: %v", body)
	}
	e.pool.Exec(ctx, `DELETE FROM availability WHERE teacher_id=$1`, tch)
	if _, body := e.req("kairat", "GET", "/api/teachers/"+tch+"/slots?duration=30", nil); body["emptyReason"] != slotsNoAvailability {
		t.Fatalf("no availability: %v", body)
	}
	// Удаление доступности не трогает созданные занятия — урок по-прежнему у обоих
	if _, raw := e.raw("kairat", "GET", "/api/lessons?scope=upcoming", nil); !strings.Contains(raw, lk) {
		t.Fatal("урок остаётся после удаления интервала")
	}
}
