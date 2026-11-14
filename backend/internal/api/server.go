package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"ortalyq/backend/internal/auth"
	"ortalyq/backend/internal/coderun"
	"ortalyq/backend/internal/config"
	"ortalyq/backend/internal/lk"
	"ortalyq/backend/internal/realtime"
	"ortalyq/backend/internal/storage"
	"ortalyq/backend/internal/workshop"
)

type Server struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	store  *storage.Store // nil, если хранилище не настроено
	lk     *lk.Client     // nil, если LiveKit не настроен
	signer *auth.Signer
	hub    *realtime.Hub
	runner coderun.Runner // nil, если исполнитель кода не настроен
	runs   *codeRuns
	ai     workshop.Provider // nil, если ИИ мастерской не настроен
	aiBusy *aiInFlight
	demo   *demoMemory // пробный урок: содержимое временных комнат (только в памяти)
}

func New(cfg *config.Config, db *pgxpool.Pool, store *storage.Store, lkc *lk.Client) *Server {
	s := &Server{cfg: cfg, db: db, store: store, lk: lkc, signer: auth.NewSigner(cfg.AppSecret)}
	s.hub = realtime.NewHub(db)
	s.runs = newCodeRuns(cfg.CodeRunMaxConcurrent)
	s.aiBusy = &aiInFlight{busy: map[string]bool{}}
	s.demo = newDemoMemory()
	if cfg.AIProblem() == "" {
		s.ai = &workshop.OpenAI{BaseURL: cfg.OpenAIBaseURL, APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIModel,
			ReasoningEffort: cfg.OpenAIReasoningEffort, MaxOutputTokens: cfg.OpenAIMaxOutputTokens,
			Timeout: time.Duration(cfg.OpenAITimeoutSeconds) * time.Second, HTTP: &http.Client{}}
	}
	if cfg.CodeRunnerConfigured() {
		s.runner = coderun.NewJudge0(cfg.CodeRunnerURL, cfg.CodeRunnerAuthHeader, cfg.CodeRunnerToken)
	}
	return s
}

// SetCodeRunner подменяет исполнитель кода (тесты).
func (s *Server) SetCodeRunner(r coderun.Runner) { s.runner = r }

func (s *Server) Hub() *realtime.Hub { return s.hub }

const sessionCookie = "ortalyq_session"

