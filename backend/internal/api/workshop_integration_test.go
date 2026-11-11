package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"ortalyq/backend/internal/auth"
	"ortalyq/backend/internal/workshop"
)

// Интеграционные тесты «Учебной мастерской» (HTTP + WebSocket + отдельная PostgreSQL из TEST_DATABASE_URL).
// Провайдер ИИ подменён (fakeAI): реальный платный API не вызывается. fakeAI не изображает ИИ в приложении —
// он есть только в тестах и проверяет маршрут бэкенда: права, лимиты, сохранение, публикацию и скрытие ответов.

const (
	draftOnlyMarker  = "ЧЕРНОВИК-ТОЛЬКО-ДЛЯ-ПРЕПОДАВАТЕЛЯ"
	answerMarker     = "ОТВЕТ-ДЛЯ-ПРЕПОДАВАТЕЛЯ"
	tipsMarker       = "СОВЕТ-ПО-ПРОВЕРКЕ"
	publishedMarker  = "ОПУБЛИКОВАННЫЙ-РАЗБОР"
	hwReviewMarker   = "ПОВТОРЕНИЕ-ТЕМЫ"
	hwExerciseMarker = "УПРАЖНЕНИЕ"
)

type fakeAI struct {
	mu    sync.Mutex
	delay time.Duration
	err   error
	text  string // ответ «как есть» (для проверки некорректного ответа)
	calls atomic.Int32
	last  workshop.Request
}

func (f *fakeAI) Generate(ctx context.Context, r workshop.Request) (workshop.Response, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.last = r
	delay, err, text := f.delay, f.err, f.text
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return workshop.Response{}, &workshop.ProviderError{Kind: workshop.ErrTimeout}
		case <-time.After(delay):
		}
	}
	if err != nil {
		return workshop.Response{}, err
	}
	if text != "" {
		return workshop.Response{Text: text, Model: "fake"}, nil
	}
	if r.SchemaName == "workshop_homework" {
		ex := `{"title":"№","instruction":"` + hwExerciseMarker + `: выполните","content":"задание"}`
		return workshop.Response{Model: "fake", Text: `{"review":"` + hwReviewMarker + `","exercises":[` + strings.Repeat(ex+",", 4) + ex +
			`],"answers":["` + answerMarker + `1","` + answerMarker + `2","` + answerMarker + `3","` + answerMarker + `4","` + answerMarker + `5"],"checking_tips":"` + tipsMarker + `"}`}, nil
	}
	verdict := "none"
	if strings.Contains(r.Instructions, "insufficient_data — данных") {
		verdict = "insufficient_data"
	}
	return workshop.Response{Model: "fake", Text: `{"verdict":"` + verdict + `","sections":[{"heading":"Раздел","body":"` + draftOnlyMarker + `"}]}`}, nil
}

func (f *fakeAI) set(fn func(f *fakeAI)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

// raw — запрос с телом ответа «как есть» (для проверки отсутствия скрытых данных в ответах ученику).
func (e *tenv) raw(who, method, path string, body any) (int, string) {
	var rd io.Reader = bytes.NewReader(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, e.http.URL+path, rd)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://test.local")
	if who != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.cookies[who]})
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// collect читает все сообщения сокета в течение d (сырые JSON-строки).
func collect(c *websocket.Conn, d time.Duration) []string {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var out []string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return out
		}
		out = append(out, string(data))
	}
}

