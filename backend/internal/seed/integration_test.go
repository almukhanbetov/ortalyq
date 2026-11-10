package seed

import (
	"context"
	"testing"
	"time"

	"ortalyq/backend/internal/db"
	"ortalyq/backend/internal/testdb"
)

// Интеграционный тест на реальном PostgreSQL. Запускается, только если задан TEST_DATABASE_URL
// (в CI — сервис postgres). Тест работает в собственной пустой базе (testdb.New): снимок ниже
// охватывает всю БД, и записи параллельно идущих тестов других пакетов в него попадать не должны.
func TestMigrateSeedLocalizeIdempotent(t *testing.T) {
	pool := testdb.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for i := 0; i < 2; i++ { // повторный запуск миграций ничего не меняет
		if err := db.Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate #%d: %v", i+1, err)
		}
	}
	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil || applied < 3 {
		t.Fatalf("schema_migrations: %d, %v", applied, err)
	}

	if err := Run(ctx, pool, "ci-demo-password-123"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	snapshot := func() string {
		var s string
		err := pool.QueryRow(ctx, `SELECT md5(string_agg(x, '|' ORDER BY x)) FROM (
			SELECT u.email||u.name||u.city||u.phone||u.timezone AS x FROM users u
			UNION ALL SELECT l.id::text||l.price::text||l.subject FROM lessons l
			UNION ALL SELECT p.user_id::text||p.price::text FROM teacher_profiles p) t`).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first, err := LocalizeKZ(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot()
	second, err := LocalizeKZ(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || before != snapshot() {
		t.Fatalf("LocalizeKZ не идемпотентна: %q / %q", first, second)
	}

	var name, tz string
	var price int
	if err := pool.QueryRow(ctx, `SELECT u.name, u.timezone, p.price FROM users u JOIN teacher_profiles p ON p.user_id=u.id WHERE u.email=$1`, TeacherEmail).Scan(&name, &tz, &price); err != nil {
		t.Fatal(err)
	}
	if name != "Айгерим Нурланова (ДЕМО)" || tz != "Asia/Almaty" || price != 6000 {
		t.Fatalf("демо-преподаватель: %q %q %d", name, tz, price)
	}
}
