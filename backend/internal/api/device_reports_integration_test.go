package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Обращения «Не вижу урок / Не могу войти» с устройства: очистка данных, серверная оценка причины,
// отсутствие дублей, доступ к чужим урокам и разбор администратором.
func TestDeviceReports(t *testing.T) {
	d := setupDiag(t)
	d.user("admin", "admin", "Админ", "Asia/Almaty")
	d.user("arman", "teacher", "Арман", "Asia/Almaty")
	d.user("kairat", "student", "Kairat", "Asia/Almaty")
	d.user("other", "student", "Other", "Asia/Almaty")
	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)
	live := d.lesson("arman", "kairat", now.Add(-10*time.Minute), now.Add(50*time.Minute), "live", "")
	done := d.lesson("arman", "kairat", now.Add(-26*time.Hour), now.Add(-25*time.Hour), "completed", "")
	otherLesson := d.lesson("arman", "other", now.Add(2*time.Hour), now.Add(3*time.Hour), "scheduled", "")

	// Каждый ответ API несёт X-Request-ID; /api/config — версию приложения
	r, _ := http.Get(d.http.URL + "/api/config")
	if len(r.Header.Get(RequestIDHeader)) != 12 {
		t.Fatalf("X-Request-ID: %q", r.Header.Get(RequestIDHeader))
	}
	r.Body.Close()
	if _, b := d.req("", "GET", "/api/config", nil); b["version"] == nil {
		t.Fatal("версия в /api/config")
	}

	base := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"frontendVersion": "test-1", "backendVersion": "dev", "page": "/lessons/" + live + "?token=SECRET123#frag",
			"checkedAt": time.Now().UTC(), "clockSkewMs": 1500, "serverTimeReceived": true, "online": true, "outcome": "lesson_found",
			"probes": []any{
				map[string]any{"name": "session", "ok": true, "status": 200, "requestId": "a1b2c3d4e5f6", "ms": 12},
				map[string]any{"name": "lesson_check", "ok": true, "status": 200, "requestId": "<script>", "ms": 30},
				map[string]any{"name": "steal_cookies", "ok": true, "status": 200},
			},
			"password": "hunter2", "cookie": "ortalyq_session=abc", "body": "полное тело ответа",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	stored := func(code string) string {
		var s string
		d.pool.QueryRow(ctx, `SELECT row_to_json(r)::text FROM diagnostic_reports r WHERE code=$1`, code).Scan(&s)
		return s
	}

	// 1. Урок найден, всё работает — причина не установлена (честно), данные очищены
	c, body := d.req("kairat", "POST", "/api/me/diagnostics", base(map[string]any{"lessonId": live}))
	if c != 201 || body["status"] != "saved" {
		t.Fatalf("submit: %d %v", c, body)
	}
	cause := body["cause"].(map[string]any)
	if cause["established"] != false || !strings.Contains(cause["text"].(string), "не установлена") {
		t.Fatalf("причина: %v", cause)
	}
	code1 := body["code"].(string)
	row := stored(code1)
	for _, bad := range []string{"SECRET123", "frag", "hunter2", "ortalyq_session", "полное тело", "steal_cookies", "<script>"} {
		if strings.Contains(row, bad) {
			t.Fatalf("сохранено лишнее %q: %s", bad, row)
		}
	}
	if !strings.Contains(row, `"/lessons/`+live+`"`) || !strings.Contains(row, "a1b2c3d4e5f6") || !strings.Contains(row, live) {
		t.Fatalf("нужные сведения не сохранены: %s", row)
	}
	// Повтор — тот же номер, hits 2
	_, again := d.req("kairat", "POST", "/api/me/diagnostics", base(map[string]any{"lessonId": live}))
	if again["code"] != code1 || again["hits"].(float64) != 2 {
		t.Fatalf("повтор: %v", again)
	}

	// 2. Завершённый урок, ошибка сети и неверные часы — причины установлены
	_, body = d.req("kairat", "POST", "/api/me/diagnostics", base(map[string]any{"lessonId": done, "outcome": "load_failed", "clockSkewMs": -3 * 3600 * 1000,
		"probes": []any{map[string]any{"name": "lessons_upcoming", "ok": false, "status": 0, "errorCode": "network"}}}))
	cause = body["cause"].(map[string]any)
	codes := strings.Join(toStrings(cause["codes"]), ",")
	if cause["established"] != true || !strings.Contains(codes, "lesson_ended") || !strings.Contains(codes, "network_error") || !strings.Contains(codes, "clock_skew") {
		t.Fatalf("причины: %v", cause)
	}

	// 3. Чужой урок по ID не сохраняется и не раскрывается
	_, body = d.req("kairat", "POST", "/api/me/diagnostics", base(map[string]any{"lessonId": otherLesson, "outcome": "unknown"}))
	if strings.Contains(stored(body["code"].(string)), otherLesson) {
		t.Fatal("чужой урок попал в обращение")
	}
	_, rawResp := d.raw("kairat", "POST", "/api/me/diagnostics", base(map[string]any{"lessonId": otherLesson, "outcome": "no_lessons"}))
	if strings.Contains(rawResp, otherLesson) || strings.Contains(rawResp, "Other") {
		t.Fatal("ответ раскрывает чужой урок")
	}

	// 4. Без входа и администратору — нельзя
	if c, _ := d.req("", "POST", "/api/me/diagnostics", base(nil)); c != 401 {
		t.Fatalf("аноним: %d", c)
	}

	// 5. Администратор: список с данными браузера и сервера; другим — нет
	for _, who := range []string{"kairat", "arman"} {
		if c, _ := d.req(who, "GET", "/api/admin/diagnostics/reports", nil); c != 403 {
			t.Fatalf("%s: доступ к обращениям", who)
		}
	}
	_, list := d.req("admin", "GET", "/api/admin/diagnostics/reports?status=new", nil)
	var rep map[string]any
	for _, x := range list["reports"].([]any) {
		if x.(map[string]any)["code"] == code1 {
			rep = x.(map[string]any)
		}
	}
	if rep == nil || rep["user"].(map[string]any)["name"] != "Kairat" || rep["lesson"].(map[string]any)["id"] != live || rep["reviewStatus"] != "new" ||
		!strings.Contains(rep["appVersion"].(string), "test-1") || rep["client"].(map[string]any)["browser"] == nil {
		t.Fatalf("обращение у администратора: %v", rep)
	}
	if strings.Contains(rep["user"].(map[string]any)["emailMasked"].(string), "kairat@") {
		t.Fatal("email не замаскирован")
	}
	// «Решено» без подтверждённой причины — нельзя
	if c, _ := d.req("admin", "PATCH", "/api/admin/diagnostics/reports/"+code1, map[string]any{"reviewStatus": "resolved"}); c != 400 {
		t.Fatalf("решено без причины: %d", c)
	}
	if c, _ := d.req("admin", "PATCH", "/api/admin/diagnostics/reports/"+code1, map[string]any{"reviewStatus": "in_progress", "note": "смотрю"}); c != 200 {
		t.Fatal("в работе")
	}
	if c, _ := d.req("admin", "PATCH", "/api/admin/diagnostics/reports/"+code1, map[string]any{"reviewStatus": "resolved", "confirmedCause": "Ученик смотрел после окончания урока"}); c != 200 {
		t.Fatal("решено")
	}
	var hist, status, review string
	d.pool.QueryRow(ctx, `SELECT history::text, status, review_status FROM diagnostic_reports WHERE code=$1`, code1).Scan(&hist, &status, &review)
	if !strings.Contains(hist, "in_progress") || !strings.Contains(hist, "resolved") || status != "closed" || review != "resolved" {
		t.Fatalf("журнал/статус: %s %s %s", hist, status, review)
	}
	// После решения та же проблема — новое обращение
	_, body = d.req("kairat", "POST", "/api/me/diagnostics", base(map[string]any{"lessonId": live}))
	if body["code"] == code1 {
		t.Fatal("после решения нужно новое обращение")
	}
	// Данные занятий не изменились
	var st string
	d.pool.QueryRow(ctx, `SELECT status FROM lessons WHERE id=$1`, live).Scan(&st)
	if st != "live" {
		t.Fatalf("статус урока изменён: %s", st)
	}
}

func toStrings(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
