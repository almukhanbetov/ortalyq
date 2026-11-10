package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ortalyq/backend/internal/auth"
	"ortalyq/backend/internal/config"
)

// Границы входа в класс без БД: [начало − JoinEarlyMinutes, окончание). Сравниваются абсолютные моменты.
func TestClassWindowBoundaries(t *testing.T) {
	s := &Server{cfg: &config.Config{JoinEarlyMinutes: 15}}
	start := time.Date(2030, 3, 15, 17, 20, 0, 0, almaty) // 17:20–18:20 по Алматы
	l := &lessonRow{Status: "scheduled", StartsAt: start, EndsAt: start.Add(time.Hour)}
	msk := userLocation("Europe/Moscow")
	for _, c := range []struct {
		name string
		now  time.Time
		want string // подстрока причины; "" — вход открыт
	}{
		{"за 16 минут до начала", start.Add(-16 * time.Minute), "откроется за 15 минут"},
		{"ровно за 15 минут", start.Add(-15 * time.Minute), ""},
		{"начало", start, ""},
		{"опоздание на 5 минут (17:25)", start.Add(5 * time.Minute), ""},
		{"за наносекунду до конца", start.Add(time.Hour - time.Nanosecond), ""},
		{"момент окончания 18:20", start.Add(time.Hour), "Время занятия истекло"},
		{"то же окончание в Москве (16:20 МСК)", time.Date(2030, 3, 15, 16, 20, 0, 0, msk), "Время занятия истекло"},
		{"16:19 МСК = 18:19 Алматы — урок ещё идёт", time.Date(2030, 3, 15, 16, 19, 0, 0, msk), ""},
	} {
		if got := s.classProblem(l, c.now); (c.want == "" && got != "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, ожидалось %q", c.name, got, c.want)
		}
	}
	if _, closes := s.joinWindow(l); !closes.Equal(l.EndsAt) {
		t.Errorf("вход закрывается в %v, а урок кончается в %v — продления быть не должно", closes, l.EndsAt)
	}
	for _, st := range []string{"cancelled", "completed"} {
		x := *l
		x.Status = st
		if s.classProblem(&x, start.Add(5*time.Minute)) == "" {
			t.Errorf("%s: вход в класс открыт во время урока", st)
		}
	}
	demo := &lessonRow{Status: "scheduled", AlwaysOpen: true, StartsAt: start.AddDate(-1, 0, 0), EndsAt: start.AddDate(-1, 0, 0).Add(time.Hour)}
	if got := s.classProblem(demo, start); got != "" {
		t.Errorf("постоянно открытый демо-урок закрыт: %q", got)
	}
}

