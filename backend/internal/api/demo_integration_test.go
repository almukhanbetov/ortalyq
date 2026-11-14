package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"ortalyq/backend/internal/coderun"
	"ortalyq/backend/internal/config"
	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/testdb"
)

// Интеграционные тесты пробного урока (HTTP + WebSocket + PostgreSQL). Требуют TEST_DATABASE_URL.
// Каждый «браузер» — отдельный cookie jar (с учётом Path cookie) и свой адрес клиента (X-Forwarded-For),
// чтобы ограничения частоты разных тестов не мешали друг другу.

type denv struct {
	t    *testing.T
	srv  *Server
	http *httptest.Server
	pool *pgxpool.Pool
}

var demoIPSeq atomic.Int32

func setupDemo(t *testing.T, mut func(*config.Config)) *denv {
	ctx := context.Background()
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AppSecret: strings.Repeat("d", 32), PublicOrigins: []string{"http://test.local"}, JoinEarlyMinutes: 15,
		CodeRunCPUSeconds: 2, CodeRunWallSeconds: 5, CodeRunMemoryMB: 128, CodeRunMaxConcurrent: 4, CodeRunnerPythonID: 71,
		WorkshopAIPerMinute: 10, WorkshopAIPerHour: 100,
		DemoEnabled: true, DemoMinutes: 15, DemoMaxActive: 30, DemoPerIPPerHour: 50, DemoCodeRuns: 2, DemoRetentionHours: 24,
		// httptest-сервер слушает loopback: тест играет роль доверенного прокси и задаёт адрес «браузера» в X-Forwarded-For.
		TrustedProxies: []string{"127.0.0.1", "::1"}}
	if mut != nil {
		mut(cfg)
	}
	e := &denv{t: t, pool: pool, srv: New(cfg, pool, nil, nil)}
	hctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go e.srv.Hub().Run(hctx)
	e.srv.StartCodeRuns(hctx)
	e.http = httptest.NewServer(e.srv.Router())
	t.Cleanup(e.http.Close)
	return e
}

type browser struct {
	e   *denv
	jar *cookiejar.Jar
	ip  string
}

func (e *denv) browser() *browser {
	jar, _ := cookiejar.New(nil)
	n := demoIPSeq.Add(1)
	return &browser{e: e, jar: jar, ip: fmt.Sprintf("10.%d.%d.%d", n/65536%256, n/256%256, n%256)}
}

func (b *browser) req(method, path string, body any) (int, map[string]any) {
	b.e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	} else {
		rd = bytes.NewReader(nil)
	}
	r, _ := http.NewRequest(method, b.e.http.URL+path, rd)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://test.local")
	r.Header.Set("X-Forwarded-For", b.ip)
	resp, err := (&http.Client{Jar: b.jar}).Do(r)
	if err != nil {
		b.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (b *browser) ws(roomID string) (*websocket.Conn, *http.Response, error) {
	u, _ := url.Parse(b.e.http.URL + "/api/demo/rooms/" + roomID + "/ws")
	h := http.Header{}
	h.Set("Origin", "http://test.local")
	h.Set("X-Forwarded-For", b.ip)
	var parts []string
	for _, c := range b.jar.Cookies(u) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	if len(parts) > 0 {
		h.Set("Cookie", strings.Join(parts, "; "))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(u.String(), "http"), &websocket.DialOptions{HTTPHeader: h})
}

func (b *browser) mustWS(roomID string) *websocket.Conn {
	b.e.t.Helper()
	c, resp, err := b.ws(roomID)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		b.e.t.Fatalf("WS %s: %v (%d)", roomID, err, code)
	}
	b.e.t.Cleanup(func() { c.CloseNow() })
	next(b.e.t, c, "hello", nil)
	return c
}

func (b *browser) create(mode string) (string, map[string]any) {
	b.e.t.Helper()
	code, st := b.req("POST", "/api/demo/rooms", map[string]any{"mode": mode})
	if code != 201 {
		b.e.t.Fatalf("create %s: %d %v", mode, code, st)
	}
	return st["id"].(string), st
}