type ctxUser struct {
	ID       string
	Role     string
	Name     string
	Email    string
	Timezone string
}

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Адрес клиента (c.ClientIP) для ограничений частоты: X-Forwarded-For / X-Real-IP принимаются только
	// от прокси из TRUSTED_PROXIES, иначе — адрес соединения. Gin по умолчанию доверяет всем — отключаем.
	if err := r.SetTrustedProxies(s.cfg.TrustedProxies); err != nil {
		slog.Error("TRUSTED_PROXIES: not applied, trusting no proxies", "err", err)
		_ = r.SetTrustedProxies(nil)
	}
	r.Use(gin.Recovery(), requestID(), untrustedForwardWarning(s.cfg.TrustedProxies), requestLogger(), securityHeaders(), serverTimeHeader())
	r.MaxMultipartMemory = 8 << 20

	// /api/health — процесс жив (liveness); /api/ready — есть связь с БД (readiness).
	r.GET("/api/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/api/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.Ping(ctx); err != nil {
			c.JSON(503, gin.H{"ok": false, "error": "database unavailable"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})

	a := r.Group("/api")
	a.Use(s.originCheck(), s.loadSession())

	a.GET("/config", s.publicConfig)
	a.POST("/auth/register", s.register)
	a.POST("/auth/login", s.login)
	a.POST("/auth/logout", s.logout)
	a.GET("/auth/me", s.me)

	// Публичные разделы
	a.GET("/subjects", s.subjects)
	a.GET("/teachers", s.listTeachers)
	a.GET("/teachers/:id", s.getTeacher)
	a.GET("/teachers/:id/slots", s.teacherSlots)
	a.GET("/teachers/:id/photo", s.teacherPhoto)
	a.GET("/listings", s.listListings)

	// Пробный урок: без регистрации, доступ — по cookie участника своей демо-комнаты (см. demo.go)
	a.POST("/demo/rooms", s.createDemo)
	a.GET("/demo/rooms/:id", s.getDemo)
	a.POST("/demo/rooms/:id/accept", s.acceptDemo)
	a.POST("/demo/rooms/:id/join", s.joinDemo)
	a.POST("/demo/rooms/:id/end", s.endDemoHandler)
	a.GET("/demo/rooms/:id/ws", s.demoWS)
	a.GET("/demo/rooms/:id/code", s.getDemoCode)
	a.POST("/demo/rooms/:id/code/runs", s.createDemoCodeRun)
	a.POST("/demo/rooms/:id/code/runs/:rid/cancel", s.cancelDemoCodeRun)
	a.GET("/demo/rooms/:id/workshop", s.getDemoWorkshop)

	// Временные ссылки на файлы (проверка подписи, без cookie)
	a.GET("/files/:token", s.serveSignedFile)
	// Шаблон записи (egress) — доступ по подписанному токену
	a.GET("/recorder/:token/state", s.recorderState)
	// Вебхук LiveKit (подписан ключом API)
	a.POST("/livekit/webhook", s.livekitWebhook)

	u := a.Group("")
	u.Use(requireUser())
	u.PATCH("/me", s.updateMe)
	u.GET("/me/profile", s.myTeacherProfile)
	u.PUT("/me/profile", requireRole("teacher"), s.updateTeacherProfile)
	u.POST("/me/photo", requireRole("teacher"), s.uploadPhoto)

	u.POST("/listings", requireRole("teacher", "student"), s.createListing)
	u.GET("/my/listings", s.myListings)
	u.PUT("/listings/:id", s.updateListing)
	u.DELETE("/listings/:id", s.deleteListing)

	u.GET("/my/availability", requireRole("teacher"), s.myAvailability)
	u.POST("/my/availability", requireRole("teacher"), s.addAvailability)
	u.DELETE("/my/availability/:id", requireRole("teacher"), s.deleteAvailability)

	u.POST("/lessons", requireRole("student"), s.bookLesson)
	u.GET("/lessons", s.myLessons)
	u.GET("/my/students", requireRole("teacher"), s.myStudents)
	u.GET("/my/homework", s.myHomework)
	u.POST("/me/lesson-check", s.myLessonCheck)
	u.POST("/me/diagnostics", s.myDeviceReport)
	u.GET("/lessons/:id", s.getLesson)
	u.POST("/lessons/:id/cancel", s.cancelLesson)
	u.POST("/lessons/:id/join", s.joinLesson)
	u.POST("/lessons/:id/end", requireRole("teacher"), s.endLesson)
	u.GET("/lessons/:id/ws", s.lessonWS)
	u.GET("/lessons/:id/board", s.getBoard)
	u.GET("/lessons/:id/chat", s.getChat)
	u.GET("/lessons/:id/code", s.getCode)
	u.POST("/lessons/:id/code/runs", s.createCodeRun)
	u.POST("/lessons/:id/code/runs/:rid/cancel", s.cancelCodeRun)

	// Учебная мастерская
	u.GET("/lessons/:id/workshop", s.getWorkshop)
	u.GET("/lessons/:id/workshop/doc", s.getWorkshopDoc)
	u.PUT("/lessons/:id/workshop/mode", s.setWorkshopMode)
	u.POST("/lessons/:id/workshop/ai-notice", s.ackWorkshopNotice)
	u.POST("/lessons/:id/workshop/analyses", s.createAnalysis)
	u.PUT("/lessons/:id/workshop/analyses/:aid", s.updateAnalysis)
	u.POST("/lessons/:id/workshop/analyses/:aid/publish", s.publishAnalysis(true))
	u.POST("/lessons/:id/workshop/analyses/:aid/unpublish", s.publishAnalysis(false))
	u.DELETE("/lessons/:id/workshop/analyses/:aid", s.deleteAnalysis)
	u.POST("/lessons/:id/workshop/homework", s.createWorkshopHomework)
	u.PUT("/lessons/:id/workshop/homework/:hid", s.updateWorkshopHomework)
	u.POST("/lessons/:id/workshop/homework/:hid/publish", s.publishWorkshopHomework(true))
	u.POST("/lessons/:id/workshop/homework/:hid/unpublish", s.publishWorkshopHomework(false))
	u.DELETE("/lessons/:id/workshop/homework/:hid", s.deleteWorkshopHomework)

	u.POST("/lessons/:id/recordings", requireRole("teacher"), s.requestRecording)
	u.POST("/lessons/:id/recordings/:rid/consent", s.consentRecording)
	u.POST("/lessons/:id/recordings/:rid/stop", requireRole("teacher"), s.stopRecording)
	u.GET("/lessons/:id/recordings/:rid/link", s.recordingLink)

	u.POST("/lessons/:id/materials", requireRole("teacher"), s.uploadMaterial)
	u.DELETE("/lessons/:id/materials/:mid", requireRole("teacher"), s.deleteMaterial)
	u.GET("/lessons/:id/materials/:mid/link", s.materialLink)

	u.POST("/lessons/:id/homework", requireRole("teacher"), s.createHomework)
	u.DELETE("/lessons/:id/homework/:hid", requireRole("teacher"), s.deleteHomework)
	u.GET("/lessons/:id/homework/:hid/link", s.homeworkFileLink)
	u.POST("/lessons/:id/homework/:hid/submission", requireRole("student"), s.submitHomework)
	u.GET("/lessons/:id/homework/:hid/submission/link", s.submissionFileLink)
	u.POST("/lessons/:id/homework/:hid/review", requireRole("teacher"), s.reviewHomework)

	u.POST("/lessons/:id/review", requireRole("student"), s.createReview)

	adm := u.Group("/admin")
	adm.Use(requireRole("admin"))
	adm.GET("/listings", s.adminListings)
	adm.POST("/listings/:id/hide", s.adminHideListing)
	adm.POST("/listings/:id/unhide", s.adminUnhideListing)
	adm.GET("/users", s.adminUsers)
	adm.GET("/stats", s.adminStats)
	adm.POST("/diagnostics/lessons/:id", s.adminDiagnoseLesson)
	adm.GET("/diagnostics/reports", s.adminDiagnosticReports)
	adm.PATCH("/diagnostics/reports/:code", s.adminReviewReport)

	r.NoRoute(func(c *gin.Context) { c.JSON(404, gin.H{"error": "Не найдено"}) })
	return r
}

