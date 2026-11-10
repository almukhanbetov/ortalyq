package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"ortalyq/backend/internal/auth"
	"ortalyq/backend/internal/coderun"
	"ortalyq/backend/internal/config"
	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/testdb"
)

// Интеграционный тест редактора кода (HTTP + WebSocket + PostgreSQL). Требует TEST_DATABASE_URL.
// Исполнитель — заглушка stubRunner: она НЕ выполняет код и возвращает заданный результат,
// проверяется маршрут бэкенда (права, лимиты, снимок, рассылка, сохранение).

type stubRunner struct {
	delay time.Duration
	calls atomic.Int32
	last  atomic.Value // coderun.Request
}

func (s *stubRunner) Run(ctx context.Context, r coderun.Request) (coderun.Result, error) {
	s.calls.Add(1)
	s.last.Store(r)
	select {
	case <-ctx.Done():
		return coderun.Result{Status: coderun.StatusCancelled, Message: "Запуск отменён"}, ctx.Err()
	case <-time.After(s.delay):
	}
	code, ms := 0, 15
	return coderun.Result{Status: coderun.StatusCompleted, Stdout: "stub-output\n", ExitCode: &code, TimeMs: &ms}, nil
}

type tenv struct {
	t       *testing.T
	srv     *Server
	http    *httptest.Server
	pool    *pgxpool.Pool
	lessonA string // teacher + student
	lessonB string // teacher + outsider
	cookies map[string]string
}

func setupCode(t *testing.T) *tenv {
	ctx := context.Background()
	// Своя пустая база на каждый тест: пакеты go test ./... идут параллельно и не должны видеть данные друг друга.
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	e := &tenv{t: t, pool: pool, cookies: map[string]string{}}
	sfx := fmt.Sprint(time.Now().UnixNano())
	ids := map[string]string{}
	for _, u := range []struct{ key, role string }{{"teacher", "teacher"}, {"student", "student"}, {"outsider", "student"}} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name) VALUES ($1,'x',$2,$3) RETURNING id`,
			u.key+sfx+"@test.local", u.role, "Тест "+u.key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		raw, hash, _ := auth.NewToken()
		if _, err := pool.Exec(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1,$2, now() + interval '1 hour')`, hash, id); err != nil {
			t.Fatal(err)
		}
		ids[u.key], e.cookies[u.key] = id, raw
	}
	// Оба урока сейчас открыты для входа (класс доступен с JoinEarlyMinutes до начала и до окончания):
	// A идёт [−10 мин, +10 мин), B начинается через 10 минут — вход уже открыт. Время у преподавателя не пересекается.
	base := time.Now().Truncate(time.Minute)
	mk := func(student string, from, to time.Duration) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, status)
			VALUES ($1,$2,'Информатика',$3,$4,'live') RETURNING id`, ids["teacher"], ids[student], base.Add(from), base.Add(to)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.lessonA, e.lessonB = mk("student", -10*time.Minute, 10*time.Minute), mk("outsider", 10*time.Minute, 70*time.Minute)

	cfg := &config.Config{AppSecret: strings.Repeat("s", 32), PublicOrigins: []string{"http://test.local"}, JoinEarlyMinutes: 15, CodeRunCPUSeconds: 2,
		CodeRunWallSeconds: 5, CodeRunMemoryMB: 128, CodeRunMaxConcurrent: 4, CodeRunnerPythonID: 71}
	e.srv = New(cfg, pool, nil, nil)
	hctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go e.srv.Hub().Run(hctx)
	e.srv.StartCodeRuns(hctx)
	e.http = httptest.NewServer(e.srv.Router())
	t.Cleanup(e.http.Close)
	return e
}

func (e *tenv) req(who, method, path string, body any) (int, map[string]any) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
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
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *tenv) ws(who, lesson string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	h.Set("Origin", "http://test.local")
	h.Set("Cookie", sessionCookie+"="+e.cookies[who])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.http.URL, "http")+"/api/lessons/"+lesson+"/ws", &websocket.DialOptions{HTTPHeader: h})
}

// next читает сообщения, пока не встретится тип typ (и pred, если задан).
func next(t *testing.T, c *websocket.Conn, typ string, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("ждали %s: %v", typ, err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		if m["type"] == typ && (pred == nil || pred(m)) {
			return m
		}
	}
}

func send(t *testing.T, c *websocket.Conn, v any) {
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func TestCodeAccessControl(t *testing.T) {
	e := setupCode(t)
	e.srv.SetCodeRunner(&stubRunner{})
	run := map[string]any{"language": "python", "source": "print(1)", "stdin": ""}

	if code, _ := e.req("", "GET", "/api/lessons/"+e.lessonA+"/code", nil); code != 401 {
		t.Fatalf("anonymous GET: %d", code)
	}
	// Посторонний (участник другого урока) не видит и не запускает код урока A
	if code, _ := e.req("outsider", "GET", "/api/lessons/"+e.lessonA+"/code", nil); code != 404 {
		t.Fatalf("outsider GET: %d", code)
	}
	if code, _ := e.req("outsider", "POST", "/api/lessons/"+e.lessonA+"/code/runs", run); code != 404 {
		t.Fatalf("outsider POST: %d", code)
	}
	if _, resp, err := e.ws("outsider", e.lessonA); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("outsider WS must be rejected with 404, got %v %v", resp, err)
	}
	// Ученик урока A не имеет доступа к уроку B
	if code, _ := e.req("student", "POST", "/api/lessons/"+e.lessonB+"/code/runs", run); code != 404 {
		t.Fatalf("student → lesson B: %d", code)
	}
	if _, resp, err := e.ws("student", e.lessonB); err == nil || resp.StatusCode != 404 {
		t.Fatalf("student WS → lesson B: %v", err)
	}
	// Участники урока A — доступ есть
	for _, who := range []string{"teacher", "student"} {
		if code, body := e.req(who, "GET", "/api/lessons/"+e.lessonA+"/code", nil); code != 200 || body["runnerConfigured"] != true {
			t.Fatalf("%s GET: %d %v", who, code, body)
		}
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM code_runs WHERE lesson_id IN ($1,$2)`, e.lessonA, e.lessonB).Scan(&n)
	if n != 0 {
		t.Fatalf("отказанные запросы не должны создавать запуски: %d", n)
	}
}

