package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ortalyq/backend/internal/api"
	"ortalyq/backend/internal/config"
	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/lk"
	"ortalyq/backend/internal/seed"
	"ortalyq/backend/internal/storage"
)

const usage = `Использование: server <команда>

Команды:
  serve          запустить HTTP API (миграции применяются автоматически)
  migrate        применить миграции БД
  seed           пересоздать демо-данные (пароль: DEMO_PASSWORD)
  localize-demo  обновить существующие демо-записи под Казахстан (₸, имена, города); можно запускать повторно
  demo-room      пересоздать только демо-класс (чистая доска и чат), остальные данные не трогаются
  create-admin   создать администратора: -email, -name; пароль из ADMIN_PASSWORD
`

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()

	switch cmd {
	case "migrate":
		if err := db.Migrate(ctx, pool); err != nil {
			fatal(err)
		}
		fmt.Println("Миграции применены")
	case "seed":
		if err := db.Migrate(ctx, pool); err != nil {
			fatal(err)
		}
		pw := os.Getenv("DEMO_PASSWORD")
		if len(pw) < 8 {
			fatal(errors.New("задайте DEMO_PASSWORD (не короче 8 символов) — пароль демо-аккаунтов"))
		}
		if err := seed.Run(ctx, pool, pw); err != nil {
			fatal(err)
		}
		fmt.Println("Демо-данные созданы:")
		fmt.Println("  преподаватель:", seed.TeacherEmail)
		fmt.Println("  ученик:       ", seed.StudentEmail)
		fmt.Println("  второй ученик:", seed.Student2Email, "(для проверки отсутствия доступа к чужому занятию)")
		fmt.Println("  пароль — значение DEMO_PASSWORD")
		fmt.Println("Демо-класс открыт сразу: Кабинет → «Мои занятия» → «Войти в класс»")
	case "localize-demo":
		if err := db.Migrate(ctx, pool); err != nil {
			fatal(err)
		}
		res, err := seed.LocalizeKZ(ctx, pool)
		if err != nil {
			fatal(err)
		}
		fmt.Println("Демо-данные обновлены под Казахстан —", res)
	case "demo-room":
		if err := db.Migrate(ctx, pool); err != nil {
			fatal(err)
		}
		if err := seed.ResetDemoRoom(ctx, pool); err != nil {
			fatal(err)
		}
		fmt.Println("Демо-класс пересоздан: войдите как", seed.TeacherEmail, "и", seed.StudentEmail, "→ «Мои занятия» → «Войти в класс»")
	case "create-admin":
		fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
		email := fs.String("email", "", "email администратора")
		name := fs.String("name", "Администратор", "имя")
		fs.Parse(os.Args[2:])
		pw := os.Getenv("ADMIN_PASSWORD")
		if *email == "" || pw == "" {
			fatal(errors.New("укажите -email и переменную окружения ADMIN_PASSWORD"))
		}
		if err := db.Migrate(ctx, pool); err != nil {
			fatal(err)
		}
		if err := seed.CreateAdmin(ctx, pool, *email, *name, pw); err != nil {
			fatal(err)
		}
		fmt.Println("Администратор создан:", *email)
	case "serve":
		serve(ctx, cfg, pool)
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func serve(ctx context.Context, cfg *config.Config, p *pgxpool.Pool) {
	var err error
	if err := db.Migrate(ctx, p); err != nil {
		fatal(err)
	}

	var store *storage.Store
	if cfg.StorageConfigured() {
		store, err = storage.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3Region, cfg.S3UseSSL)
		if err != nil {
			fatal(err)
		}
		bctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = store.EnsureBucket(bctx)
		cancel()
		if err != nil {
			slog.Error("S3 недоступно — загрузка файлов и записи работать не будут", "err", err)
		}
	} else {
		slog.Warn("S3 не настроено: материалы, домашние файлы и записи недоступны")
	}

	var lkc *lk.Client
	if cfg.LiveKitConfigured() {
		lkc = lk.New(cfg.LiveKitURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
	} else {
		slog.Warn("LiveKit не настроен: видеоклассы недоступны")
	}
	if p := cfg.RecordingProblem(); p != "" {
		slog.Warn("Запись занятий недоступна", "reason", p)
	}

	srv := api.New(cfg, p, store, lkc)
	go srv.Hub().Run(ctx)
	go srv.RunJobs(ctx)

	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv.Router(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpSrv.Shutdown(sctx)
	}()
	slog.Info("API запущен", "addr", cfg.Addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Ошибка:", err)
	os.Exit(1)
}
