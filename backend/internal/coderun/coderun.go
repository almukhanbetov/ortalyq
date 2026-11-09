// Package coderun — адаптер внешнего сервиса выполнения кода.
//
// Бэкенд НИКОГДА не выполняет пользовательский код сам (нет exec/eval/shell): программа и входные
// данные передаются по HTTP изолированному исполнителю (Judge0 CE на отдельном сервере), а обратно
// приходят stdout/stderr и статус. Ограничения CPU, памяти, числа процессов, времени, размера вывода
// и запрет сети передаются в каждом запросе (а их максимумы задаются в judge0.conf исполнителя).
package coderun

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Размеры, которые принимает бэкенд (проверяются до обращения к исполнителю).
const (
	MaxSourceBytes = 64 << 10
	MaxStdinBytes  = 64 << 10
	MaxOutputBytes = 64 << 10 // stdout и stderr по отдельности; лишнее обрезается
	// Ограничение размера файлов, создаваемых программой (в т.ч. stdout/stderr — это файлы на стороне
	// исполнителя), в КБ. Превышение → SIGXFSZ → статус output_limit.
	maxFileSizeKB = 64
	maxProcesses  = 16
	stackLimitKB  = 64000
)

// Статусы запуска (общие для API, БД и интерфейса).
const (
	StatusRunning       = "running"
	StatusCompleted     = "completed"      // программа завершилась с кодом 0
	StatusRuntimeError  = "runtime_error"  // ненулевой код выхода / сигнал (в Python — в т.ч. SyntaxError)
	StatusCompileError  = "compile_error"  // для компилируемых языков
	StatusTimeLimit     = "time_limit"     // превышен лимит времени
	StatusMemoryLimit   = "memory_limit"   // превышен лимит памяти
	StatusOutputLimit   = "output_limit"   // превышен лимит вывода
	StatusUnavailable   = "unavailable"    // исполнитель недоступен / не ответил
	StatusNotConfigured = "not_configured" // исполнитель не настроен
	StatusCancelled     = "cancelled"
	StatusInternalError = "internal_error" // внутренняя ошибка исполнителя
)

// Language — язык, доступный в редакторе. Judge0ID == 0 — язык не показывается.
type Language struct {
	ID       string `json:"id"`     // python | cpp | javascript
	Label    string `json:"label"`  // для интерфейса
	Monaco   string `json:"monaco"` // идентификатор языка Monaco
	Judge0ID int    `json:"-"`
}

type Limits struct {
	CPUSeconds  float64
	WallSeconds float64
	MemoryKB    int
}

type Request struct {
	Language Language
	Source   string
	Stdin    string
	Limits   Limits
}

type Result struct {
	Status    string
	Stdout    string
	Stderr    string
	Message   string // пояснение исполнителя (например «Exited with error status 1»)
	ExitCode  *int
	TimeMs    *int
	MemoryKB  *int
	Truncated bool // вывод обрезан до MaxOutputBytes
}

// Runner — сервис выполнения. Run блокируется до результата или отмены ctx.
// Ошибка возвращается только вместе с Result.Status (unavailable/cancelled/internal_error).
type Runner interface {
	Run(ctx context.Context, req Request) (Result, error)
}

var ErrUnavailable = errors.New("сервис выполнения недоступен")

// ---------------- Judge0 ----------------

type Judge0 struct {
	base       string
	authHeader string
	token      string
	http       *http.Client
	poll       time.Duration
}

func NewJudge0(baseURL, authHeader, token string) *Judge0 {
	return &Judge0{
		base:       strings.TrimRight(baseURL, "/"),
		authHeader: authHeader,
		token:      token,
		// Таймаут одного HTTP-запроса; общий срок выполнения задаёт ctx.
		http: &http.Client{Timeout: 15 * time.Second},
		poll: 300 * time.Millisecond,
	}
}

type j0Submission struct {
	SourceCode                    string  `json:"source_code"`
	LanguageID                    int     `json:"language_id"`
	Stdin                         string  `json:"stdin"`
	CPUTimeLimit                  float64 `json:"cpu_time_limit"`
	CPUExtraTime                  float64 `json:"cpu_extra_time"`
	WallTimeLimit                 float64 `json:"wall_time_limit"`
	MemoryLimit                   int     `json:"memory_limit"`
	StackLimit                    int     `json:"stack_limit"`
	MaxProcessesAndOrThreads      int     `json:"max_processes_and_or_threads"`
	EnablePerProcessAndThreadTime bool    `json:"enable_per_process_and_thread_time_limit"`
	EnablePerProcessAndThreadMem  bool    `json:"enable_per_process_and_thread_memory_limit"`
	MaxFileSize                   int     `json:"max_file_size"`
	EnableNetwork                 bool    `json:"enable_network"`
	RedirectStderrToStdout        bool    `json:"redirect_stderr_to_stdout"`
	NumberOfRuns                  int     `json:"number_of_runs"`
}

type j0Result struct {
	Stdout        *string `json:"stdout"`
	Stderr        *string `json:"stderr"`
	CompileOutput *string `json:"compile_output"`
	Message       *string `json:"message"`
	ExitCode      *int    `json:"exit_code"`
	Time          *string `json:"time"`
	Memory        *int    `json:"memory"`
	Status        struct {
		ID          int    `json:"id"`
		Description string `json:"description"`
	} `json:"status"`
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func (j *Judge0) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, j.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if j.token != "" {
		req.Header.Set(j.authHeader, j.token)
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		// Тело ответа не логируем и не возвращаем: в нём может быть фрагмент кода пользователя.
		return resp.StatusCode, fmt.Errorf("judge0: HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("judge0: некорректный ответ")
		}
	}
	return resp.StatusCode, nil
}