func TestCodeRunFlow(t *testing.T) {
	e := setupCode(t)
	stub := &stubRunner{delay: 300 * time.Millisecond}
	e.srv.SetCodeRunner(stub)
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

	src, stdin := "name = input()\nprint('Привет,', name)\n", "Айгерим\n"
	code, body := e.req("student", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "python", "source": src, "stdin": stdin})
	if code != 202 {
		t.Fatalf("run: %d %v", code, body)
	}
	runID := body["run"].(map[string]any)["id"].(string)
	// Второй запуск, пока первый выполняется, отклоняется
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "python", "source": "x"}); code != 409 {
		t.Fatalf("concurrent run: %d", code)
	}
	// Отменить запуск урока A через урок B нельзя
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonB+"/code/runs/"+runID+"/cancel", nil); code != 409 {
		t.Fatalf("cross-lesson cancel: %d", code)
	}
	// Оба участника получают итог с автором запуска
	for _, c := range []*websocket.Conn{teacher, student} {
		m := next(t, c, "code.run", func(m map[string]any) bool { return m["run"].(map[string]any)["status"] == "completed" })
		r := m["run"].(map[string]any)
		if r["id"] != runID || r["userName"] != "Тест student" || r["stdout"] != "stub-output\n" {
			t.Fatalf("run broadcast: %v", r)
		}
	}
	// Исполнителю передан ровно отправленный снимок кода и входных данных
	got := stub.last.Load().(coderun.Request)
	if got.Source != src || got.Stdin != stdin || got.Language.Judge0ID != 71 || got.Limits.MemoryKB != 128*1024 {
		t.Fatalf("snapshot: %+v", got)
	}
	// После обновления страницы результат доступен по HTTP
	if code, body := e.req("teacher", "GET", "/api/lessons/"+e.lessonA+"/code", nil); code != 200 || body["lastRun"].(map[string]any)["id"] != runID {
		t.Fatalf("lastRun: %v", body)
	}
	// Исходный код в БД не хранится — только хеш и размер
	var stored string
	e.pool.QueryRow(context.Background(), `SELECT row_to_json(r)::text FROM code_runs r WHERE id=$1`, runID).Scan(&stored)
	if strings.Contains(stored, "Айгерим") || strings.Contains(stored, "input()") {
		t.Fatal("source/stdin must not be stored in code_runs")
	}

	// Отмена
	stub.delay = 5 * time.Second
	_, body = e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "python", "source": "while True: pass"})
	rid := body["run"].(map[string]any)["id"].(string)
	if code, _ := e.req("student", "POST", "/api/lessons/"+e.lessonA+"/code/runs/"+rid+"/cancel", nil); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	next(t, student, "code.run", func(m map[string]any) bool { return m["run"].(map[string]any)["status"] == "cancelled" })

	// Лимиты размера
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "python", "source": strings.Repeat("x", coderun.MaxSourceBytes+1)}); code != 413 {
		t.Fatalf("big source: %d", code)
	}
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "python", "source": "x", "stdin": strings.Repeat("x", coderun.MaxStdinBytes+1)}); code != 413 {
		t.Fatalf("big stdin: %d", code)
	}
	if code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "cpp", "source": "x"}); code != 400 {
		t.Fatalf("unsupported language: %d", code)
	}
	// Частота: не больше 10 запусков в минуту на пользователя
	stub.delay = 0
	limited := false
	for i := 0; i < 15 && !limited; i++ {
		code, _ := e.req("teacher", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "python", "source": "print(1)"})
		limited = code == 429
		time.Sleep(30 * time.Millisecond)
	}
	if !limited {
		t.Fatal("rate limit not applied")
	}

	// Исполнитель не настроен: понятный ответ, фиктивного результата нет
	e.srv.SetCodeRunner(nil)
	code, body = e.req("student", "POST", "/api/lessons/"+e.lessonA+"/code/runs", map[string]any{"language": "python", "source": "print(1)"})
	if code != 503 || body["error"] != "Сервис выполнения не настроен" || body["code"] != "not_configured" {
		t.Fatalf("not configured: %d %v", code, body)
	}
}