// Интеграционный тест (HTTP + WebSocket + PostgreSQL, своя одноразовая БД; требует TEST_DATABASE_URL).
// Каждый сценарий — отдельная пара преподаватель/ученик, уроки вставляются относительно текущего момента.
func TestLessonAccessByTime(t *testing.T) {
	e := setupCode(t)
	ctx := context.Background()
	sfx := fmt.Sprint(time.Now().UnixNano())
	user := func(key, role, tz string) string {
		var id string
		if err := e.pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name, timezone) VALUES ($1,'x',$2,$3,$4) RETURNING id`,
			key+sfx+"@access.test", role, "Тест "+key, tz).Scan(&id); err != nil {
			t.Fatal(err)
		}
		raw, hash, _ := auth.NewToken()
		if _, err := e.pool.Exec(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1,$2, now() + interval '1 hour')`, hash, id); err != nil {
			t.Fatal(err)
		}
		e.cookies[key] = raw
		return id
	}
	now := time.Now().Truncate(time.Second)
	type lesson struct{ id, teacher, student string }
	mk := func(key string, from, to time.Duration, status string, alwaysOpen bool) lesson {
		l := lesson{teacher: "t_" + key, student: "s_" + key}
		tid := user(l.teacher, "teacher", "Asia/Almaty")
		sid := user(l.student, "student", "Europe/Moscow") // ученик в Москве, преподаватель в Алматы
		if err := e.pool.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, price, status, always_open, is_demo)
			VALUES ($1,$2,'Математика',$3,$4,6000,$5,$6,$6) RETURNING id`, tid, sid, now.Add(from), now.Add(to), status, alwaysOpen).Scan(&l.id); err != nil {
			t.Fatal(err)
		}
		return l
	}
	detail := func(who, id string) map[string]any {
		t.Helper()
		code, body := e.req(who, "GET", "/api/lessons/"+id, nil)
		if code != 200 {
			t.Fatalf("GET lesson %s: %d %v", id, code, body)
		}
		return body
	}
	inList := func(who, scope, id string) bool {
		_, body := e.req(who, "GET", "/api/lessons?scope="+scope, nil)
		return listIDs(body)[id]
	}
	// join: без LiveKit сервер отвечает 503 только после проверки окна — значит, окно открыто.
	join := func(who, id string) (int, string) {
		code, body := e.req(who, "POST", "/api/lessons/"+id+"/join", nil)
		msg, _ := body["error"].(string)
		return code, msg
	}
	wsCode := func(who, id string) int {
		c, resp, err := e.ws(who, id)
		if err == nil {
			c.CloseNow()
			return 101
		}
		if resp == nil {
			t.Fatalf("ws %s: %v", id, err)
		}
		return resp.StatusCode
	}
	type want struct {
		canJoin, upcoming bool
		joinCode          int
		joinMsg           string
		ws                int
	}
	check := func(name string, l lesson, w want) {
		t.Helper()
		for _, who := range []string{l.teacher, l.student} {
			d := detail(who, l.id)
			if d["canJoin"] != w.canJoin {
				t.Errorf("%s (%s): canJoin=%v, ожидалось %v", name, who, d["canJoin"], w.canJoin)
			}
			if inList(who, "upcoming", l.id) != w.upcoming || inList(who, "history", l.id) == w.upcoming {
				t.Errorf("%s (%s): в предстоящих=%v, в истории=%v; ожидалось предстоящее=%v", name, who,
					inList(who, "upcoming", l.id), inList(who, "history", l.id), w.upcoming)
			}
			if code, msg := join(who, l.id); code != w.joinCode || !strings.Contains(msg, w.joinMsg) {
				t.Errorf("%s (%s): join %d %q, ожидалось %d %q", name, who, code, msg, w.joinCode, w.joinMsg)
			}
			if code := wsCode(who, l.id); code != w.ws {
				t.Errorf("%s (%s): WebSocket класса %d, ожидалось %d", name, who, code, w.ws)
			}
		}
	}
	const noVideo = "Видеосвязь не настроена" // окно открыто, отказ только из-за отсутствия LiveKit в тесте

	before := mk("before", 30*time.Minute, 90*time.Minute, "scheduled", false)
	early := mk("early", 14*time.Minute, 74*time.Minute, "scheduled", false)
	late := mk("late", -5*time.Minute, 55*time.Minute, "scheduled", false)
	ended := mk("ended", -60*time.Minute, -1*time.Second, "live", false)
	endedNobody := mk("nobody", -61*time.Minute, -1*time.Minute, "scheduled", false)
	cancelled := mk("cancel", -5*time.Minute, 55*time.Minute, "cancelled", false)
	demo := mk("demo", -48*time.Hour, -47*time.Hour, "scheduled", true)

	check("до начала (за 30 мин)", before, want{false, true, 409, "откроется за 15 минут", 409})
	check("вход за 14 мин до начала", early, want{true, true, 503, noVideo, 101})
	check("опоздание на 5 минут", late, want{true, true, 503, noVideo, 101})
	check("окончание секунду назад", ended, want{false, false, 409, "Время занятия истекло", 409})
	check("окончание без входа", endedNobody, want{false, false, 409, "Время занятия истекло", 409})
	check("отменённый урок во время его интервала", cancelled, want{false, false, 409, "отменено", 409})
	check("постоянно открытый демо-урок", demo, want{true, true, 503, noVideo, 101})

	// Опоздание: окно входа заканчивается ровно в момент окончания, начало/конец/цена не меняются.
	d := detail(late.student, late.id)
	ls := d["lesson"].(map[string]any)
	closes, _ := time.Parse(time.RFC3339Nano, d["joinClosesAt"].(string))
	ends, _ := time.Parse(time.RFC3339Nano, ls["endsAt"].(string))
	opens, _ := time.Parse(time.RFC3339Nano, d["joinOpensAt"].(string))
	starts, _ := time.Parse(time.RFC3339Nano, ls["startsAt"].(string))
	if !closes.Equal(ends) || !ends.Equal(now.Add(55*time.Minute)) || !starts.Equal(now.Add(-5*time.Minute)) || ls["price"] != float64(6000) {
		t.Errorf("опоздание изменило урок: начало %v, конец %v, вход до %v, цена %v", starts, ends, closes, ls["price"])
	}
	if !opens.Equal(starts.Add(-15 * time.Minute)) {
		t.Errorf("вход открывается в %v, ожидалось за 15 минут до %v", opens, starts)
	}
	if left := time.Until(closes).Round(time.Minute); left != 55*time.Minute {
		t.Errorf("опоздавшему на 5 минут осталось %v, ожидалось 55 мин", left)
	}
	// Запуск кода в классе отменённого и закончившегося урока запрещён сервером
	for _, l := range []lesson{cancelled, ended} {
		if code, body := e.req(l.student, "POST", "/api/lessons/"+l.id+"/code/runs", map[string]any{"language": "python", "source": "print(1)"}); code != 409 {
			t.Errorf("запуск кода в закрытом классе: %d %v", code, body)
		}
	}

	// Фоновая задача: закончившиеся уроки закрываются без продления, идущие и будущие не трогаются.
	e.srv.tick(ctx)
	statuses := map[string]string{}
	rows, _ := e.pool.Query(ctx, `SELECT id::text, status, coalesce(cancel_reason,'') FROM lessons`)
	for rows.Next() {
		var id, st, reason string
		rows.Scan(&id, &st, &reason)
		statuses[id] = st + "|" + reason
	}
	rows.Close()
	for _, c := range []struct {
		l    lesson
		want string
	}{
		{ended, "completed|"}, {endedNobody, "cancelled|Занятие не состоялось"}, {late, "scheduled|"},
		{before, "scheduled|"}, {early, "scheduled|"}, {demo, "scheduled|"},
	} {
		if statuses[c.l.id] != c.want {
			t.Errorf("после фоновой задачи %s: %q, ожидалось %q", c.l.student, statuses[c.l.id], c.want)
		}
	}

	// Свободный интервал преподавателя — не занятие: добавление окна, уже начавшегося, урок не создаёт.
	var before1, after1 int
	tid := user("t_avail", "teacher", "Asia/Almaty")
	e.pool.QueryRow(ctx, `SELECT count(*) FROM lessons`).Scan(&before1)
	if code, body := e.req("t_avail", "POST", "/api/my/availability", map[string]any{
		"startsAt": now.Add(-30 * time.Minute).Format(time.RFC3339), "endsAt": now.Add(3 * time.Hour).Format(time.RFC3339)}); code != 201 {
		t.Fatalf("добавление интервала: %d %v", code, body)
	}
	e.pool.QueryRow(ctx, `SELECT count(*) FROM lessons`).Scan(&after1)
	if _, body := e.req("t_avail", "GET", "/api/lessons?scope=upcoming", nil); after1 != before1 || len(listIDs(body)) != 0 {
		t.Errorf("интервал создал занятие: было %d, стало %d, в списке преподавателя %v (%s)", before1, after1, listIDs(body), tid)
	}
}