func inviteToken(t *testing.T, st map[string]any) string {
	t.Helper()
	inv, _ := st["invite"].(map[string]any)
	path, _ := inv["path"].(string)
	i := strings.Index(path, "#")
	if i < 0 {
		t.Fatalf("нет ссылки-приглашения: %v", st)
	}
	return path[i+1:]
}

// tableSnapshot — состояние таблиц реальных занятий, расписания и оплат (цены уроков).
func tableSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var out string
	err := pool.QueryRow(context.Background(), `SELECT concat_ws('|',
		(SELECT count(*) FROM users), (SELECT count(*) FROM sessions),
		(SELECT coalesce(string_agg(id::text||status||starts_at::text||ends_at::text||price::text, ',' ORDER BY id), '') FROM lessons),
		(SELECT coalesce(string_agg(id::text||starts_at::text||ends_at::text, ',' ORDER BY id), '') FROM availability),
		(SELECT count(*) FROM boards), (SELECT count(*) FROM chat_messages), (SELECT count(*) FROM code_docs),
		(SELECT count(*) FROM workshop_docs), (SELECT count(*) FROM code_runs), (SELECT count(*) FROM recordings))`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func seedRealLesson(t *testing.T, pool *pgxpool.Pool) string {
	ctx := context.Background()
	var teacher, student, lesson string
	pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name) VALUES ('t@demo.test','x','teacher','Т') RETURNING id`).Scan(&teacher)
	pool.QueryRow(ctx, `INSERT INTO users(email, password_hash, role, name) VALUES ('s@demo.test','x','student','С') RETURNING id`).Scan(&student)
	base := time.Now().Truncate(time.Minute)
	pool.Exec(ctx, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3)`, teacher, base.Add(-time.Hour), base.Add(3*time.Hour))
	if err := pool.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at, status, price)
		VALUES ($1,$2,'Математика',$3,$4,'live',5000) RETURNING id`, teacher, student, base.Add(-10*time.Minute), base.Add(50*time.Minute)).Scan(&lesson); err != nil {
		t.Fatal(err)
	}
	return lesson
}

func TestDemoSoloWithoutAccount(t *testing.T) {
	e := setupDemo(t, nil)
	lesson := seedRealLesson(t, e.pool)
	before := tableSnapshot(t, e.pool)

	a := e.browser()
	id, st := a.create("solo")
	me := st["me"].(map[string]any)
	if me["role"] != "teacher" || st["status"] != "active" || st["invite"] != nil {
		t.Fatalf("solo state: %v", st)
	}
	// Cookie демо выдаётся только для пути своей комнаты
	u, _ := url.Parse(e.http.URL + "/api/lessons/" + lesson)
	if len(a.jar.Cookies(u)) != 0 {
		t.Fatalf("cookie демо не должна отправляться в API уроков: %v", a.jar.Cookies(u))
	}
	if code, _ := a.req("GET", "/api/demo/rooms/"+id, nil); code != 200 {
		t.Fatalf("participant GET: %d", code)
	}
	// Демо-сессия не даёт доступа к урокам и данным пользователей
	for _, p := range []string{"/api/lessons", "/api/lessons/" + lesson, "/api/lessons/" + lesson + "/board", "/api/my/homework", "/api/admin/users"} {
		if code, _ := a.req("GET", p, nil); code != 401 {
			t.Fatalf("%s с демо-cookie: %d, ожидали 401", p, code)
		}
	}
	if code, body := a.req("GET", "/api/auth/me", nil); code != 200 || body["user"] != nil {
		t.Fatalf("демо-посетитель не должен быть пользователем: %d %v", code, body)
	}
	// Посторонний не видит демонстрацию
	if code, _ := e.browser().req("GET", "/api/demo/rooms/"+id, nil); code != 404 {
		t.Fatalf("outsider GET: %d", code)
	}
	if _, resp, err := e.browser().ws(id); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("outsider WS должен получить 404: %v", err)
	}
	// Класс работает: WebSocket, мастерская без ИИ, вкладка кода без исполнителя
	c := a.mustWS(id)
	send(t, c, map[string]any{"type": "chat.send", "body": "привет", "clientId": "1"})
	next(t, c, "chat.message", nil)
	if code, w := a.req("GET", "/api/demo/rooms/"+id+"/workshop", nil); code != 200 || w["demo"] != true || w["ai"].(map[string]any)["configured"] != false {
		t.Fatalf("workshop: %d %v", code, w)
	}
	if code, ci := a.req("GET", "/api/demo/rooms/"+id+"/code", nil); code != 200 || ci["runnerConfigured"] != false || !strings.Contains(ci["runnerProblem"].(string), "не настроен") {
		t.Fatalf("code info: %d %v", code, ci)
	}
	if code, r := a.req("POST", "/api/demo/rooms/"+id+"/code/runs", map[string]any{"language": "python", "source": "print(1)"}); code != 503 || !strings.Contains(r["error"].(string), "не настроен") {
		t.Fatalf("run without runner: %d %v", code, r)
	}
	// Видео без LiveKit — понятная причина
	if code, r := a.req("POST", "/api/demo/rooms/"+id+"/join", nil); code != 503 || !strings.Contains(r["error"].(string), "Видеосвязь не настроена") {
		t.Fatalf("join without LiveKit: %d %v", code, r)
	}
	if code, _ := a.req("POST", "/api/demo/rooms/"+id+"/end", nil); code != 200 {
		t.Fatalf("end: %d", code)
	}
	if after := tableSnapshot(t, e.pool); after != before {
		t.Fatalf("демонстрация изменила уроки/расписание/оплаты:\n%s\n%s", before, after)
	}
}

func TestDemoPairInviteAndThirdRefused(t *testing.T) {
	e := setupDemo(t, nil)
	host, guest, third := e.browser(), e.browser(), e.browser()
	id, st := host.create("pair")
	token := inviteToken(t, st)

	if code, _ := guest.req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": token + "x"}); code != 404 {
		t.Fatalf("bad token: %d", code)
	}
	code, gs := guest.req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": token, "name": "Алия"})
	if code != 201 || gs["me"].(map[string]any)["role"] != "student" || gs["me"].(map[string]any)["name"] != "Алия (демо)" {
		t.Fatalf("accept: %d %v", code, gs)
	}
	if gs["invite"] != nil {
		t.Fatal("ученик не должен видеть приглашение")
	}
	// Третий участник — отказ (сервер), повтор гостя — тот же участник
	if code, r := third.req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": token}); code != 409 || !strings.Contains(r["error"].(string), "два участника") {
		t.Fatalf("third: %d %v", code, r)
	}
	if code, r := guest.req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": token}); code != 200 || r["me"].(map[string]any)["role"] != "student" {
		t.Fatalf("guest again: %d %v", code, r)
	}
	if code, r := host.req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": token}); code != 200 || r["me"].(map[string]any)["role"] != "teacher" {
		t.Fatalf("host accepts own link: %d %v", code, r)
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM demo_participants WHERE room_id=$1`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("participants: %d", n)
	}
	if _, st := host.req("GET", "/api/demo/rooms/"+id, nil); st["invite"].(map[string]any)["accepted"] != true {
		t.Fatalf("host state after accept: %v", st)
	}
	// Ученик не может завершить демонстрацию для всех
	if code, _ := guest.req("POST", "/api/demo/rooms/"+id+"/end", nil); code != 403 {
		t.Fatalf("student end: %d", code)
	}
	// Приглашение в самостоятельную демонстрацию не действует
	sid, _ := e.browser().create("solo")
	if code, _ := third.req("POST", "/api/demo/rooms/"+sid+"/accept", map[string]any{"token": e.srv.demoInviteToken(sid)}); code != 404 {
		t.Fatalf("accept into solo: %d", code)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestDemoSyncAndIsolation(t *testing.T) {
	e := setupDemo(t, nil)
	host, guest, other := e.browser(), e.browser(), e.browser()
	id, st := host.create("pair")
	guest.req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": inviteToken(t, st)})
	id2, _ := other.create("solo")

	// Чужая комната: ни по своей cookie, ни подставив значение cookie под путь другой комнаты
	if code, _ := host.req("GET", "/api/demo/rooms/"+id2, nil); code != 404 {
		t.Fatalf("host → room2: %d", code)
	}
	u1, _ := url.Parse(e.http.URL + "/api/demo/rooms/" + id)
	u2, _ := url.Parse(e.http.URL + "/api/demo/rooms/" + id2)
	host.jar.SetCookies(u2, []*http.Cookie{{Name: demoCookie, Value: host.jar.Cookies(u1)[0].Value, Path: demoCookiePath(id2)}})
	if code, _ := host.req("GET", "/api/demo/rooms/"+id2, nil); code != 404 {
		t.Fatalf("подставленная cookie другой комнаты: %d", code)
	}

	h, g, o := host.mustWS(id), guest.mustWS(id), other.mustWS(id2)
	el := `{"id":"rect-1","type":"rectangle","version":2,"versionNonce":5,"isDeleted":false}`
	send(t, h, map[string]any{"type": "board.update", "elements": []json.RawMessage{json.RawMessage(el)}})
	next(t, g, "board.update", nil)
	send(t, g, map[string]any{"type": "code.update", "update": b64("code-upd"), "cid": "c1"})
	if m := next(t, h, "code.update", nil); m["update"] != b64("code-upd") {
		t.Fatalf("code relay: %v", m)
	}
	send(t, h, map[string]any{"type": "workshop.update", "update": b64("text-upd"), "cid": "w1"})
	if m := next(t, g, "workshop.update", nil); m["update"] != b64("text-upd") {
		t.Fatalf("workshop relay: %v", m)
	}
	// Другая демо-комната ничего из этого не получила
	send(t, o, map[string]any{"type": "board.sync"})
	if m := next(t, o, "board.snapshot", nil); len(m["board"].(map[string]any)["elements"].([]any)) != 0 {
		t.Fatalf("утечка доски в другую комнату: %v", m)
	}
	send(t, o, map[string]any{"type": "code.sync"})
	if m := next(t, o, "code.snapshot", nil); len(m["updates"].([]any)) != 0 {
		t.Fatalf("утечка кода в другую комнату: %v", m)
	}
	// Разрешение на начальный пример выдаётся один раз и только для пустого содержимого
	send(t, o, map[string]any{"type": "workshop.seed"})
	next(t, o, "workshop.seed.grant", nil)
	send(t, o, map[string]any{"type": "workshop.seed"})
	send(t, o, map[string]any{"type": "ping"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, data, err := o.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		if m["type"] == "workshop.seed.grant" {
			t.Fatal("повторное разрешение на пример")
		}
		if m["type"] == "pong" {
			break
		}
	}
	send(t, h, map[string]any{"type": "board.seed"}) // доска уже не пуста
	send(t, h, map[string]any{"type": "ping"})
	for {
		_, data, err := h.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		if m["type"] == "board.seed.grant" {
			t.Fatal("пример для непустой доски")
		}
		if m["type"] == "pong" {
			break
		}
	}
	// Содержимое демонстрации не попадает в таблицы уроков
	e.srv.Hub().Flush(demoKey(id))
	var boards, docs int
	e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM boards), (SELECT count(*) FROM code_docs) + (SELECT count(*) FROM workshop_docs)`).Scan(&boards, &docs)
	if boards+docs != 0 {
		t.Fatalf("демо сохранилось в БД: boards=%d docs=%d", boards, docs)
	}
}

func readUntilClosed(t *testing.T, c *websocket.Conn) (sawEnded bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == -1 && ctx.Err() != nil {
				t.Fatal("соединение не закрыто сервером")
			}
			return sawEnded
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		if m["type"] == "demo.ended" {
			sawEnded = true
		}
	}
}

func TestDemoExpiresOnServerTime(t *testing.T) {
	e := setupDemo(t, nil)
	host, guest := e.browser(), e.browser()
	id, st := host.create("pair")
	guest.req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": inviteToken(t, st)})
	h, g := host.mustWS(id), guest.mustWS(id)

	// Сокращаем срок до 1 с (таймер окончания пересоздаётся так же, как при создании)
	expires := time.Now().Add(time.Second)
	e.pool.Exec(context.Background(), `UPDATE demo_rooms SET expires_at=$2 WHERE id=$1`, id, expires)
	e.srv.demo.forget(id)
	e.srv.scheduleDemoEnd(id, expires)

	if !readUntilClosed(t, h) || !readUntilClosed(t, g) {
		t.Fatal("участники должны получить demo.ended")
	}
	if e.srv.Hub().HasRoom(demoKey(id)) {
		t.Fatal("комната не освобождена в памяти")
	}
	var reason string
	e.pool.QueryRow(context.Background(), `SELECT end_reason FROM demo_rooms WHERE id=$1`, id).Scan(&reason)
	if reason != "expired" {
		t.Fatalf("end_reason=%q", reason)
	}
	if code, s := host.req("GET", "/api/demo/rooms/"+id, nil); code != 200 || s["status"] != "ended" {
		t.Fatalf("state after expiry: %d %v", code, s)
	}
	if _, resp, err := host.ws(id); err == nil || resp == nil || resp.StatusCode != 410 {
		t.Fatalf("WS после окончания должен получить 410: %v", err)
	}
	if code, _ := host.req("POST", "/api/demo/rooms/"+id+"/code/runs", map[string]any{"language": "python", "source": "print(1)"}); code != 409 {
		t.Fatalf("run after expiry: %d", code)
	}
	if code, _ := e.browser().req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": inviteToken(t, st)}); code != 410 && code != 409 {
		t.Fatalf("accept after expiry: %d", code)
	}

	// Истёкшая без таймера (как после перезапуска) закрывается фоновой задачей; старые записи удаляются
	id2, _ := e.browser().create("solo")
	e.srv.demo.forget(id2)
	e.pool.Exec(context.Background(), `UPDATE demo_rooms SET created_at=now()-interval '2 days', expires_at=now()-interval '2 days' + interval '15 minutes' WHERE id=$1`, id2)
	e.srv.demoSweep(context.Background())
	var left int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM demo_rooms WHERE id=$1`, id2).Scan(&left)
	if left != 0 {
		t.Fatal("служебные записи старше срока хранения должны удаляться")
	}
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM demo_participants WHERE room_id=$1`, id2).Scan(&left)
	if left != 0 {
		t.Fatal("участники удалённой демонстрации должны удаляться")
	}
}

func TestDemoEndByHost(t *testing.T) {
	e := setupDemo(t, nil)
	host := e.browser()
	id, _ := host.create("solo")
	c := host.mustWS(id)
	if code, _ := host.req("POST", "/api/demo/rooms/"+id+"/end", nil); code != 200 {
		t.Fatalf("end: %d", code)
	}
	if !readUntilClosed(t, c) {
		t.Fatal("нет demo.ended")
	}
	if _, resp, err := host.ws(id); err == nil || resp.StatusCode != 410 {
		t.Fatalf("WS после завершения: %v", err)
	}
	if len(e.srv.Hub().EphemeralKeys()) != 0 {
		t.Fatal("в памяти остались временные комнаты")
	}
}

func TestDemoLimits(t *testing.T) {
	e := setupDemo(t, func(c *config.Config) { c.DemoMaxActive = 3 })
	a := e.browser()
	for i := 0; i < 3; i++ {
		a.create("solo")
	}
	// 3 в минуту с одного адреса
	if code, _ := a.req("POST", "/api/demo/rooms", map[string]any{"mode": "solo"}); code != 429 {
		t.Fatalf("4-я за минуту: %d", code)
	}
	// Общий предел активных демонстраций
	if code, r := e.browser().req("POST", "/api/demo/rooms", map[string]any{"mode": "pair"}); code != 503 || !strings.Contains(r["error"].(string), "слишком много") {
		t.Fatalf("сверх DEMO_MAX_ACTIVE: %d %v", code, r)
	}
	if code, _ := e.browser().req("POST", "/api/demo/rooms", map[string]any{"mode": "group"}); code != 400 {
		t.Fatalf("неизвестный режим: %d", code)
	}
	// Перебор приглашений в одну комнату ограничен
	e.pool.Exec(context.Background(), `UPDATE demo_rooms SET ended_at=now(), end_reason='ended'`)
	host := e.browser()
	id, _ := host.create("pair")
	last := 0
	for i := 0; i < demoAcceptPerRoom+1; i++ {
		last, _ = e.browser().req("POST", "/api/demo/rooms/"+id+"/accept", map[string]any{"token": "wrong"})
	}
	if last != 429 {
		t.Fatalf("перебор приглашений: %d", last)
	}
}

func TestDemoCodeRunLimits(t *testing.T) {
	e := setupDemo(t, nil)
	stub := &stubRunner{}
	e.srv.SetCodeRunner(stub)
	a := e.browser()
	id, _ := a.create("solo")
	c := a.mustWS(id)
	run := map[string]any{"language": "python", "source": "print(1)", "stdin": ""}
	for i := 0; i < 2; i++ {
		code, r := a.req("POST", "/api/demo/rooms/"+id+"/code/runs", run)
		if code != 202 {
			t.Fatalf("run %d: %d %v", i, code, r)
		}
		next(t, c, "code.run", func(m map[string]any) bool { return m["run"].(map[string]any)["status"] == "completed" })
	}
	if code, r := a.req("POST", "/api/demo/rooms/"+id+"/code/runs", run); code != 429 || !strings.Contains(r["error"].(string), "лимит") {
		t.Fatalf("сверх лимита: %d %v", code, r)
	}
	if _, ci := a.req("GET", "/api/demo/rooms/"+id+"/code", nil); ci["runsLeft"].(float64) != 0 || ci["lastRun"].(map[string]any)["stdout"] != "stub-output\n" {
		t.Fatalf("code info: %v", ci)
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM code_runs`).Scan(&n)
	if n != 0 {
		t.Fatal("запуски демо не сохраняются в таблице уроков")
	}
}

