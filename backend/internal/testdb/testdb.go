// Package testdb даёт интеграционным тестам изолированную одноразовую базу PostgreSQL.
//
// go test ./... запускает пакеты параллельно. Если все тесты пишут в одну базу TEST_DATABASE_URL,
// данные одного пакета попадают в проверки другого (например, снимок всей БД в тесте seed).
// Поэтому каждый тест получает собственную пустую базу на том же сервере; после теста она удаляется.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ortalyq/backend/internal/db"
)

var unsafe = regexp.MustCompile(`[^a-z0-9]+`)

// New создаёт пустую базу (без миграций) на сервере из TEST_DATABASE_URL и возвращает пул к ней.
// Без TEST_DATABASE_URL тест пропускается. Пользователю базы нужно право CREATEDB
// (в CI это владелец сервиса postgres). TEST_DATABASE_URL должен быть в формате postgres://…
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatalf("TEST_DATABASE_URL должен быть URL вида postgres://…: %v", err)
	}
	suffix := make([]byte, 4)
	rand.Read(suffix)
	name := strings.Trim(unsafe.ReplaceAllString(strings.ToLower(t.Name()), "_"), "_")
	if len(name) > 40 {
		name = name[:40]
	}
	name = "ortalyq_test_" + name + "_" + hex.EncodeToString(suffix) // ≤ 63 символов

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("подключение к TEST_DATABASE_URL: %v", err)
	}
	defer admin.Close(ctx)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		t.Fatalf("CREATE DATABASE %s: %v", name, err)
	}

	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup выполняется в обратном порядке: всё, что тест зарегистрирует позже (HTTP-сервер,
	// фоновые задачи), остановится раньше; затем закрываем пул и удаляем базу.
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Logf("не удалось удалить тестовую базу %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			t.Logf("не удалось удалить тестовую базу %s: %v", name, err)
		}
	})
	return pool
}
