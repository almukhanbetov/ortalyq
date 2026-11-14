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
)

// Адрес клиента для ограничений частоты: X-Forwarded-For / X-Real-IP учитываются только от TRUSTED_PROXIES.
// Запросы подаются прямо в роутер с заданным адресом соединения (RemoteAddr) — так проверяются и прямой
// клиент, и клиент за доверенным прокси, без сети. Ограничения — вход, регистрация и проверка
// приглашений на ознакомительный урок.

func ipReq(t *testing.T, e *tenv, peer, method, path string, body any, hdr map[string]string) (int, map[string]any) {
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

var routers sync.Map // *tenv → http.Handler

func randUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// ipEnv — окружение урока с заданным списком доверенных прокси (до первого запроса к роутеру).
func ipEnv(t *testing.T, trusted []string) *tenv {
	e := setupCode(t)
	e.srv.cfg.TrustedProxies = trusted
	return e
}

// Прямой клиент (не из TRUSTED_PROXIES) подставляет новый X-Forwarded-For / X-Real-IP в каждом запросе —
// ограничения всё равно считаются по адресу соединения.
func TestClientIPSpoofIgnored(t *testing.T) {
	e := ipEnv(t, []string{"127.0.0.1"})
	spoof := func(i int) map[string]string {
		return map[string]string{"X-Forwarded-For": fmt.Sprintf("203.0.113.%d", i), "X-Real-IP": fmt.Sprintf("192.0.2.%d", i)}
	}
	last := 0

	// Проверка приглашений: 30 попыток за 10 минут с адреса
	const peer1 = "198.51.100.11:40000"
	for i := 0; i <= 30; i++ {
		last, _ = ipReq(t, e, peer1, "POST", "/api/trial/"+randUUID()+"/invite", map[string]any{"code": "x"}, spoof(i))
		if i < 30 && last == 429 {
			t.Fatalf("попытка %d: 429 раньше лимита", i)
		}
	}
	if last != 429 {
		t.Fatalf("подмена X-Forwarded-For обошла лимит приглашений: %d", last)
	}

	// Вход: 30 попыток за 10 минут с адреса
	const peer2 = "198.51.100.12:40000"
	for i := 0; i <= 30; i++ {
		last, _ = ipReq(t, e, peer2, "POST", "/api/auth/login", map[string]any{"email": fmt.Sprintf("nobody%d@example.org", i), "password": "x"}, spoof(i))
	}
	if last != 429 {
		t.Fatalf("подмена X-Forwarded-For обошла лимит входа: %d", last)
	}

	// Регистрация: 20 в час с адреса
	const peer3 = "198.51.100.13:40000"
	for i := 0; i <= 20; i++ {
		last, _ = ipReq(t, e, peer3, "POST", "/api/auth/register", map[string]any{"email": "bad", "password": "x"}, spoof(i))
	}
	if last != 429 {
		t.Fatalf("подмена X-Forwarded-For обошла лимит регистрации: %d", last)
	}
}

// Через доверенный прокси (Nginx: X-Forwarded-For «<что прислал клиент>, <адрес клиента>») адрес клиента —
// крайний правый недоверенный: разные клиенты считаются раздельно, а подставленное клиентом слева не помогает.
func TestClientIPTrustedProxy(t *testing.T) {
	e := ipEnv(t, []string{"10.255.0.0/24"})
	const proxy = "10.255.0.7:5555"
	via := func(clientSays, real string) map[string]string {
		xff := real
		if clientSays != "" {
			xff = clientSays + ", " + real
		}
		return map[string]string{"X-Forwarded-For": xff, "X-Real-IP": real}
	}
	login := map[string]any{"email": "nobody@example.org", "password": "x"}

	// Клиент A исчерпывает лимит, меняя «свой» X-Forwarded-For — прокси дописывает настоящий адрес справа.
	for i := 0; i < 30; i++ {
		if code, _ := ipReq(t, e, proxy, "POST", "/api/auth/login", login, via(fmt.Sprintf("192.0.2.%d", i), "198.51.100.30")); code == 429 {
			t.Fatalf("A: 429 на попытке %d", i)
		}
	}
	if code, _ := ipReq(t, e, proxy, "POST", "/api/auth/login", login, via("198.51.100.31", "198.51.100.30")); code != 429 {
		t.Fatalf("клиент за прокси обошёл лимит, подставив адрес слева: %d", code)
	}
	// Цепочка из двух доверенных прокси: берётся первый недоверенный справа.
	if code, _ := ipReq(t, e, proxy, "POST", "/api/auth/login", login, map[string]string{"X-Forwarded-For": "198.51.100.30, 10.255.0.9"}); code != 429 {
		t.Fatalf("A через два прокси: %d", code)
	}
	// Клиент B за тем же прокси — свой лимит (прокси не склеивает всех в один адрес).
	if code, _ := ipReq(t, e, proxy, "POST", "/api/auth/login", login, via("", "198.51.100.31")); code == 429 {
		t.Fatal("другой клиент за прокси получил чужой лимит входа")
	}

	// Без доверия к прокси все клиенты за ним выглядят одним адресом (поэтому TRUSTED_PROXIES обязателен на VPS).
	e2 := ipEnv(t, nil)
	const proxy2 = "10.254.0.7:5555"
	for i := 0; i < 30; i++ {
		ipReq(t, e2, proxy2, "POST", "/api/auth/login", login, via("", fmt.Sprintf("198.51.100.%d", 40+i)))
	}
	if code, _ := ipReq(t, e2, proxy2, "POST", "/api/auth/login", login, via("", "198.51.100.99")); code != 429 {
		t.Fatalf("без TRUSTED_PROXIES ожидался общий лимит адреса прокси: %d", code)
	}
}
