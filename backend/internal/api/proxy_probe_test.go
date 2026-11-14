package api

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Проверочный запрос set-trusted-proxy.sh: строка журнала со своей меткой, адресом соединения и признаком
// доверия; адрес клиента в журнал не попадает; число записей ограничено.
func TestProxyProbeLog(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	proxyProbeLimiter = &limiter{hits: map[string][]time.Time{}}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.SetTrustedProxies([]string{"10.244.0.1"})
	r.Use(untrustedForwardWarning([]string{"10.244.0.1"}))
	r.GET("/api/health", func(c *gin.Context) { c.Status(200) })
	send := func(peer, mark string) {
		req := httptest.NewRequest("GET", "/api/health", nil)
		req.RemoteAddr = peer + ":5000"
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		if mark != "" {
			req.Header.Set(ProxyProbeHeader, mark)
		}
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	line := func(mark string) string {
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "probe="+mark) {
				return l
			}
		}
		return ""
	}

	a, b := strings.Repeat("a", 32), strings.Repeat("b", 32)
	send("10.244.0.1", a)
	send("10.42.0.9", b)
	if l := line(a); !strings.Contains(l, "peer=10.244.0.1 trusted=true forwarded_used=true") {
		t.Fatalf("доверенный: %q", l)
	}
	if l := line(b); !strings.Contains(l, "peer=10.42.0.9 trusted=false forwarded_used=false") {
		t.Fatalf("недоверенный: %q", l)
	}
	if strings.Contains(buf.String(), "192.0.2.1") {
		t.Fatal("адрес клиента попал в журнал")
	}
	for _, bad := range []string{"short", "ZZZZZZZZZZZZZZZZZZZZ", strings.Repeat("a", 65), "abcdef0123456789\nx"} {
		send("10.244.0.1", bad)
		if strings.Contains(buf.String(), "probe="+bad) {
			t.Fatalf("метка %q не должна записываться", bad)
		}
	}
	for i := 0; i < 30; i++ {
		send("10.244.0.1", strings.Repeat("c", 32))
	}
	if n := strings.Count(buf.String(), `msg="proxy probe"`); n != 20 {
		t.Fatalf("записей проверочного запроса: %d, ожидалось 20 в минуту", n)
	}
}