// expectAll читает сообщения, пока не встретятся все типы (порядок между ними не важен).
func expectAll(t *testing.T, c *websocket.Conn, types ...string) map[string]map[string]any {
	t.Helper()
	got := map[string]map[string]any{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for len(got) < len(types) {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("ждали %v, получили %v: %v", types, got, err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		for _, ty := range types {
			if m["type"] == ty {
				got[ty] = m
			}
		}
	}
	return got
}

func assertNoSecrets(t *testing.T, where, s string) {
	t.Helper()
	for _, m := range []string{draftOnlyMarker, answerMarker, tipsMarker, "answerKey", `"draft"`, "checkingTips", "sourceText"} {
		if strings.Contains(s, m) {
			t.Fatalf("%s: ученику передано скрытое содержимое %q: %s", where, m, s)
		}
	}
}

func lessonPath(id, p string) string { return "/api/lessons/" + id + "/workshop" + p }

func TestWorkshopAccessControl(t *testing.T) {
	e := setupCode(t)
	ai := &fakeAI{}
	e.srv.SetAIProvider(ai)
	A := e.lessonA
	analysis := map[string]any{"action": "explain", "level": "standard", "fragment": "текст"}

	if code, _ := e.req("", "GET", lessonPath(A, ""), nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	// Посторонний: 404 на всё (существование урока не раскрывается)
	for _, rq := range []struct{ m, p string }{{"GET", ""}, {"GET", "/doc"}, {"PUT", "/mode"}, {"POST", "/ai-notice"}, {"POST", "/analyses"}, {"POST", "/homework"}} {
		if code, _ := e.req("outsider", rq.m, lessonPath(A, rq.p), analysis); code != 404 {
			t.Fatalf("outsider %s %s: %d", rq.m, rq.p, code)
		}
	}
	// Ученик урока не выполняет действия преподавателя
	for _, rq := range []struct {
		m, p string
		body any
	}{
		{"PUT", "/mode", map[string]any{"mode": "english"}},
		{"POST", "/ai-notice", nil},
		{"POST", "/analyses", analysis},
		{"POST", "/homework", map[string]any{"text": "т", "level": "standard"}},
	} {
		if code, _ := e.req("student", rq.m, lessonPath(A, rq.p), rq.body); code != 403 {
			t.Fatalf("student %s %s: %d", rq.m, rq.p, code)
		}
	}
	if ai.calls.Load() != 0 {
		t.Fatal("отклонённые запросы не должны обращаться к ИИ")
	}
	// Преподаватель урока A — не преподаватель «вообще»: урок B другой пары недоступен ученику A
	if code, _ := e.req("student", "GET", lessonPath(e.lessonB, ""), nil); code != 404 {
		t.Fatalf("student → lesson B: %d", code)
	}
	// Состояние для ученика — без сведений о настройке ИИ
	code, body := e.req("student", "GET", lessonPath(A, ""), nil)
	if code != 200 || body["role"] != "student" || body["ai"] != nil || body["mode"] != "general" {
		t.Fatalf("student state: %d %v", code, body)
	}
	code, body = e.req("teacher", "GET", lessonPath(A, ""), nil)
	if code != 200 || body["ai"].(map[string]any)["configured"] != true || body["editable"] != true {
		t.Fatalf("teacher state: %d %v", code, body)
	}
}

func TestWorkshopDocSync(t *testing.T) {
	e := setupCode(t)
	teacher, _, err := e.ws("teacher", e.lessonA)
	if err != nil {
		t.Fatal(err)
	}
	defer teacher.CloseNow()
	student, _, err := e.ws("student", e.lessonA)
	if err != nil {
		t.Fatal(err)
	}
	defer student.CloseNow()
	next(t, teacher, "hello", nil)
	next(t, student, "hello", nil)

	send(t, teacher, map[string]any{"type": "workshop.sync"})
	if m := next(t, teacher, "workshop.snapshot", nil); len(m["updates"].([]any)) != 0 || m["readOnly"] != false {
		t.Fatalf("новый документ пуст и доступен для правки: %v", m)
	}
	// Одновременные правки двух участников: оба обновления приняты, разосланы и сохранены (сервер их не сливает и не теряет).
	u1 := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	u2 := base64.StdEncoding.EncodeToString([]byte{4, 5})
	send(t, teacher, map[string]any{"type": "workshop.update", "update": u1, "cid": "t1"})
	send(t, student, map[string]any{"type": "workshop.update", "update": u2, "cid": "s1"})
	tm, sm := expectAll(t, teacher, "workshop.ack", "workshop.update"), expectAll(t, student, "workshop.ack", "workshop.update")
	if sm["workshop.update"]["update"] != u1 || tm["workshop.update"]["update"] != u2 {
		t.Fatalf("relay: %v / %v", tm, sm)
	}
	if tm["workshop.ack"]["seq"] == sm["workshop.ack"]["seq"] {
		t.Fatal("каждое обновление получает свой номер")
	}
	// Документ мастерской отделён от редактора кода
	send(t, teacher, map[string]any{"type": "code.sync"})
	if m := next(t, teacher, "code.snapshot", nil); len(m["updates"].([]any)) != 0 {
		t.Fatalf("учебный текст не должен попадать в документ кода: %v", m)
	}
	// Просмотр по HTTP (страница урока) — из памяти активной комнаты
	if code, body := e.req("student", "GET", lessonPath(e.lessonA, "/doc"), nil); code != 200 || len(body["updates"].([]any)) != 2 {
		t.Fatalf("doc: %d %v", code, body)
	}

	// Все вышли → сохранено в отдельную таблицу; после «перезагрузки» журнал восстанавливается
	teacher.Close(websocket.StatusNormalClosure, "")
	student.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	var seq int64
	for time.Now().Before(deadline) && seq != 2 {
		e.pool.QueryRow(context.Background(), `SELECT seq FROM workshop_docs WHERE lesson_id=$1`, e.lessonA).Scan(&seq)
		time.Sleep(100 * time.Millisecond)
	}
	if seq != 2 {
		t.Fatalf("persisted seq = %d", seq)
	}
	var codeDocs int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM code_docs WHERE lesson_id=$1`, e.lessonA).Scan(&codeDocs)
	if codeDocs != 0 {
		t.Fatal("документ кода не должен создаваться правками учебного текста")
	}
	time.Sleep(200 * time.Millisecond)
	again, _, _ := e.ws("teacher", e.lessonA)
	defer again.CloseNow()
	next(t, again, "hello", nil)
	send(t, again, map[string]any{"type": "workshop.sync"})
	if m := next(t, again, "workshop.snapshot", nil); len(m["updates"].([]any)) != 2 || m["seq"].(float64) != 2 {
		t.Fatalf("restored: %v", m)
	}
	if code, body := e.req("teacher", "GET", lessonPath(e.lessonA, "/doc"), nil); code != 200 || body["seq"].(float64) != 2 {
		t.Fatalf("doc after reload: %v", body)
	}
	// Урок B: свой пустой документ
	other, _, _ := e.ws("outsider", e.lessonB)
	defer other.CloseNow()
	next(t, other, "hello", nil)
	send(t, other, map[string]any{"type": "workshop.sync"})
	if m := next(t, other, "workshop.snapshot", nil); len(m["updates"].([]any)) != 0 {
		t.Fatalf("урок B не должен видеть текст урока A: %v", m)
	}
	if code, _ := e.req("outsider", "GET", lessonPath(e.lessonA, "/doc"), nil); code != 404 {
		t.Fatal("посторонний не читает текст")
	}

	// Завершение урока: правки учебного текста отклоняются, новые подключения закрыты, текст сохранён
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/end", nil); code != 200 {
		t.Fatal("end lesson")
	}
	next(t, again, "lesson.status", nil)
	send(t, again, map[string]any{"type": "workshop.update", "update": base64.StdEncoding.EncodeToString([]byte{7}), "cid": "late"})
	if m := next(t, again, "workshop.reject", nil); m["locked"] != true || m["cid"] != "late" {
		t.Fatalf("после окончания правки запрещены: %v", m)
	}
	if _, resp, err := e.ws("student", e.lessonA); err == nil || resp.StatusCode != 409 {
		t.Fatal("после окончания урока вход в класс закрыт")
	}
	if code, body := e.req("student", "GET", lessonPath(e.lessonA, "/doc"), nil); code != 200 || len(body["updates"].([]any)) != 2 {
		t.Fatalf("после урока текст доступен участникам для просмотра: %v", body)
	}
}

// Окно редактирования истекает по времени урока, даже если соединение открыто и фоновая задача ещё не завершила урок.
func TestWorkshopEditWindow(t *testing.T) {
	e := setupCode(t)
	ctx := context.Background()
	var tID, sID string
	e.pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name) VALUES ('t2@test.local','x','teacher','T2') RETURNING id`).Scan(&tID)
	e.pool.QueryRow(ctx, `SELECT student_id FROM lessons WHERE id=$1`, e.lessonA).Scan(&sID)
	raw, hash, _ := auth.NewToken()
	e.pool.Exec(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1,$2, now() + interval '1 hour')`, hash, tID)
	e.cookies["t2"] = raw
	var lid string
	// Урок outsider с другим преподавателем, заканчивается через 1,5 с (у outsider урок B начинается позже)
	if err := e.pool.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, status)
		SELECT $1, student_id, 'Английский язык', now() - interval '30 minutes', now() + interval '1500 milliseconds', 'live' FROM lessons WHERE id=$2 RETURNING id`,
		tID, e.lessonB).Scan(&lid); err != nil {
		t.Fatal(err)
	}
	c, _, err := e.ws("t2", lid)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	next(t, c, "hello", nil)
	send(t, c, map[string]any{"type": "workshop.update", "update": base64.StdEncoding.EncodeToString([]byte{1}), "cid": "a"})
	next(t, c, "workshop.ack", nil)
	time.Sleep(1700 * time.Millisecond)
	send(t, c, map[string]any{"type": "workshop.update", "update": base64.StdEncoding.EncodeToString([]byte{2}), "cid": "b"})
	if m := next(t, c, "workshop.reject", nil); m["locked"] != true {
		t.Fatalf("после окончания по расписанию правки запрещены: %v", m)
	}
	// И запрос к ИИ тоже
	e.srv.SetAIProvider(&fakeAI{})
	e.req("t2", "POST", lessonPath(lid, "/ai-notice"), nil)
	if code, _ := e.req("t2", "POST", lessonPath(lid, "/analyses"), map[string]any{"action": "explain", "level": "standard", "fragment": "x"}); code != 409 {
		t.Fatalf("ИИ после окончания: %d", code)
	}
}

func TestWorkshopAIFlow(t *testing.T) {
	e := setupCode(t)
	ai := &fakeAI{}
	e.srv.SetAIProvider(ai)
	A := e.lessonA
	student, _, err := e.ws("student", A)
	if err != nil {
		t.Fatal(err)
	}
	defer student.CloseNow()
	next(t, student, "hello", nil)
	teacherWS, _, _ := e.ws("teacher", A)
	defer teacherWS.CloseNow()
	next(t, teacherWS, "hello", nil)

	req := map[string]any{"action": "explain", "level": "easier", "fragment": "Photosynthesis converts light"}
	// Без подтверждения передачи текста внешнему ИИ запрос не выполняется
	if code, body := e.req("teacher", "POST", lessonPath(A, "/analyses"), req); code != 428 || body["code"] != "notice_required" {
		t.Fatalf("notice: %d %v", code, body)
	}
	if ai.calls.Load() != 0 {
		t.Fatal("ИИ не должен вызываться без подтверждения")
	}
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/ai-notice"), nil); code != 200 {
		t.Fatal("ack")
	}
	// Проверки ввода — до обращения к ИИ
	for _, bad := range []map[string]any{
		{"action": "explain", "level": "standard", "fragment": "   "},
		{"action": "explain", "level": "standard", "fragment": strings.Repeat("я", workshop.MaxFragmentRunes+1)},
		{"action": "explain", "level": "nope", "fragment": "x"},
		{"action": "correct", "level": "standard", "fragment": "x"}, // нет в общем режиме
	} {
		if code, _ := e.req("teacher", "POST", lessonPath(A, "/analyses"), bad); code != 400 {
			t.Fatalf("bad input %v: %d", bad["action"], code)
		}
	}
	if ai.calls.Load() != 0 {
		t.Fatal("некорректный ввод не должен отправляться ИИ")
	}

	code, body := e.req("teacher", "POST", lessonPath(A, "/analyses"), req)
	if code != 201 {
		t.Fatalf("create: %d %v", code, body)
	}
	an := body["analysis"].(map[string]any)
	aid := an["id"].(string)
	if an["fragment"] != "Photosynthesis converts light" || an["action"] != "explain" || an["published"] != nil ||
		!strings.Contains(an["draft"].(map[string]any)["sections"].([]any)[0].(map[string]any)["body"].(string), draftOnlyMarker) {
		t.Fatalf("analysis: %v", an)
	}
	// В запросе к ИИ только предмет и фрагмент — без имён, email и других данных участников
	last := ai.last
	for _, s := range []string{last.Input, last.Instructions} {
		if strings.Contains(s, "Тест") || strings.Contains(s, "@test.local") || strings.Contains(s, "teacher") {
			t.Fatalf("в запрос к ИИ попали данные участников: %s", s)
		}
	}
	if !strings.Contains(last.Input, "Информатика") || !strings.Contains(last.Instructions, "Проще") {
		t.Fatal("в запросе предмет урока и сложность")
	}
	// Ученик не видит черновик ни по HTTP, ни по WebSocket
	_, sraw := e.raw("student", "GET", lessonPath(A, ""), nil)
	assertNoSecrets(t, "GET workshop (draft)", sraw)
	if strings.Contains(sraw, aid) {
		t.Fatal("неопубликованный разбор не должен быть виден ученику")
	}
	if code, _ := e.req("student", "PUT", lessonPath(A, "/analyses/"+aid), map[string]any{"draft": map[string]any{}}); code != 403 {
		t.Fatal("ученик не редактирует разбор")
	}
	if code, _ := e.req("student", "POST", lessonPath(A, "/analyses/"+aid+"/publish"), nil); code != 403 {
		t.Fatal("ученик не публикует разбор")
	}
	// Чужой урок не даёт доступа к разбору по id
	if code, _ := e.req("teacher", "POST", lessonPath(e.lessonB, "/analyses/"+aid+"/publish"), nil); code != 404 {
		t.Fatal("разбор урока A нельзя опубликовать через урок B")
	}

	// Преподаватель редактирует и публикует
	edit := map[string]any{"draft": map[string]any{"verdict": "none", "sections": []any{map[string]any{"heading": "Итог", "body": publishedMarker + " <script>alert(1)</script>"}}}}
	if code, body := e.req("teacher", "PUT", lessonPath(A, "/analyses/"+aid), edit); code != 200 || body["analysis"].(map[string]any)["published"] != nil {
		t.Fatalf("edit: %d %v", code, body)
	}
	if code, _ := e.req("teacher", "PUT", lessonPath(A, "/analyses/"+aid), map[string]any{"draft": map[string]any{"verdict": "none", "sections": []any{}}}); code != 400 {
		t.Fatal("пустой разбор отклоняется")
	}
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/analyses/"+aid+"/publish"), nil); code != 200 {
		t.Fatal("publish")
	}
	if m := next(t, student, "workshop.changed", nil); len(m) != 1 {
		t.Fatalf("уведомление без содержимого: %v", m)
	}
	next(t, teacherWS, "workshop.changed", nil)
	_, sraw = e.raw("student", "GET", lessonPath(A, ""), nil)
	assertNoSecrets(t, "GET workshop (published)", sraw)
	if !strings.Contains(sraw, publishedMarker) || !strings.Contains(sraw, "\\u003cscript\\u003e") {
		t.Fatalf("опубликованный разбор у ученика (HTML как текст): %s", sraw)
	}
	// Правка после публикации не видна ученику до повторной публикации
	edit["draft"].(map[string]any)["sections"] = []any{map[string]any{"heading": "Итог", "body": draftOnlyMarker}}
	e.req("teacher", "PUT", lessonPath(A, "/analyses/"+aid), edit)
	_, sraw = e.raw("student", "GET", lessonPath(A, ""), nil)
	assertNoSecrets(t, "GET after re-edit", sraw)

	// Проверка ответа: вердикт «не хватает условия»
	code, body = e.req("teacher", "POST", lessonPath(A, "/analyses"), map[string]any{"action": "check", "level": "standard", "fragment": "Ответ: 42"})
	if code != 201 || body["analysis"].(map[string]any)["draft"].(map[string]any)["verdict"] != "insufficient_data" {
		t.Fatalf("check: %d %v", code, body)
	}

	// Режим «Английский»: сохраняется для урока, видна у обоих, добавляет исправление
	if code, _ := e.req("teacher", "PUT", lessonPath(A, "/mode"), map[string]any{"mode": "math"}); code != 400 {
		t.Fatal("несуществующий режим")
	}
	if code, _ := e.req("teacher", "PUT", lessonPath(A, "/mode"), map[string]any{"mode": "english"}); code != 200 {
		t.Fatal("mode")
	}
	next(t, student, "workshop.changed", nil)
	if _, b := e.req("student", "GET", lessonPath(A, ""), nil); b["mode"] != "english" {
		t.Fatalf("режим сохранён: %v", b["mode"])
	}
	code, body = e.req("teacher", "POST", lessonPath(A, "/analyses"), map[string]any{"action": "correct", "level": "standard", "fragment": "She go to school"})
	if code != 201 || body["analysis"].(map[string]any)["mode"] != "english" || !strings.Contains(ai.last.Instructions, "Произношение") {
		t.Fatalf("english correct: %d %v", code, body)
	}

	// Домашнее задание
	code, body = e.req("teacher", "POST", lessonPath(A, "/homework"), map[string]any{"text": "Present Simple: She goes", "level": "standard"})
	if code != 201 {
		t.Fatalf("homework: %d %v", code, body)
	}
	hw := body["homework"].(map[string]any)
	hid := hw["id"].(string)
	if len(hw["draft"].(map[string]any)["exercises"].([]any)) != 5 || len(hw["answerKey"].(map[string]any)["answers"].([]any)) != 5 {
		t.Fatalf("homework draft: %v", hw)
	}
	if strings.Contains(ai.last.Input, draftOnlyMarker) || !strings.Contains(ai.last.Input, publishedMarker) {
		t.Fatal("в задание идут только опубликованные разборы")
	}
	_, sraw = e.raw("student", "GET", lessonPath(A, ""), nil)
	if strings.Contains(sraw, hid) || strings.Contains(sraw, hwReviewMarker) {
		t.Fatal("неопубликованное задание не видно ученику")
	}
	// Правка: 5 упражнений обязательно
	d := hw["draft"].(map[string]any)
	key := hw["answerKey"].(map[string]any)
	d["review"] = hwReviewMarker + " (исправлено)"
	if code, _ := e.req("teacher", "PUT", lessonPath(A, "/homework/"+hid), map[string]any{"draft": d, "answerKey": key}); code != 200 {
		t.Fatal("edit homework")
	}
	short := map[string]any{"review": "r", "exercises": d["exercises"].([]any)[:4]}
	if code, _ := e.req("teacher", "PUT", lessonPath(A, "/homework/"+hid), map[string]any{"draft": short, "answerKey": key}); code != 400 {
		t.Fatal("4 упражнения отклоняются")
	}
	if code, _ := e.req("student", "POST", lessonPath(A, "/homework/"+hid+"/publish"), nil); code != 403 {
		t.Fatal("ученик не публикует задание")
	}
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/homework/"+hid+"/publish"), nil); code != 200 {
		t.Fatal("publish homework")
	}
	msgs := collect(student, 500*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatal("ученик получает уведомление о публикации")
	}
	for _, m := range msgs {
		assertNoSecrets(t, "WS", m)
		if strings.Contains(m, hwReviewMarker) || strings.Contains(m, publishedMarker) {
			t.Fatalf("сообщения синхронизации не содержат материалов: %s", m)
		}
	}
	_, sraw = e.raw("student", "GET", lessonPath(A, ""), nil)
	assertNoSecrets(t, "GET workshop (homework)", sraw)
	if !strings.Contains(sraw, hwReviewMarker+" (исправлено)") || strings.Count(sraw, hwExerciseMarker) != 5 {
		t.Fatalf("ученик видит опубликованное задание: %s", sraw)
	}
	// Ответы хранятся у преподавателя
	_, traw := e.raw("teacher", "GET", lessonPath(A, ""), nil)
	if !strings.Contains(traw, answerMarker+"5") || !strings.Contains(traw, tipsMarker) {
		t.Fatal("ответы доступны преподавателю")
	}

	// После окончания: ИИ и режим закрыты, опубликованное доступно, публиковать/снимать можно
	e.req("teacher", "POST", "/api/lessons/"+A+"/end", nil)
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/analyses"), req); code != 409 {
		t.Fatalf("ИИ после окончания: %d", code)
	}
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/homework"), map[string]any{"text": "x", "level": "standard"}); code != 409 {
		t.Fatal("ДЗ через ИИ после окончания")
	}
	if code, _ := e.req("teacher", "PUT", lessonPath(A, "/mode"), map[string]any{"mode": "general"}); code != 409 {
		t.Fatal("режим после окончания")
	}
	code, body = e.req("student", "GET", lessonPath(A, ""), nil)
	if code != 200 || body["editable"] != false || len(body["homework"].([]any)) != 1 || len(body["analyses"].([]any)) != 1 {
		t.Fatalf("материалы после окончания: %d %v", code, body)
	}
	_, sraw = e.raw("student", "GET", lessonPath(A, ""), nil)
	assertNoSecrets(t, "GET after end", sraw)
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/analyses/"+aid+"/publish"), nil); code != 200 {
		t.Fatal("публикация после окончания урока")
	}
	// Отменённый урок: публикация запрещена
	e.pool.Exec(context.Background(), `UPDATE lessons SET status='cancelled' WHERE id=$1`, A)
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/homework/"+hid+"/unpublish"), nil); code != 409 {
		t.Fatal("отменённый урок")
	}
}