func TestCodeSyncPersistence(t *testing.T) {
	e := setupCode(t)
	teacher, _, _ := e.ws("teacher", e.lessonA)
	student, _, _ := e.ws("student", e.lessonA)
	next(t, teacher, "hello", nil)
	next(t, student, "hello", nil)
	send(t, teacher, map[string]any{"type": "code.sync"})
	if m := next(t, teacher, "code.snapshot", nil); len(m["updates"].([]any)) != 0 {
		t.Fatalf("new lesson must have empty code doc: %v", m)
	}
	u1 := base64.StdEncoding.EncodeToString([]byte{1, 1, 2, 3})
	send(t, teacher, map[string]any{"type": "code.update", "update": u1, "cid": "c1"})
	if m := next(t, teacher, "code.ack", nil); m["cid"] != "c1" || m["seq"].(float64) != 1 {
		t.Fatalf("ack: %v", m)
	}
	if m := next(t, student, "code.update", nil); m["update"] != u1 || m["seq"].(float64) != 1 {
		t.Fatalf("relay: %v", m)
	}
	// Отправитель не получает своё обновление обратно (нет петли)
	send(t, student, map[string]any{"type": "code.update", "update": base64.StdEncoding.EncodeToString([]byte{9}), "cid": "s1"})
	next(t, student, "code.ack", nil)
	if m := next(t, teacher, "code.update", nil); m["seq"].(float64) != 2 {
		t.Fatalf("seq 2: %v", m)
	}
	// Слишком большое обновление отклоняется
	send(t, student, map[string]any{"type": "code.update", "update": base64.StdEncoding.EncodeToString(make([]byte, 300<<10)), "cid": "big"})
	next(t, student, "code.reject", nil)

	// Все вышли → состояние сохранено в БД; новая загрузка комнаты восстанавливает журнал
	teacher.Close(websocket.StatusNormalClosure, "")
	student.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	var seq int64
	for time.Now().Before(deadline) {
		e.pool.QueryRow(context.Background(), `SELECT seq FROM code_docs WHERE lesson_id=$1`, e.lessonA).Scan(&seq)
		if seq == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if seq != 2 {
		t.Fatalf("persisted seq = %d", seq)
	}
	time.Sleep(200 * time.Millisecond)
	again, _, _ := e.ws("student", e.lessonA)
	defer again.CloseNow()
	next(t, again, "hello", nil)
	send(t, again, map[string]any{"type": "code.sync"})
	m := next(t, again, "code.snapshot", nil)
	if ups := m["updates"].([]any); len(ups) != 2 || ups[0] != u1 || m["seq"].(float64) != 2 {
		t.Fatalf("restored snapshot: %v", m)
	}
	// Документ урока B пуст — состояние раздельное
	other, _, _ := e.ws("outsider", e.lessonB)
	defer other.CloseNow()
	next(t, other, "hello", nil)
	send(t, other, map[string]any{"type": "code.sync"})
	if m := next(t, other, "code.snapshot", nil); len(m["updates"].([]any)) != 0 {
		t.Fatalf("lesson B must not see lesson A code: %v", m)
	}
}
