package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"ortalyq/backend/internal/auth"
)

// Встроенная диагностика: результат совпадает с фактической выдачей списков, входом в класс и бронированием.

type diagEnv struct {
	*tenv
	ids map[string]string
}

func setupDiag(t *testing.T) *diagEnv {
	e := setupCode(t)
	d := &diagEnv{tenv: e, ids: map[string]string{}}
	return d
}

func (d *diagEnv) user(key, role, name, tz string) string {
	ctx := context.Background()
	var id string
	if err := d.pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name, timezone) VALUES ($1,'x',$2,$3,$4) RETURNING id`,
		key+"@diag.test", role, name, tz).Scan(&id); err != nil {
		d.t.Fatal(err)
	}
	raw, hash, _ := auth.NewToken()
	d.pool.Exec(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1,$2, now() + interval '1 hour')`, hash, id)
	d.cookies[key] = raw
	d.ids[key] = id
	return id
}

func (d *diagEnv) lesson(teacher, student string, from, to time.Time, status, reason string) string {
	var id string
	if err := d.pool.QueryRow(context.Background(), `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, status, cancel_reason)
		VALUES ($1,$2,'Математика',$3,$4,$5,$6) RETURNING id`, d.ids[teacher], d.ids[student], from, to, status, reason).Scan(&id); err != nil {
		d.t.Fatal(err)
	}
	return id
}

func listHas(e *tenv, who, scope, id string) bool {
	_, raw := e.raw(who, "GET", "/api/lessons?scope="+scope, nil)
	return strings.Contains(raw, id)
}

func TestLessonCheckForParticipant(t *testing.T) {
	d := setupDiag(t)
	d.user("arman", "teacher", "Арман", "Asia/Almaty")
	d.user("kairat1", "student", "Kairat", "Asia/Almaty")
	d.user("kairat2", "student", "Kairat", "Europe/Moscow") // тот же отображаемый имя, другой аккаунт
	now := time.Now().Truncate(time.Minute)
	live := d.lesson("arman", "kairat1", now.Add(-20*time.Minute), now.Add(40*time.Minute), "live", "")
	done := d.lesson("arman", "kairat1", now.Add(-26*time.Hour), now.Add(-25*time.Hour), "completed", "")
	canc := d.lesson("arman", "kairat1", now.Add(-50*time.Hour), now.Add(-49*time.Hour), "cancelled", "Занятие не состоялось")
	stale := d.lesson("arman", "kairat1", now.Add(-5*time.Hour), now.Add(-4*time.Hour), "scheduled", "") // фоновая задача ещё не обработала

	code, body := d.req("kairat1", "POST", "/api/me/lesson-check", map[string]any{"teacherId": d.ids["arman"]})
	if code != 200 {
		t.Fatalf("check: %d %v", code, body)
	}
	got := map[string]map[string]any{}
	for _, x := range body["lessons"].([]any) {
		m := x.(map[string]any)
		got[m["id"].(string)] = m
	}
	if len(got) != 4 || body["reportCode"] != nil {
		t.Fatalf("ожидали 4 своих урока без обращения: %v", body)
	}
	// Совпадение с фактической выдачей списков
	for id, m := range got {
		want := "history"
		if listHas(d.tenv, "kairat1", "upcoming", id) {
			want = "upcoming"
		} else if !listHas(d.tenv, "kairat1", "history", id) {
			t.Fatalf("урок %s нет ни в одном списке", id)
		}
		if m["list"] != want {
			t.Fatalf("диагностика %s: %v, выдача: %s", id, m["list"], want)
		}
	}
	if got[live]["list"] != "upcoming" || got[live]["canEnter"] != true || !strings.Contains(got[live]["explanation"].(string), "идёт") {
		t.Fatalf("идущий урок: %v", got[live])
	}
	if !strings.Contains(got[done]["explanation"].(string), "завершён") || !strings.Contains(got[canc]["explanation"].(string), "Занятие не состоялось") {
		t.Fatalf("завершён/отменён: %v / %v", got[done], got[canc])
	}
	if got[stale]["list"] != "history" || got[stale]["canEnter"] != false || !strings.Contains(got[stale]["explanation"].(string), "закончилось") {
		t.Fatalf("прошедший со старым статусом: %v", got[stale])
	}
	// Вход в класс: диагностика совпадает с /join (без LiveKit разрешённый вход даёт 503, запрещённый — 409)
	for id, m := range got {
		c, _ := d.req("kairat1", "POST", "/api/lessons/"+id+"/join", nil)
		if (m["canEnter"] == true) != (c == 503) {
			t.Fatalf("вход %s: диагностика %v, /join %d", id, m["canEnter"], c)
		}
	}

	// Второй Kairat (тот же отображаемый имя) не видит уроки первого — ни списком, ни по номеру
	code, body = d.req("kairat2", "POST", "/api/me/lesson-check", map[string]any{"lessonId": live})
	if code != 200 || len(body["lessons"].([]any)) != 0 || body["reportCode"] == nil {
		t.Fatalf("чужой урок по номеру: %d %v", code, body)
	}
	codeA := body["reportCode"].(string)
	// Несуществующий номер — тот же вид ответа (существование чужого урока не раскрывается)
	_, body2 := d.req("kairat2", "POST", "/api/me/lesson-check", map[string]any{"lessonId": "00000000-0000-0000-0000-000000000000"})
	if len(body2["lessons"].([]any)) != 0 || body2["reportCode"] == nil {
		t.Fatalf("несуществующий: %v", body2)
	}
	// Повтор той же проверки — тот же номер обращения, новой записи нет
	_, again := d.req("kairat2", "POST", "/api/me/lesson-check", map[string]any{"lessonId": live})
	if again["reportCode"] != codeA {
		t.Fatalf("повтор должен вернуть тот же номер: %v != %v", again["reportCode"], codeA)
	}
	var n, hits int
	d.pool.QueryRow(context.Background(), `SELECT count(*), max(hits) FROM diagnostic_reports WHERE code=$1`, codeA).Scan(&n, &hits)
	if n != 1 || hits != 2 {
		t.Fatalf("обращение: count=%d hits=%d", n, hits)
	}
	// Некорректный номер (35 символов, как в обращении) — понятно и без ошибки сервера
	if c, b := d.req("kairat1", "POST", "/api/me/lesson-check", map[string]any{"lessonId": "e94083a8-6ecd-4582-b879-d2fcbf7f668"}); c != 200 || len(b["lessons"].([]any)) != 0 {
		t.Fatalf("некорректный номер: %d %v", c, b)
	}
	// Преподаватель видит свои уроки
	if _, b := d.req("arman", "POST", "/api/me/lesson-check", nil); len(b["lessons"].([]any)) != 4 {
		t.Fatalf("преподаватель: %v", b)
	}
	if c, _ := d.req("", "POST", "/api/me/lesson-check", nil); c != 401 {
		t.Fatal("аноним")
	}
}

