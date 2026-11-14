package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"ortalyq/backend/internal/coderun"
	"ortalyq/backend/internal/db"
)

// Ограничения частоты запусков.
const (
	runsPerUserPerMinute   = 10
	runsPerLessonPerMinute = 20
	codeRunBodyLimit       = coderun.MaxSourceBytes + coderun.MaxStdinBytes + 16<<10
)

var codeRunLimiter = &limiter{hits: map[string][]time.Time{}}

// codeRuns — активные запуски (не больше одного на урок) и общий лимит одновременных запусков.
type codeRuns struct {
	mu       sync.Mutex
	byLesson map[string]string             // lessonID → runID
	cancels  map[string]context.CancelFunc // runID → отмена
	sem      chan struct{}
	ctx      context.Context // завершается при остановке сервера
}

func newCodeRuns(max int) *codeRuns {
	return &codeRuns{byLesson: map[string]string{}, cancels: map[string]context.CancelFunc{}, sem: make(chan struct{}, max), ctx: context.Background()}
}

// StartCodeRuns задаёт контекст жизни сервера для фоновых запусков и помечает запуски,
// прерванные предыдущим перезапуском (бэкенд работает в одном экземпляре).
func (s *Server) StartCodeRuns(ctx context.Context) {
	s.runs.mu.Lock()
	s.runs.ctx = ctx
	s.runs.mu.Unlock()
	if _, err := s.db.Exec(ctx, `UPDATE code_runs SET status=$1, message='Запуск прерван перезапуском сервера', finished_at=now()
		WHERE status=$2`, coderun.StatusInternalError, coderun.StatusRunning); err != nil {
		slog.Warn("reset stale code runs", "err", err)
	}
}

func (s *Server) codeLanguages() []coderun.Language {
	langs := []coderun.Language{{ID: "python", Label: "Python 3", Monaco: "python", Judge0ID: s.cfg.CodeRunnerPythonID}}
	// Остальные языки показываются, только если исполнитель настроен и для них задан language_id.
	if s.runner != nil && s.cfg.CodeRunnerCppID > 0 {
		langs = append(langs, coderun.Language{ID: "cpp", Label: "C++", Monaco: "cpp", Judge0ID: s.cfg.CodeRunnerCppID})
	}
	if s.runner != nil && s.cfg.CodeRunnerJSID > 0 {
		langs = append(langs, coderun.Language{ID: "javascript", Label: "JavaScript", Monaco: "javascript", Judge0ID: s.cfg.CodeRunnerJSID})
	}
	return langs
}

const codeRunSelect = `SELECT r.id, r.user_id, u.name, r.language, r.status, r.stdout, r.stderr, r.message, r.exit_code, r.time_ms,
	r.memory_kb, r.output_truncated, r.source_bytes, r.stdin_bytes, r.created_at, r.finished_at
	FROM code_runs r JOIN users u ON u.id = r.user_id`

