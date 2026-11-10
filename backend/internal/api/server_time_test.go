package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Каждый ответ API несёт серверное время: по нему клиент считает статус урока, остаток и кнопку входа,
// а не по часам устройства (иначе при неверных часах один урок «идёт» у одного участника и «через 2 часа» у другого).
func TestServerTimeHeader(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(serverTimeHeader())
	r.GET("/api/x", func(c *gin.Context) { c.Status(204) })
	r.GET("/api/fail", func(c *gin.Context) { c.JSON(403, gin.H{"error": "нет"}) })
	for _, path := range []string{"/api/x", "/api/fail"} {
		before := time.Now().UnixMilli()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		ms, err := strconv.ParseInt(w.Header().Get(ServerTimeHeader), 10, 64)
		if err != nil || ms < before || ms > time.Now().UnixMilli() {
			t.Fatalf("%s: %s=%q (ожидалось текущее время сервера в мс)", path, ServerTimeHeader, w.Header().Get(ServerTimeHeader))
		}
	}
}