// Исполнитель настроен, но не отвечает (настоящий адаптер Judge0, закрытый порт): участники видят понятную
// причину без служебных подробностей, попытка не расходует лимит, поддельного результата нет.
func TestDemoCodeRunnerDown(t *testing.T) {
	e := setupDemo(t, nil)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close() // адрес гарантированно не принимает соединения
	e.srv.SetCodeRunner(coderun.NewJudge0(dead.URL, "X-Auth-Token", "t"))
	a := e.browser()
	id, _ := a.create("solo")
	c := a.mustWS(id)
	code, r := a.req("POST", "/api/demo/rooms/"+id+"/code/runs", map[string]any{"language": "python", "source": "print(1)", "stdin": ""})
	if code != 202 {
		t.Fatalf("run: %d %v", code, r)
	}
	var fin map[string]any
	next(t, c, "code.run", func(m map[string]any) bool {
		fin = m
		return m["run"].(map[string]any)["status"] != "running"
	})
	run := fin["run"].(map[string]any)
	if run["status"] != coderun.StatusUnavailable || !strings.Contains(run["message"].(string), "недоступен") || strings.Contains(run["message"].(string), "CODE_RUNNER") || run["stdout"] != nil && run["stdout"] != "" {
		t.Fatalf("результат при недоступном исполнителе: %v", run)
	}
	if fin["runsLeft"].(float64) != 2 {
		t.Fatalf("неудачная попытка засчитана: %v", fin["runsLeft"])
	}
	if _, ci := a.req("GET", "/api/demo/rooms/"+id+"/code", nil); ci["runsLeft"].(float64) != 2 {
		t.Fatalf("code info: %v", ci)
	}
}