type codeRun struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	UserName    string     `json:"userName"`
	Language    string     `json:"language"`
	Status      string     `json:"status"`
	Stdout      string     `json:"stdout"`
	Stderr      string     `json:"stderr"`
	Message     string     `json:"message"`
	ExitCode    *int       `json:"exitCode"`
	TimeMs      *int       `json:"timeMs"`
	MemoryKB    *int       `json:"memoryKb"`
	Truncated   bool       `json:"truncated"`
	SourceBytes int        `json:"sourceBytes"`
	StdinBytes  int        `json:"stdinBytes"`
	CreatedAt   time.Time  `json:"createdAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
}

func scanCodeRun(row interface{ Scan(...any) error }) (codeRun, error) {
	var r codeRun
	err := row.Scan(&r.ID, &r.UserID, &r.UserName, &r.Language, &r.Status, &r.Stdout, &r.Stderr, &r.Message, &r.ExitCode, &r.TimeMs,
		&r.MemoryKB, &r.Truncated, &r.SourceBytes, &r.StdinBytes, &r.CreatedAt, &r.FinishedAt)
	return r, err
}

func (s *Server) loadCodeRun(ctx context.Context, lessonID, runID string) (*codeRun, error) {
	r, err := scanCodeRun(s.db.QueryRow(ctx, codeRunSelect+` WHERE r.id=$1 AND r.lesson_id=$2`, runID, lessonID))
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GET /lessons/:id/code — языки, доступность исполнителя и последний запуск урока.
func (s *Server) getCode(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	var last any
	r, err := scanCodeRun(s.db.QueryRow(c, codeRunSelect+` WHERE r.lesson_id=$1 ORDER BY r.created_at DESC LIMIT 1`, l.ID))
	if err == nil {
		last = r
	} else if !db.IsNoRows(err) {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{
		"languages":        s.codeLanguages(),
		"runnerConfigured": s.runner != nil,
		"lastRun":          last,
		"limits":           s.codeInfoLimits(),
	})
}

// POST /lessons/:id/code/runs — запуск конкретного снимка кода и входных данных.
func (s *Server) createCodeRun(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	if msg := s.classProblem(l, time.Now()); msg != "" {
		fail(c, 409, "Запуск кода недоступен: "+msg)
		return
	}
	if s.runner == nil {
		c.AbortWithStatusJSON(503, gin.H{"error": "Сервис выполнения не настроен", "code": coderun.StatusNotConfigured})
		return
	}
	lang, in, ok := s.decodeCodeRun(c)
	if !ok {
		return
	}
	u := currentUser(c)
	if !codeRunLimiter.allow("u:"+u.ID, runsPerUserPerMinute, time.Minute) || !codeRunLimiter.allow("l:"+l.ID, runsPerLessonPerMinute, time.Minute) {
		fail(c, 429, "Слишком частые запуски — подождите немного")
		return
	}

	rs := s.runs
	rs.mu.Lock()
	if _, busy := rs.byLesson[l.ID]; busy {
		rs.mu.Unlock()
		fail(c, 409, "Предыдущий запуск ещё выполняется")
		return
	}
	select {
	case rs.sem <- struct{}{}:
	default:
		rs.mu.Unlock()
		fail(c, 503, "Сервис выполнения занят — попробуйте через несколько секунд")
		return
	}
	rs.byLesson[l.ID] = "" // резерв до вставки строки
	rs.mu.Unlock()
	release := func(runID string) {
		rs.mu.Lock()
		delete(rs.byLesson, l.ID)
		delete(rs.cancels, runID)
		rs.mu.Unlock()
		<-rs.sem
	}

	sum := sha256.Sum256([]byte(in.Source))
	var runID string
	err := s.db.QueryRow(c, `INSERT INTO code_runs(lesson_id, user_id, language, status, source_sha256, source_bytes, stdin_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, l.ID, u.ID, lang.ID, coderun.StatusRunning, hex.EncodeToString(sum[:]), len(in.Source), len(in.Stdin)).Scan(&runID)
	if err != nil {
		release("")
		internal(c, err)
		return
	}
	run, err := s.loadCodeRun(c, l.ID, runID)
	if err != nil {
		release(runID)
		internal(c, err)
		return
	}

	rctx, cancel := context.WithTimeout(rs.ctx, 30*time.Second+time.Duration(s.cfg.CodeRunWallSeconds*float64(time.Second)))
	rs.mu.Lock()
	rs.byLesson[l.ID] = runID
	rs.cancels[runID] = cancel
	rs.mu.Unlock()
	s.hub.Broadcast(l.ID, gin.H{"type": "code.run", "run": run})

	req := coderun.Request{Language: *lang, Source: in.Source, Stdin: in.Stdin, Limits: s.codeRunLimits()}
	lessonID := l.ID
	go func() {
		defer release(runID)
		defer cancel()
		start := time.Now()
		res, err := s.runner.Run(rctx, req)
		if err != nil {
			// Без исходного кода, входных данных и тела ответа исполнителя.
			slog.Warn("code run", "lesson", lessonID, "run", runID, "status", res.Status, "err", err)
		}
		slog.Info("code run finished", "lesson", lessonID, "run", runID, "lang", req.Language.ID, "status", res.Status, "ms", time.Since(start).Milliseconds())
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		_, err = s.db.Exec(dctx, `UPDATE code_runs SET status=$3, stdout=$4, stderr=$5, message=$6, exit_code=$7, time_ms=$8, memory_kb=$9,
			output_truncated=$10, finished_at=now() WHERE id=$1 AND lesson_id=$2`,
			runID, lessonID, res.Status, res.Stdout, res.Stderr, res.Message, res.ExitCode, res.TimeMs, res.MemoryKB, res.Truncated)
		if err != nil {
			slog.Error("save code run", "run", runID, "err", err)
			return
		}
		if final, err := s.loadCodeRun(dctx, lessonID, runID); err == nil {
			s.hub.Broadcast(lessonID, gin.H{"type": "code.run", "run": final})
		}
	}()
	c.JSON(202, gin.H{"run": run})
}