func TestWorkshopProviderFailures(t *testing.T) {
	e := setupCode(t)
	A := e.lessonA
	req := map[string]any{"action": "explain", "level": "standard", "fragment": "текст урока"}

	// ИИ не настроен: понятный отказ, редактор и сохранённые материалы работают, фиктивного ответа нет
	e.srv.SetAIProvider(nil)
	code, body := e.req("teacher", "GET", lessonPath(A, ""), nil)
	ai := body["ai"].(map[string]any)
	if code != 200 || ai["configured"] != false || ai["problem"] == "" {
		t.Fatalf("not configured state: %v", body)
	}
	e.req("teacher", "POST", lessonPath(A, "/ai-notice"), nil)
	if code, body := e.req("teacher", "POST", lessonPath(A, "/analyses"), req); code != 503 || body["code"] != "ai_not_configured" {
		t.Fatalf("not configured: %d %v", code, body)
	}
	if code, _ := e.req("teacher", "GET", lessonPath(A, "/doc"), nil); code != 200 {
		t.Fatal("документ доступен без ИИ")
	}

	f := &fakeAI{}
	e.srv.SetAIProvider(f)
	for _, tc := range []struct {
		name   string
		apply  func(f *fakeAI)
		status int
		code   string
	}{
		{"timeout", func(f *fakeAI) { f.err = &workshop.ProviderError{Kind: workshop.ErrTimeout} }, 504, "timeout"},
		{"provider down", func(f *fakeAI) { f.err = &workshop.ProviderError{Kind: workshop.ErrUnavailable, Status: 500} }, 502, "unavailable"},
		{"bad json", func(f *fakeAI) { f.err = nil; f.text = `{"sections":"<b>oops</b>"}` }, 502, "invalid_response"},
		{"empty sections", func(f *fakeAI) { f.text = `{"verdict":"none","sections":[]}` }, 502, "invalid_response"},
	} {
		f.set(tc.apply)
		code, body := e.req("teacher", "POST", lessonPath(A, "/analyses"), req)
		if code != tc.status || body["code"] != tc.code || body["error"] == "" {
			t.Fatalf("%s: %d %v", tc.name, code, body)
		}
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM workshop_analyses`).Scan(&n)
	if n != 0 {
		t.Fatalf("неудачные запросы не создают разборов: %d", n)
	}

	// Повторное нажатие, пока идёт запрос → 409; запрос выполняется один
	f.set(func(f *fakeAI) { f.text = ""; f.delay = 600 * time.Millisecond })
	before := f.calls.Load()
	done := make(chan int)
	go func() {
		c, _ := e.req("teacher", "POST", lessonPath(A, "/analyses"), req)
		done <- c
	}()
	time.Sleep(150 * time.Millisecond)
	if code, _ := e.req("teacher", "POST", lessonPath(A, "/analyses"), req); code != 409 {
		t.Fatalf("повторный запрос во время выполнения: %d", code)
	}
	if c := <-done; c != 201 {
		t.Fatalf("первый запрос: %d", c)
	}
	if f.calls.Load()-before != 1 {
		t.Fatal("к ИИ ушёл один запрос")
	}

	// Частота запросов
	f.set(func(f *fakeAI) { f.delay = 0 })
	e.srv.cfg.WorkshopAIPerMinute = 3
	limited := false
	for i := 0; i < 6 && !limited; i++ {
		c, _ := e.req("teacher", "POST", lessonPath(A, "/analyses"), req)
		limited = c == 429
	}
	if !limited {
		t.Fatal("ограничение частоты не сработало")
	}
}
