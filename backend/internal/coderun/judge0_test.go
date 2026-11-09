package coderun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeJudge0 имитирует API Judge0 CE (POST /submissions, GET /submissions/{token}) с ответами
// в формате документации ce.judge0.com. Код он НЕ выполняет: результат задаёт тест.
type fakeJudge0 struct {
	t       *testing.T
	mu      sync.Mutex
	got     []map[string]any
	result  func(source, stdin string) map[string]any
	pending int // сколько раз ответить «Processing» перед результатом
	polls   int
	token   string
	status  int // код ответа на POST (0 = 201)
}

func (f *fakeJudge0) handler() http.Handler {
	subs := map[string][2]string{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.token != "" && r.Header.Get("X-Auth-Token") != f.token {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/submissions":
			if f.status != 0 {
				w.WriteHeader(f.status)
				return
			}
			if r.URL.Query().Get("base64_encoded") != "true" || r.URL.Query().Get("wait") != "false" {
				f.t.Errorf("unexpected query %s", r.URL.RawQuery)
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			src, _ := base64.StdEncoding.DecodeString(body["source_code"].(string))
			in, _ := base64.StdEncoding.DecodeString(body["stdin"].(string))
			f.mu.Lock()
			f.got = append(f.got, body)
			subs["tok-1"] = [2]string{string(src), string(in)}
			f.mu.Unlock()
			w.WriteHeader(201)
			w.Write([]byte(`{"token":"tok-1"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/submissions/"):
			f.mu.Lock()
			s, ok := subs[strings.TrimPrefix(r.URL.Path, "/submissions/")]
			f.polls++
			wait := f.polls <= f.pending
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(404)
				return
			}
			if wait {
				w.Write([]byte(`{"status":{"id":2,"description":"Processing"}}`))
				return
			}
			res := f.result(s[0], s[1])
			for _, k := range []string{"stdout", "stderr", "compile_output", "message"} {
				if v, ok := res[k].(string); ok {
					res[k] = base64.StdEncoding.EncodeToString([]byte(v))
				}
			}
			json.NewEncoder(w).Encode(res)
		default:
			w.WriteHeader(404)
		}
	})
}

func status(id int, d string) map[string]any { return map[string]any{"id": id, "description": d} }

var py = Language{ID: "python", Judge0ID: 71}
var lim = Limits{CPUSeconds: 2, WallSeconds: 5, MemoryKB: 131072}

func newRunner(t *testing.T, f *fakeJudge0) *Judge0 {
	f.t = t
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	j := NewJudge0(srv.URL, "X-Auth-Token", f.token)
	j.poll = 5 * time.Millisecond
	return j
}

func TestJudge0SuccessSendsLimitsAndStdin(t *testing.T) {
	f := &fakeJudge0{token: "secret-token", pending: 2, result: func(src, in string) map[string]any {
		// Результат программы `name = input(); print("Привет,", name)` при stdin «Айгерим»
		return map[string]any{"stdout": "Привет, " + strings.TrimSpace(in) + "\n", "status": status(3, "Accepted"), "time": "0.012", "memory": 3200, "exit_code": 0}
	}}
	j := newRunner(t, f)
	res, err := j.Run(context.Background(), Request{Language: py, Source: "name = input()\nprint(\"Привет,\", name)\n", Stdin: "Айгерим\n", Limits: lim})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCompleted || res.Stdout != "Привет, Айгерим\n" || res.TimeMs == nil || *res.TimeMs != 12 || *res.ExitCode != 0 {
		t.Fatalf("unexpected result %+v", res)
	}
	b := f.got[0]
	want := map[string]any{"language_id": 71.0, "cpu_time_limit": 2.0, "wall_time_limit": 5.0, "memory_limit": 131072.0,
		"max_processes_and_or_threads": float64(maxProcesses), "max_file_size": float64(maxFileSizeKB), "enable_network": false,
		"number_of_runs": 1.0, "redirect_stderr_to_stdout": false, "enable_per_process_and_thread_time_limit": false}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("%s = %v, want %v", k, b[k], v)
		}
	}
	if f.polls != 3 {
		t.Errorf("polls = %d, want 3 (2×Processing + result)", f.polls)
	}
}

func TestJudge0StatusMapping(t *testing.T) {
	big := strings.Repeat("я", MaxOutputBytes) // 2 байта на символ — превышает лимит
	cases := []struct {
		name   string
		res    map[string]any
		status string
		check  func(Result) bool
	}{
		{"syntax error", map[string]any{"stderr": "  File \"script.py\", line 1\n    print(\nSyntaxError: unexpected EOF while parsing\n", "status": status(11, "Runtime Error (NZEC)"), "exit_code": 1, "message": "Exited with error status 1"},
			StatusRuntimeError, func(r Result) bool { return strings.Contains(r.Stderr, "SyntaxError") && *r.ExitCode == 1 }},
		{"infinite loop", map[string]any{"status": status(5, "Time Limit Exceeded"), "time": "2.004", "message": "Time limit exceeded"},
			StatusTimeLimit, func(r Result) bool { return *r.TimeMs == 2004 }},
		{"output limit (SIGXFSZ)", map[string]any{"stdout": strings.Repeat("x", 1000), "status": status(8, "Runtime Error (SIGXFSZ)")},
			StatusOutputLimit, nil},
		{"big stdout truncated", map[string]any{"stdout": big, "status": status(3, "Accepted")},
			StatusCompleted, func(r Result) bool {
				return r.Truncated && len(r.Stdout) <= MaxOutputBytes && utf8.ValidString(r.Stdout)
			}},
		{"memory", map[string]any{"stderr": "MemoryError\n", "status": status(11, "Runtime Error (NZEC)"), "memory": 131000},
			StatusMemoryLimit, nil},
		{"compile error", map[string]any{"compile_output": "main.cpp:1: error", "status": status(6, "Compilation Error")},
			StatusCompileError, func(r Result) bool { return strings.Contains(r.Stderr, "main.cpp:1") }},
		{"internal", map[string]any{"status": status(13, "Internal Error")}, StatusInternalError, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := newRunner(t, &fakeJudge0{result: func(string, string) map[string]any { return tc.res }})
			res, err := j.Run(context.Background(), Request{Language: py, Source: "x", Limits: lim})
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != tc.status || (tc.check != nil && !tc.check(res)) {
				t.Fatalf("got %+v", res)
			}
		})
	}
}

func TestJudge0Failures(t *testing.T) {
	ctx := context.Background()
	// Неверный ключ
	j := newRunner(t, &fakeJudge0{token: "right"})
	j.token = "wrong"
	if res, err := j.Run(ctx, Request{Language: py, Source: "x", Limits: lim}); !errors.Is(err, ErrUnavailable) || res.Status != StatusUnavailable || !strings.Contains(res.Message, "ключ") {
		t.Fatalf("bad token: %+v %v", res, err)
	}
	// Очередь переполнена
	j = newRunner(t, &fakeJudge0{status: 503})
	if res, _ := j.Run(ctx, Request{Language: py, Source: "x", Limits: lim}); res.Status != StatusUnavailable {
		t.Fatalf("503: %+v", res)
	}
	// Некорректные параметры
	j = newRunner(t, &fakeJudge0{status: 422})
	if res, _ := j.Run(ctx, Request{Language: py, Source: "x", Limits: lim}); res.Status != StatusInternalError {
		t.Fatalf("422: %+v", res)
	}
	// Исполнитель выключен
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if res, err := NewJudge0(url, "X-Auth-Token", "").Run(ctx, Request{Language: py, Source: "x", Limits: lim}); !errors.Is(err, ErrUnavailable) || res.Status != StatusUnavailable {
		t.Fatalf("down: %+v %v", res, err)
	}
	// Исполнитель завис (всегда Processing) — срок ctx
	j = newRunner(t, &fakeJudge0{pending: 1 << 30, result: nil})
	tctx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	if res, _ := j.Run(tctx, Request{Language: py, Source: "x", Limits: lim}); res.Status != StatusUnavailable {
		t.Fatalf("timeout: %+v", res)
	}
	// Отмена пользователем
	j = newRunner(t, &fakeJudge0{pending: 1 << 30})
	cctx, ccancel := context.WithCancel(ctx)
	time.AfterFunc(30*time.Millisecond, ccancel)
	if res, _ := j.Run(cctx, Request{Language: py, Source: "x", Limits: lim}); res.Status != StatusCancelled {
		t.Fatalf("cancel: %+v", res)
	}
}

func TestTruncateUTF8(t *testing.T) {
	s, cut := Truncate("абв", 3)
	if !cut || s != "а" {
		t.Fatalf("%q %v", s, cut)
	}
	if s, cut := Truncate("abc", 3); cut || s != "abc" {
		t.Fatal("no cut expected")
	}
}
