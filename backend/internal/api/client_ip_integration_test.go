package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ortalyq/backend/internal/config"
)

// Адрес клиента для ограничений частоты: X-Forwarded-For / X-Real-IP учитываются только от TRUSTED_PROXIES.
// Запросы подаются прямо в роутер с заданным адресом соединения (RemoteAddr) — так проверяются и прямой
// клиент, и клиент за доверенным прокси, без сети.

func ipReq(t *testing.T, e *denv, peer, method, path string, body any, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	buf, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(buf))
	r.RemoteAddr = peer
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://test.local")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h, ok := routers.Load(e)
	if !ok {
		h, _ = routers.LoadOrStore(e, e.srv.Router())
	}
	h.(http.Handler).ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

var routers sync.Map // *denv → http.Handler

func randUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Прямой клиент (не из TRUSTED_PROXIES) подставляет новый X-Forwarded-For / X-Real-IP в каждом запросе —
// ограничения всё равно считаются по адресу соединения.
func TestClientIPSpoofIgnored(t *testing.T) {
	e := setupDemo(t, func(c *config.Config) { c.TrustedProxies = []string{"127.0.0.1"}; c.DemoMaxActive = 100 })
	const peer = "198.51.100.10:40000"
	spoof := func(i int) map[string]string {
		return map[string]string{"X-Forwarded-For": fmt.Sprintf("203.0.113.%d", i), "X-Real-IP": fmt.Sprintf("192.0.2.%d", i)}
	}

	// Создание демо-комнат: 3 в минуту с адреса
	for i := 0; i < 3; i++ {
		if code, r := ipReq(t, e, peer, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"}, spoof(i)); code != 201 {
			t.Fatalf("create %d: %d %v", i, code, r)
		}
	}
	if code, _ := ipReq(t, e, peer, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"}, spoof(99)); code != 429 {
		t.Fatalf("подмена X-Forwarded-For обошла лимит создания: %d", code)
	}

	// Принятие приглашений: 20 попыток за 10 минут с адреса (разные комнаты — лимит на комнату не мешает)
	const peer2 = "198.51.100.11:40000"
	last := 0
	for i := 0; i <= demoAcceptPerIP; i++ {
		last, _ = ipReq(t, e, peer2, "POST", "/api/demo/rooms/"+randUUID()+"/accept", map[string]any{"token": "x"}, spoof(i))
		if i < demoAcceptPerIP && last == 429 {
			t.Fatalf("попытка %d: 429 раньше лимита", i)
		}
	}
	if last != 429 {
		t.Fatalf("подмена X-Forwarded-For обошла лимит приглашений: %d", last)
	}

	// Вход: 30 попыток за 10 минут с адреса
	const peer3 = "198.51.100.12:40000"
	for i := 0; i <= 30; i++ {
		last, _ = ipReq(t, e, peer3, "POST", "/api/auth/login", map[string]any{"email": fmt.Sprintf("nobody%d@example.org", i), "password": "x"}, spoof(i))
	}
	if last != 429 {
		t.Fatalf("подмена X-Forwarded-For обошла лимит входа: %d", last)
	}

	// Регистрация: 20 в час с адреса
	const peer4 = "198.51.100.13:40000"
	for i := 0; i <= 20; i++ {
		last, _ = ipReq(t, e, peer4, "POST", "/api/auth/register", map[string]any{"email": "bad", "password": "x"}, spoof(i))
	}
	if last != 429 {
		t.Fatalf("подмена X-Forwarded-For обошла лимит регистрации: %d", last)
	}
}

// Через доверенный прокси (Nginx: X-Forwarded-For «<что прислал клиент>, <адрес клиента>») адрес клиента —
// крайний правый недоверенный: разные клиенты считаются раздельно, а подставленное клиентом слева не помогает.
func TestClientIPTrustedProxy(t *testing.T) {
	e := setupDemo(t, func(c *config.Config) { c.TrustedProxies = []string{"10.255.0.0/24"}; c.DemoMaxActive = 100 })
	const proxy = "10.255.0.7:5555"
	via := func(clientSays, real string) map[string]string {
		xff := real
		if clientSays != "" {
			xff = clientSays + ", " + real
		}
		return map[string]string{"X-Forwarded-For": xff, "X-Real-IP": real}
	}

	// Клиент A исчерпывает лимит, меняя «свой» X-Forwarded-For — прокси дописывает настоящий адрес справа.
	for i := 0; i < 3; i++ {
		if code, r := ipReq(t, e, proxy, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"}, via(fmt.Sprintf("203.0.113.%d", i), "198.51.100.20")); code != 201 {
			t.Fatalf("A create %d: %d %v", i, code, r)
		}
	}
	if code, _ := ipReq(t, e, proxy, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"}, via("198.51.100.21", "198.51.100.20")); code != 429 {
		t.Fatalf("клиент за прокси обошёл лимит, подставив адрес слева: %d", code)
	}
	// Клиент B за тем же прокси — свой лимит (прокси не склеивает всех в один адрес).
	if code, r := ipReq(t, e, proxy, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"}, via("", "198.51.100.21")); code != 201 {
		t.Fatalf("B за доверенным прокси: %d %v", code, r)
	}
	// Цепочка из двух доверенных прокси: берётся первый недоверенный справа.
	if code, _ := ipReq(t, e, proxy, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"},
		map[string]string{"X-Forwarded-For": "198.51.100.20, 10.255.0.9"}); code != 429 {
		t.Fatalf("A через два прокси: %d", code)
	}

	// Вход через прокси: лимит по настоящему адресу, не общий для всех.
	for i := 0; i <= 30; i++ {
		ipReq(t, e, proxy, "POST", "/api/auth/login", map[string]any{"email": "nobody@example.org", "password": "x"}, via(fmt.Sprintf("192.0.2.%d", i), "198.51.100.30"))
	}
	if code, _ := ipReq(t, e, proxy, "POST", "/api/auth/login", map[string]any{"email": "nobody@example.org", "password": "x"}, via("", "198.51.100.30")); code != 429 {
		t.Fatalf("лимит входа через прокси: %d", code)
	}
	if code, _ := ipReq(t, e, proxy, "POST", "/api/auth/login", map[string]any{"email": "nobody@example.org", "password": "x"}, via("", "198.51.100.31")); code == 429 {
		t.Fatal("другой клиент за прокси получил чужой лимит входа")
	}

	// Без доверия к прокси все клиенты за ним выглядят одним адресом (поэтому TRUSTED_PROXIES обязателен на VPS).
	e2 := setupDemo(t, func(c *config.Config) { c.TrustedProxies = nil; c.DemoMaxActive = 100 })
	const proxy2 = "10.254.0.7:5555"
	for i := 0; i < 3; i++ {
		ipReq(t, e2, proxy2, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"}, via("", fmt.Sprintf("198.51.100.%d", 40+i)))
	}
	if code, _ := ipReq(t, e2, proxy2, "POST", "/api/demo/rooms", map[string]any{"mode": "solo"}, via("", "198.51.100.49")); code != 429 {
		t.Fatalf("без TRUSTED_PROXIES ожидался общий лимит адреса прокси: %d", code)
	}
}
