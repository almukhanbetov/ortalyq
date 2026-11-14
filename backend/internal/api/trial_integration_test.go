package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Бесплатный ознакомительный урок: начинается в любое время, ученик — по приглашению, тот же класс,
// завершение кнопкой, материалы остаются, расписание и обычные уроки не затрагиваются.

func inviteCode(t *testing.T, detail map[string]any) string {
	t.Helper()
	inv, _ := detail["invite"].(map[string]any)
	path, _ := inv["path"].(string)
	i := strings.Index(path, "#")
	if i < 0 {
		t.Fatalf("нет приглашения: %v", detail["invite"])
	}
	return path[i+1:]
}

func TestTrialLessonFlow(t *testing.T) {
	e := setupCode(t)
	ctx := context.Background()

	// 1) У преподавателя идёт обычный урок A — предупреждение и id идущего урока, новый не создаётся.
	code, r := e.req("teacher", "POST", "/api/lessons/trial", nil)
	if code != 409 || r["lessonId"] != e.lessonA || r["isTrial"] != false {
		t.Fatalf("идёт обычный урок: %d %v", code, r)
	}
	if code, _ := e.req("student", "POST", "/api/lessons/trial", nil); code != 403 {
		t.Fatalf("ученик не начинает ознакомительный урок: %d", code)
	}
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/end", nil); code != 200 {
		t.Fatal("завершение урока A")
	}

	// 2) Начать в любой момент: урок сразу идёт, бесплатный, без расписания.
	code, r = e.req("teacher", "POST", "/api/lessons/trial", nil)
	if code != 201 {
		t.Fatalf("создание: %d %v", code, r)
	}
	id := r["id"].(string)
	code, d := e.req("teacher", "GET", "/api/lessons/"+id, nil)
	l := d["lesson"].(map[string]any)
	if code != 200 || l["isTrial"] != true || l["price"].(float64) != 0 || l["status"] != "live" || l["startedAt"] == nil || d["canJoin"] != true || l["studentId"] != "" {
		t.Fatalf("урок: %d %v", code, d)
	}
	invite := inviteCode(t, d)

	// 3) Повторное нажатие — тот же урок, без дубликата.
	code, r = e.req("teacher", "POST", "/api/lessons/trial", nil)
	if code != 409 || r["lessonId"] != id || r["isTrial"] != true {
		t.Fatalf("повторное нажатие: %d %v", code, r)
	}

	// 4) Посторонние: без кода и с чужим кодом — 404; урок и класс им недоступны.
	if code, _ := e.req("", "POST", "/api/trial/"+id+"/invite", map[string]any{"code": strings.Repeat("0", 32)}); code != 404 {
		t.Fatalf("неверный код: %d", code)
	}
	if code, _ := e.req("outsider", "GET", "/api/lessons/"+id, nil); code != 404 {
		t.Fatalf("посторонний видит урок: %d", code)
	}
	if _, resp, err := e.ws("outsider", id); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("посторонний вошёл в класс: %v", err)
	}
	// Сведения о приглашении — без входа, по коду
	if code, r := e.req("", "POST", "/api/trial/"+id+"/invite", map[string]any{"code": invite}); code != 200 || r["state"] != "open" || r["teacherName"] != "Тест teacher" {
		t.Fatalf("сведения о приглашении: %d %v", code, r)
	}
	if code, _ := e.req("", "POST", "/api/trial/"+id+"/accept", map[string]any{"code": invite}); code != 401 {
		t.Fatalf("без входа присоединиться нельзя: %d", code)
	}
	if code, r := e.req("teacher", "POST", "/api/trial/"+id+"/accept", map[string]any{"code": invite}); code != 409 || !strings.Contains(r["error"].(string), "ваш урок") {
		t.Fatalf("преподаватель по своей ссылке: %d %v", code, r)
	}

	// 5) Ученик присоединяется (повторно — без изменений); третий получает отказ.
	tws := e.mustWS("teacher", id)
	if code, r := e.req("student", "POST", "/api/trial/"+id+"/accept", map[string]any{"code": invite}); code != 200 || r["lessonId"] != id {
		t.Fatalf("приём: %d %v", code, r)
	}
	next(t, tws, "lesson.participants", nil) // класс преподавателя обновит сведения об ученике
	if code, _ := e.req("student", "POST", "/api/trial/"+id+"/accept", map[string]any{"code": invite}); code != 200 {
		t.Fatal("повторный приём тем же учеником")
	}
	if code, r := e.req("outsider", "POST", "/api/trial/"+id+"/accept", map[string]any{"code": invite}); code != 409 || !strings.Contains(r["error"].(string), "другой ученик") {
		t.Fatalf("третий участник: %d %v", code, r)
	}
	if code, _ := e.req("outsider", "GET", "/api/lessons/"+id, nil); code != 404 {
		t.Fatal("третий получил доступ к уроку")
	}
	code, d = e.req("student", "GET", "/api/lessons/"+id, nil)
	if code != 200 || d["myRole"] != "student" || d["invite"] != nil {
		t.Fatalf("урок у ученика: %d %v", code, d)
	}
	if _, d := e.req("teacher", "GET", "/api/lessons/"+id, nil); d["invite"] != nil || d["lesson"].(map[string]any)["studentName"] != "Тест student" {
		t.Fatalf("после приёма приглашение не нужно: %v", d)
	}

	// 6) Тот же класс: чат между двумя сессиями, «перезагрузка» — новое подключение того же пользователя.
	sws := e.mustWS("student", id)
	wsjson.Write(ctx, sws, map[string]any{"type": "chat.send", "body": "Здравствуйте!"})
	next(t, tws, "chat.message", nil)
	sws.Close(websocket.StatusNormalClosure, "reload")
	sws = e.mustWS("student", id)
	// Видеосвязь — обычный путь урока (LiveKit в тесте не настроен — 503, а не отказ в доступе)
	if code, _ := e.req("student", "POST", "/api/lessons/"+id+"/join", nil); code != 503 {
		t.Fatalf("join: %d", code)
	}

	// 7) Завершение кнопкой: вход закрыт, фактические начало и окончание сохранены, материалы доступны.
	time.Sleep(1100 * time.Millisecond)
	if code, _ := e.req("student", "POST", "/api/lessons/"+id+"/end", nil); code != 403 {
		t.Fatal("ученик не завершает урок")
	}
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+id+"/end", nil); code != 200 {
		t.Fatal("завершение")
	}
	var started, ended, starts, ends time.Time
	var status string
	e.pool.QueryRow(ctx, `SELECT status, started_at, ended_at, starts_at, ends_at FROM lessons WHERE id=$1`, id).Scan(&status, &started, &ended, &starts, &ends)
	if status != "completed" || !ended.After(started) || !ends.Equal(ended) || !starts.Equal(started) {
		t.Fatalf("история: %s начат %v окончен %v интервал %v–%v", status, started, ended, starts, ends)
	}
	if _, resp, err := e.ws("student", id); err == nil || resp == nil || resp.StatusCode != 409 {
		t.Fatalf("вход после завершения: %v", err)
	}
	if code, _ := e.req("student", "POST", "/api/lessons/"+id+"/join", nil); code != 409 {
		t.Fatal("видео после завершения")
	}
	if code, r := e.req("student", "GET", "/api/lessons/"+id+"/chat", nil); code != 200 || len(r["messages"].([]any)) != 1 {
		t.Fatalf("чат сохранён: %d %v", code, r)
	}
	if code, _ := e.req("outsider", "POST", "/api/trial/"+id+"/accept", map[string]any{"code": invite}); code != 410 {
		t.Fatalf("приглашение после завершения: %d", code)
	}
	code, r = e.req("student", "GET", "/api/lessons?scope=history", nil)
	if code != 200 || !strings.Contains(toJSON(r), `"id":"`+id+`"`) {
		t.Fatalf("в истории ученика: %v", r)
	}
	sws.CloseNow()

	// 8) После завершения можно начать новый ознакомительный урок.
	if code, _ := e.req("teacher", "POST", "/api/lessons/trial", nil); code != 201 {
		t.Fatalf("новый урок после завершения: %d", code)
	}
}