// ProxyProbeHeader — проверочный запрос deploy/k8s/scripts/set-trusted-proxy.sh: значение (метка из 16–64
// шестнадцатеричных символов) и адрес соединения пишутся в журнал строкой «proxy probe». Так скрипт находит
// адрес своего собственного запроса, а не случайную старую строку журнала. Ответ клиенту не меняется;
// записей не больше 20 в минуту на весь сервер.
const ProxyProbeHeader = "X-Ortalyq-Proxy-Probe"

var proxyProbeLimiter = &limiter{hits: map[string][]time.Time{}}

func isProbeMark(v string) bool {
	if len(v) < 16 || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// untrustedForwardWarning один раз на адрес пишет в журнал, что заголовок X-Forwarded-For / X-Real-IP пришёл
// от адреса не из TRUSTED_PROXIES и проигнорирован. Если это ваш прокси (Nginx/Caddy), добавьте адрес в
// TRUSTED_PROXIES — иначе все клиенты за ним делят один лимит. Если это клиент, пытающийся подменить IP, — ничего
// делать не нужно. Само сообщение адресов клиентов не содержит, только адрес соединения.
// Здесь же обрабатывается проверочный запрос ProxyProbeHeader.
func untrustedForwardWarning(trusted []string) gin.HandlerFunc {
	var nets []*net.IPNet
	for _, t := range trusted {
		if !strings.Contains(t, "/") {
			if strings.Contains(t, ":") {
				t += "/128"
			} else {
				t += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(t); err == nil {
			nets = append(nets, n)
		}
	}
	isTrusted := func(peer string) bool {
		ip := net.ParseIP(peer)
		for _, n := range nets {
			if ip != nil && n.Contains(ip) {
				return true
			}
		}
		return false
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	return func(c *gin.Context) {
		h := c.Request.Header
		if mark := h.Get(ProxyProbeHeader); mark != "" && isProbeMark(mark) && proxyProbeLimiter.allow("probe", 20, time.Minute) {
			peer := c.RemoteIP()
			// Адрес клиента в журнал не пишется: только принят ли адрес из заголовка (forwarded_used).
			slog.Info("proxy probe", "probe", mark, "peer", peer, "trusted", isTrusted(peer), "forwarded_used", c.ClientIP() != peer)
		}
		if h.Get("X-Forwarded-For") == "" && h.Get("X-Real-IP") == "" {
			c.Next()
			return
		}
		if peer := c.RemoteIP(); !isTrusted(peer) {
			mu.Lock()
			first := !seen[peer] && len(seen) < 64
			if first {
				seen[peer] = true
			}
			mu.Unlock()
			if first {
				slog.Warn("forwarded client address ignored: peer is not in TRUSTED_PROXIES (add it if this is your reverse proxy)", "peer", peer)
			}
		}
		c.Next()
	}
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if p := c.Request.URL.Path; p == "/api/health" || p == "/api/ready" {
			return
		}
		slog.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(), "ms", time.Since(start).Milliseconds(), "rid", c.GetString("rid"))
	}
}

// RequestIDHeader — идентификатор запроса в каждом ответе API и в журнале сервера. Пользователь может
// передать его в обращении, а администратор — найти этот запрос в журнале. Секретов не содержит.
const RequestIDHeader = "X-Request-ID"

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		b := make([]byte, 6)
		rand.Read(b)
		rid := hex.EncodeToString(b)
		c.Set("rid", rid)
		c.Writer.Header().Set(RequestIDHeader, rid)
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}

// ServerTimeHeader — серверное время (мс с эпохи UTC) в каждом ответе API. По нему клиент считает
// статус урока, остаток времени и доступность кнопки входа, а не по часам устройства:
// при неверных часах или поясе устройства один урок иначе выглядел бы у участников по-разному.
const ServerTimeHeader = "X-Server-Time"

func serverTimeHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set(ServerTimeHeader, strconv.FormatInt(time.Now().UnixMilli(), 10))
		c.Next()
	}
}