// POST /lessons/:id/code/runs/:rid/cancel — отмена ожидания результата любым участником урока.
func (s *Server) cancelCodeRun(c *gin.Context) {
	l, _, ok := s.lessonAccess(c)
	if !ok {
		return
	}
	rid := c.Param("rid")
	if !isUUID(rid) {
		fail(c, 404, "Запуск не найден")
		return
	}
	s.runs.mu.Lock()
	cancel := s.runs.cancels[rid]
	active := s.runs.byLesson[l.ID] == rid
	s.runs.mu.Unlock()
	if !active || cancel == nil {
		// Нельзя отменить запуск другого урока: rid ищется только среди активных запусков этого урока.
		fail(c, 409, "Запуск уже завершён")
		return
	}
	cancel()
	c.JSON(200, gin.H{"ok": true})
}

type codeRunInput struct {
	Language string `json:"language"`
	Source   string `json:"source"`
	Stdin    string `json:"stdin"`
}

// decodeCodeRun читает и проверяет запрос запуска (язык, размер, кодировка). Общий для уроков и пробного урока.
func (s *Server) decodeCodeRun(c *gin.Context) (*coderun.Language, codeRunInput, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, codeRunBodyLimit)
	var in codeRunInput
	if err := c.ShouldBindJSON(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			fail(c, 413, "Код или входные данные слишком большие")
		} else {
			fail(c, 400, "Некорректный запрос")
		}
		return nil, in, false
	}
	var lang *coderun.Language
	for _, x := range s.codeLanguages() {
		if x.ID == in.Language {
			lang = &x
			break
		}
	}
	switch {
	case lang == nil:
		fail(c, 400, "Язык не поддерживается")
		return nil, in, false
	case strings.TrimSpace(in.Source) == "":
		fail(c, 400, "Код пустой")
		return nil, in, false
	case len(in.Source) > coderun.MaxSourceBytes:
		fail(c, 413, "Код слишком большой (максимум 64 КБ)")
		return nil, in, false
	case len(in.Stdin) > coderun.MaxStdinBytes:
		fail(c, 413, "Входные данные слишком большие (максимум 64 КБ)")
		return nil, in, false
	case !utf8.ValidString(in.Source) || !utf8.ValidString(in.Stdin):
		fail(c, 400, "Код и входные данные должны быть в UTF-8")
		return nil, in, false
	}
	return lang, in, true
}

// codeRunLimits — ограничения исполнителя из конфигурации.
func (s *Server) codeRunLimits() coderun.Limits {
	return coderun.Limits{CPUSeconds: s.cfg.CodeRunCPUSeconds, WallSeconds: s.cfg.CodeRunWallSeconds, MemoryKB: s.cfg.CodeRunMemoryMB * 1024}
}

// codeInfoLimits — ограничения для интерфейса вкладки «Код».
func (s *Server) codeInfoLimits() gin.H {
	return gin.H{
		"sourceBytes": coderun.MaxSourceBytes, "stdinBytes": coderun.MaxStdinBytes, "outputBytes": coderun.MaxOutputBytes,
		"cpuSeconds": s.cfg.CodeRunCPUSeconds, "wallSeconds": s.cfg.CodeRunWallSeconds, "memoryMb": s.cfg.CodeRunMemoryMB,
	}
}