// Одновременные нажатия создают ровно один урок.
func TestTrialLessonNoDuplicates(t *testing.T) {
	e := setupCode(t)
	e.pool.Exec(context.Background(), `UPDATE lessons SET status='completed' WHERE id=$1`, e.lessonA)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	ids := make([]string, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, r := e.req("teacher", "POST", "/api/lessons/trial", nil)
			codes[i] = c
			if v, ok := r["id"].(string); ok {
				ids[i] = v
			} else if v, ok := r["lessonId"].(string); ok {
				ids[i] = v
			}
		}(i)
	}
	wg.Wait()
	created := 0
	for i, c := range codes {
		if c == 201 {
			created++
		} else if c != 409 {
			t.Fatalf("ответ %d: %d", i, c)
		}
		if ids[i] != ids[0] {
			t.Fatalf("разные уроки: %v", ids)
		}
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM lessons WHERE is_trial`).Scan(&n)
	if created != 1 || n != 1 {
		t.Fatalf("создано %d, в БД %d", created, n)
	}
}

// Ознакомительный урок не занимает интервалы: запись на платное время, свободные слоты и завершение урока,
// который по факту пересёкся с обычным, работают как раньше. Цена, расписание и обычные уроки не меняются.
func TestTrialLessonDoesNotBlockSchedule(t *testing.T) {
	b := setupBooking(t)
	ctx := context.Background()
	t1 := b.ids["t1"]
	snap := func() string {
		var s string
		b.pool.QueryRow(ctx, `SELECT coalesce(string_agg(x, '|' ORDER BY x), '') FROM (
			SELECT 'a'||id||starts_at||ends_at AS x FROM availability
			UNION ALL SELECT 'p'||user_id||price FROM teacher_profiles
			UNION ALL SELECT 'l'||id||status||starts_at||ends_at||price FROM lessons WHERE NOT is_trial) q`).Scan(&s)
		return s
	}
	before := snap()

	if code, r := b.req("t1", "POST", "/api/lessons/trial", nil); code != 201 {
		t.Fatalf("создание: %d %v", code, r)
	}
	var trial string
	b.pool.QueryRow(ctx, `SELECT id FROM lessons WHERE is_trial AND teacher_id=$1`, t1).Scan(&trial)
	// Долгий ознакомительный урок «накрывает» окно доступности — оно всё равно свободно для записи.
	b.pool.Exec(ctx, `UPDATE lessons SET ends_at=$2 WHERE id=$1`, trial, at(b.day, "14:00"))
	code, body := b.req("a", "GET", "/api/teachers/"+t1+"/slots?duration=60", nil)
	if code != 200 || !strings.Contains(strings.ReplaceAll(toJSON(body), " ", ""), at(b.day, "10:00").UTC().Format("2006-01-02T15:04")) {
		t.Fatalf("слоты: %d %v", code, body)
	}
	if code, msg := b.book("a", t1, at(b.day, "10:00"), 60); code != 201 {
		t.Fatalf("запись при идущем ознакомительном уроке: %d %s", code, msg)
	}
	// Обычный урок, начавшийся во время ознакомительного (записан заранее): завершение ознакомительного
	// записывает фактическое окончание без конфликта с ограничениями пересечений.
	b.pool.Exec(ctx, `UPDATE lessons SET ends_at=starts_at + interval '1 second' WHERE id=$1`, trial)
	var reg string
	if err := b.pool.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, price)
		VALUES ($1,$2,'Математика', now() + interval '200 milliseconds', now() + interval '1 hour', 6000) RETURNING id`, t1, b.ids["b"]).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if code, r := b.req("t1", "POST", "/api/lessons/"+trial+"/end", nil); code != 200 {
		t.Fatalf("завершение: %d %v", code, r)
	}
	b.pool.Exec(ctx, `DELETE FROM lessons WHERE id=$1`, reg)
	b.pool.Exec(ctx, `DELETE FROM lessons WHERE teacher_id=$1 AND student_id=$2 AND starts_at=$3`, t1, b.ids["a"], at(b.day, "10:00"))
	if after := snap(); after != before {
		t.Fatal("ознакомительный урок изменил расписание, цены или обычные уроки")
	}
}

func (e *tenv) mustWS(who, lesson string) *websocket.Conn {
	e.t.Helper()
	c, resp, err := e.ws(who, lesson)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		e.t.Fatalf("WS %s: %v (%d)", who, err, code)
	}
	e.t.Cleanup(func() { c.CloseNow() })
	next(e.t, c, "hello", nil)
	return c
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
