package config

import (
	"strings"
	"testing"
)

func setBase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("APP_SECRET", strings.Repeat("x", 32))
}

func TestLoadRequiresDatabaseAndSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("APP_SECRET", strings.Repeat("x", 32))
	if _, err := Load(); err == nil {
		t.Fatal("ожидалась ошибка без DATABASE_URL")
	}
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db")
	t.Setenv("APP_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("ожидалась ошибка при коротком APP_SECRET")
	}
}

func TestLoadOriginsAndEgressDefaults(t *testing.T) {
	setBase(t)
	t.Setenv("PUBLIC_ORIGINS", " https://ortalyq.compserv.cloud/ , ,https://x.test")
	t.Setenv("S3_ENDPOINT", "rustfs:9000")
	t.Setenv("EGRESS_S3_ENDPOINT", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.PublicOrigins) != 2 || c.PublicOrigins[0] != "https://ortalyq.compserv.cloud" {
		t.Fatalf("PublicOrigins = %v", c.PublicOrigins)
	}
	if c.EgressS3Endpoint != "rustfs:9000" || c.EgressS3Region != c.S3Region {
		t.Fatalf("egress defaults: %q %q", c.EgressS3Endpoint, c.EgressS3Region)
	}
}

func TestRecordingProblem(t *testing.T) {
	setBase(t)
	for _, k := range []string{"LIVEKIT_URL", "LIVEKIT_API_KEY", "LIVEKIT_API_SECRET", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "RECORDING_ENABLED", "RECORDING_TEMPLATE_BASE_URL"} {
		t.Setenv(k, "")
	}
	c, _ := Load()
	if c.RecordingProblem() == "" || c.LiveKitConfigured() {
		t.Fatal("без LiveKit запись должна быть недоступна")
	}
	t.Setenv("LIVEKIT_URL", "https://livekit.example")
	t.Setenv("LIVEKIT_API_KEY", "k")
	t.Setenv("LIVEKIT_API_SECRET", "s")
	t.Setenv("S3_ENDPOINT", "rustfs:9000")
	t.Setenv("S3_ACCESS_KEY", "a")
	t.Setenv("S3_SECRET_KEY", "b")
	t.Setenv("RECORDING_ENABLED", "true")
	t.Setenv("RECORDING_TEMPLATE_BASE_URL", "http://frontend:3000/")
	c, _ = Load()
	if p := c.RecordingProblem(); p != "" {
		t.Fatalf("RecordingProblem = %q", p)
	}
	if c.RecordingTemplateBase != "http://frontend:3000" {
		t.Fatalf("RecordingTemplateBase = %q", c.RecordingTemplateBase)
	}
}

func TestAIConfig(t *testing.T) {
	setBase(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.AIProblem() == "" || strings.Contains(c.AIProblem(), "sk-") {
		t.Fatalf("без ключа ИИ должен быть недоступен: %q", c.AIProblem())
	}
	t.Setenv("OPENAI_API_KEY", "sk-test-secret-value")
	if c, _ = Load(); !strings.Contains(c.AIProblem(), "OPENAI_MODEL") {
		t.Fatalf("без модели ИИ должен быть недоступен: %q", c.AIProblem())
	}
	t.Setenv("OPENAI_MODEL", "test-model")
	t.Setenv("OPENAI_TIMEOUT_SECONDS", "999")
	c, _ = Load()
	if c.AIProblem() != "" || c.OpenAITimeoutSeconds != 100 || c.OpenAIBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("AI config: %q %d %q", c.AIProblem(), c.OpenAITimeoutSeconds, c.OpenAIBaseURL)
	}
	if strings.Contains(c.AIProblem(), "sk-test") {
		t.Fatal("ключ не должен попадать в сообщения")
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 127.0.0.1, ::1 ,10.244.0.0/24,172.18.0.0/16, none,")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "127.0.0.1,::1,10.244.0.0/24,172.18.0.0/16" {
		t.Fatalf("got %v", got)
	}
	if got, err := ParseTrustedProxies(""); err != nil || len(got) != 0 {
		t.Fatalf("пусто: %v %v", got, err)
	}
	for _, bad := range []string{"*", "0.0.0.0/0", "::/0", "10.0.0.0/7", "2001::/8", "nginx", "10.0.0.0/33"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("%q должен быть отклонён", bad)
		}
	}
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("APP_SECRET", strings.Repeat("s", 32))
	c, err := Load()
	if err != nil || strings.Join(c.TrustedProxies, ",") != "127.0.0.1,::1" {
		t.Fatalf("по умолчанию — только loopback: %v %v", c, err)
	}
	t.Setenv("TRUSTED_PROXIES", "0.0.0.0/0")
	if _, err := Load(); err == nil {
		t.Fatal("доверие всем адресам должно останавливать запуск")
	}
}