func TestAdminLessonDiagnosis(t *testing.T) {
	d := setupDiag(t)
	d.user("admin", "admin", "Админ", "Asia/Almaty")
	d.user("arman", "teacher", "Арман", "Asia/Almaty")
	d.user("kairat1", "student", "Kairat", "Europe/Moscow")
	d.user("kairat2", "student", "Kairat", "Asia/Almaty")
	now := time.Now().Truncate(time.Minute)
	ctx := context.Background()
	// Урок 12:00–13:00 вне текущей доступности (интервал изменили после записи), статус не обновлён после окончания
	from := now.Add(-3 * time.Hour)
	id := d.lesson("arman", "kairat1", from, from.Add(time.Hour), "live", "")
	dupID := d.lesson("arman", "kairat1", from, from.Add(time.Hour), "cancelled", "")
	d.pool.Exec(ctx, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3)`, d.ids["arman"], from.Add(40*time.Minute), from.Add(150*time.Minute))

	for _, who := range []string{"", "kairat1", "arman"} {
		if c, _ := d.req(who, "POST", "/api/admin/diagnostics/lessons/"+id, nil); c != 401 && c != 403 {
			t.Fatalf("%q: доступ к диагностике администратора: %d", who, c)
		}
	}
	if c, b := d.req("admin", "POST", "/api/admin/diagnostics/lessons/e94083a8-6ecd-4582-b879-d2fcbf7f668", nil); c != 400 || !strings.Contains(b["error"].(string), "36") {
		t.Fatalf("некорректный UUID: %d %v", c, b)
	}
	code, body := d.req("admin", "POST", "/api/admin/diagnostics/lessons/"+id, nil)
	if code != 200 {
		t.Fatalf("report: %d %v", code, body)
	}
	r := body["report"].(map[string]any)
	codes := map[string]map[string]any{}
	for _, f := range r["findings"].([]any) {
		m := f.(map[string]any)
		codes[m["code"].(string)] = m
	}
	for _, c := range []string{"outside_availability", "stale_status", "duplicate", "namesake_student"} {
		if codes[c] == nil {
			t.Fatalf("нет вывода %s: %v", c, r["findings"])
		}
	}
	if codes["list_rule_mismatch"] != nil || codes["overlap"] != nil {
		t.Fatalf("лишние выводы: %v", r["findings"])
	}
	if ids := codes["duplicate"]["relatedIds"].([]any); len(ids) != 1 || ids[0] != dupID {
		t.Fatalf("дубль: %v", ids)
	}
	if ids := codes["namesake_student"]["relatedIds"].([]any); len(ids) != 1 || ids[0] != d.ids["kairat2"] {
		t.Fatalf("тёзки: %v", ids)
	}
	// Участники: пояса профиля, списки совпадают с фактической выдачей, email замаскирован, секретов нет
	_, raw := d.raw("admin", "POST", "/api/admin/diagnostics/lessons/"+id, nil)
	for _, bad := range []string{"password", "token", "kairat1@diag.test"} {
		if strings.Contains(raw, bad) {
			t.Fatalf("в отчёте %q", bad)
		}
	}
	for _, p := range r["participants"].([]any) {
		m := p.(map[string]any)
		who := map[string]string{"teacher": "arman", "student": "kairat1"}[m["role"].(string)]
		lists := m["lists"].(map[string]any)
		if lists["upcoming"] != listHas(d.tenv, who, "upcoming", id) || lists["history"] != listHas(d.tenv, who, "history", id) {
			t.Fatalf("%s: списки диагностики %v не совпадают с выдачей", who, lists)
		}
		if m["role"] == "student" && !strings.Contains(m["startsLocal"].(string), "Europe/Moscow") {
			t.Fatalf("пояс ученика: %v", m["startsLocal"])
		}
	}
	// Все моменты отчёта — в UTC (суффикс Z), а не в поясе сервера
	for _, k := range []string{"startsAtUtc", "endsAtUtc", "createdAt", "serverTime"} {
		if v, _ := r[k].(string); !strings.HasSuffix(v, "Z") {
			t.Fatalf("%s не в UTC: %q", k, v)
		}
	}
	if r["entry"].(map[string]any)["allowed"] != false {
		t.Fatal("вход после окончания закрыт")
	}
	// Обращение без дублей: повтор — тот же номер
	rc := r["reportCode"].(string)
	_, body = d.req("admin", "POST", "/api/admin/diagnostics/lessons/"+id, nil)
	if body["report"].(map[string]any)["reportCode"] != rc {
		t.Fatal("повторная диагностика должна вернуть тот же номер")
	}
	var n int
	d.pool.QueryRow(ctx, `SELECT count(*) FROM diagnostic_reports WHERE lesson_id=$1`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("обращений по уроку: %d", n)
	}
	// Диагностика ничего не меняет в занятиях
	var status string
	d.pool.QueryRow(ctx, `SELECT status FROM lessons WHERE id=$1`, id).Scan(&status)
	if status != "live" {
		t.Fatalf("статус изменён диагностикой: %s", status)
	}
	if c, _ := d.req("admin", "GET", "/api/admin/diagnostics/reports", nil); c != 200 {
		t.Fatal("список обращений")
	}
}

// Диагностика свободного времени совпадает с фактическим бронированием.
func TestSlotsDiagnosisMatchesBooking(t *testing.T) {
	d := setupDiag(t)
	d.user("arman", "teacher", "Арман", "Asia/Almaty")
	d.user("kairat", "student", "Kairat", "Asia/Almaty")
	d.user("elnur", "student", "Elnur", "Asia/Almaty")
	ctx := context.Background()
	d.pool.Exec(ctx, `INSERT INTO teacher_profiles(user_id, subjects, price) VALUES ($1, '{Математика}', 5000)`, d.ids["arman"])
	b := time.Now().Truncate(time.Minute).Add(2 * time.Hour)
	d.pool.Exec(ctx, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3)`, d.ids["arman"], b, b.Add(150*time.Minute))
	d.lesson("arman", "elnur", b, b.Add(60*time.Minute), "scheduled", "")
	d.lesson("arman", "elnur", b.Add(60*time.Minute), b.Add(120*time.Minute), "scheduled", "")

	_, body := d.req("kairat", "GET", "/api/teachers/"+d.ids["arman"]+"/slots?duration=60", nil)
	if body["emptyReason"] != slotsTooShort || body["maxFreeMinutes"].(float64) != 30 {
		t.Fatalf("диагностика: %v", body)
	}
	book := func(dur int) int {
		c, _ := d.req("kairat", "POST", "/api/lessons", map[string]any{"teacherId": d.ids["arman"], "startsAt": b.Add(120 * time.Minute).UTC().Format(time.RFC3339), "duration": dur, "subject": "Математика"})
		return c
	}
	if c := book(60); c != 409 {
		t.Fatalf("60 мин в окно 30 мин должно отклоняться: %d", c)
	}
	if c := book(30); c != 201 {
		t.Fatalf("30 мин помещаются: %d", c)
	}
	// Теперь всё занято — диагностика booked, запись невозможна
	_, body = d.req("kairat", "GET", "/api/teachers/"+d.ids["arman"]+"/slots?duration=30", nil)
	if body["emptyReason"] != slotsBooked {
		t.Fatalf("после записи: %v", body)
	}
	// Своя запись видна ученику, чужие — нет
	mine := body["myLessonsWithTeacher"].([]any)
	if len(mine) != 1 {
		t.Fatalf("своя запись: %v", mine)
	}
	_, raw := d.raw("kairat", "GET", "/api/teachers/"+d.ids["arman"]+"/slots?duration=30", nil)
	if strings.Contains(raw, "Elnur") || strings.Contains(raw, d.ids["elnur"]) {
		t.Fatal("сведения о чужих занятиях")
	}
}