// originCheck — защита от CSRF: изменяющие запросы принимаются только с разрешённых Origin.
// Вебхук LiveKit проверяется собственной подписью.
func (s *Server) originCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		m := c.Request.Method
		if m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions || c.FullPath() == "/api/livekit/webhook" {
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" {
			// Не браузерные клиенты (curl) не отправляют Origin; браузер всегда отправляет его для POST/PUT/DELETE.
			// Дополнительно требуем, чтобы запрос не был «простым» form-запросом.
			if strings.HasPrefix(c.GetHeader("Content-Type"), "application/x-www-form-urlencoded") || strings.HasPrefix(c.GetHeader("Content-Type"), "text/plain") {
				c.AbortWithStatusJSON(403, gin.H{"error": "Запрос отклонён"})
				return
			}
			c.Next()
			return
		}
		if !s.originAllowed(origin, c.Request.Host) {
			c.AbortWithStatusJSON(403, gin.H{"error": "Недопустимый источник запроса"})
			return
		}
		c.Next()
	}
}

func (s *Server) originAllowed(origin, host string) bool {
	origin = strings.TrimRight(origin, "/")
	for _, o := range s.cfg.PublicOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	// Тот же хост (запрос пришёл через обратный прокси с тем же Host)
	if i := strings.Index(origin, "://"); i >= 0 && strings.EqualFold(origin[i+3:], host) {
		return true
	}
	return false
}

func (s *Server) loadSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := c.Cookie(sessionCookie)
		if err != nil || raw == "" {
			c.Next()
			return
		}
		var u ctxUser
		err = s.db.QueryRow(c, `
			SELECT u.id, u.role, u.name, u.email, u.timezone
			FROM sessions s JOIN users u ON u.id = s.user_id
			WHERE s.token_hash = $1 AND s.expires_at > now() AND NOT u.blocked`, auth.HashToken(raw)).
			Scan(&u.ID, &u.Role, &u.Name, &u.Email, &u.Timezone)
		if err == nil {
			c.Set("user", &u)
		}
		c.Next()
	}
}

func currentUser(c *gin.Context) *ctxUser {
	if v, ok := c.Get("user"); ok {
		return v.(*ctxUser)
	}
	return nil
}

func requireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if currentUser(c) == nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "Требуется вход"})
			return
		}
		c.Next()
	}
}

func requireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := currentUser(c)
		for _, r := range roles {
			if u != nil && u.Role == r {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(403, gin.H{"error": "Недостаточно прав"})
	}
}

func fail(c *gin.Context, code int, msg string) {
	c.AbortWithStatusJSON(code, gin.H{"error": msg})
}

func internal(c *gin.Context, err error) {
	if errors.Is(err, context.Canceled) {
		c.Abort()
		return
	}
	slog.Error("internal error", "path", c.Request.URL.Path, "err", err)
	c.AbortWithStatusJSON(500, gin.H{"error": "Внутренняя ошибка сервера"})
}

func (s *Server) publicConfig(c *gin.Context) {
	c.JSON(200, gin.H{
		"videoConfigured":      s.cfg.LiveKitConfigured(),
		"storageConfigured":    s.store != nil,
		"recordingConfigured":  s.cfg.RecordingProblem() == "",
		"recordingProblem":     s.cfg.RecordingProblem(),
		"maxUploadMB":          s.cfg.MaxUploadMB,
		"joinEarlyMinutes":     s.cfg.JoinEarlyMinutes,
		"codeRunnerConfigured": s.runner != nil,
		"workshopAIConfigured": s.ai != nil,
		"version":              s.cfg.AppVersion,
		"demo":                 gin.H{"enabled": s.cfg.DemoEnabled, "minutes": s.cfg.DemoMinutes, "codeRuns": s.cfg.DemoCodeRuns},
	})
}