func ctxResult(ctx context.Context) (Result, error) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return Result{Status: StatusCancelled, Message: "Запуск отменён"}, ctx.Err()
	}
	return Result{Status: StatusUnavailable, Message: "Сервис выполнения не ответил вовремя"}, ErrUnavailable
}

func (j *Judge0) Run(ctx context.Context, r Request) (Result, error) {
	sub := j0Submission{
		SourceCode:               b64(r.Source),
		LanguageID:               r.Language.Judge0ID,
		Stdin:                    b64(r.Stdin),
		CPUTimeLimit:             r.Limits.CPUSeconds,
		CPUExtraTime:             0.5,
		WallTimeLimit:            r.Limits.WallSeconds,
		MemoryLimit:              r.Limits.MemoryKB,
		StackLimit:               stackLimitKB,
		MaxProcessesAndOrThreads: maxProcesses,
		MaxFileSize:              maxFileSizeKB,
		EnableNetwork:            false, // дополнительно ALLOW_ENABLE_NETWORK=false в judge0.conf
		NumberOfRuns:             1,
	}
	var created struct {
		Token string `json:"token"`
	}
	code, err := j.do(ctx, http.MethodPost, "/submissions?base64_encoded=true&wait=false", sub, &created)
	if err != nil {
		if ctx.Err() != nil {
			return ctxResult(ctx)
		}
		switch {
		case code == http.StatusUnauthorized || code == http.StatusForbidden:
			return Result{Status: StatusUnavailable, Message: "Исполнитель отклонил ключ доступа (проверьте CODE_RUNNER_TOKEN)"}, ErrUnavailable
		case code == http.StatusUnprocessableEntity || code == http.StatusBadRequest:
			return Result{Status: StatusInternalError, Message: "Исполнитель отклонил параметры запуска (лимиты или язык)"}, err
		case code == http.StatusServiceUnavailable:
			return Result{Status: StatusUnavailable, Message: "Очередь исполнителя переполнена, попробуйте позже"}, ErrUnavailable
		}
		return Result{Status: StatusUnavailable, Message: "Сервис выполнения недоступен"}, ErrUnavailable
	}
	if created.Token == "" || len(created.Token) > 128 || strings.ContainsAny(created.Token, "/?#") {
		return Result{Status: StatusInternalError, Message: "Исполнитель вернул некорректный ответ"}, errors.New("judge0: bad token")
	}

	path := "/submissions/" + created.Token + "?base64_encoded=true&fields=stdout,stderr,compile_output,message,status,time,memory,exit_code"
	delay := j.poll
	for {
		select {
		case <-ctx.Done():
			return ctxResult(ctx)
		case <-time.After(delay):
		}
		var res j0Result
		if _, err := j.do(ctx, http.MethodGet, path, nil, &res); err != nil {
			if ctx.Err() != nil {
				return ctxResult(ctx)
			}
			return Result{Status: StatusUnavailable, Message: "Связь с сервисом выполнения потеряна"}, ErrUnavailable
		}
		if res.Status.ID == 1 || res.Status.ID == 2 { // In Queue / Processing
			delay = min(delay*3/2, 2*time.Second)
			continue
		}
		return convert(res, r.Limits), nil
	}
}

func decode(p *string) (string, bool) {
	if p == nil || *p == "" {
		return "", true
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(*p, "\n", ""))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Truncate обрезает строку до n байт по границе символа UTF-8.
func Truncate(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i], true
}

func convert(res j0Result, lim Limits) Result {
	out := Result{ExitCode: res.ExitCode, MemoryKB: res.Memory}
	stdout, ok1 := decode(res.Stdout)
	stderr, ok2 := decode(res.Stderr)
	compile, ok3 := decode(res.CompileOutput)
	msg, _ := decode(res.Message)
	if !ok1 || !ok2 || !ok3 {
		return Result{Status: StatusInternalError, Message: "Исполнитель вернул некорректный вывод"}
	}
	if compile != "" {
		stderr = strings.TrimRight(compile, "\n") + "\n" + stderr
	}
	var t1, t2 bool
	out.Stdout, t1 = Truncate(strings.ToValidUTF8(stdout, "�"), MaxOutputBytes)
	out.Stderr, t2 = Truncate(strings.ToValidUTF8(stderr, "�"), MaxOutputBytes)
	out.Truncated = t1 || t2
	out.Message, _ = Truncate(msg, 2000)
	if res.Time != nil {
		var sec float64
		if _, err := fmt.Sscanf(*res.Time, "%g", &sec); err == nil {
			ms := int(sec*1000 + 0.5)
			out.TimeMs = &ms
		}
	}
	// Коды статусов Judge0: GET /statuses (ce.judge0.com).
	switch res.Status.ID {
	case 3, 4: // Accepted / Wrong Answer (expected_output не используется)
		out.Status = StatusCompleted
	case 5:
		out.Status = StatusTimeLimit
	case 6:
		out.Status = StatusCompileError
	case 8: // SIGXFSZ — превышен max_file_size (вывод)
		out.Status = StatusOutputLimit
	case 7, 9, 10, 11, 12:
		out.Status = StatusRuntimeError
		// Превышение cgroup-лимита памяти проявляется как аварийное завершение; Python пишет MemoryError.
		if (res.Memory != nil && lim.MemoryKB > 0 && *res.Memory >= lim.MemoryKB*95/100) || strings.Contains(stderr, "MemoryError") {
			out.Status = StatusMemoryLimit
		}
	default: // 13 Internal Error, 14 Exec Format Error, неизвестные
		out.Status = StatusInternalError
	}
	return out
}
