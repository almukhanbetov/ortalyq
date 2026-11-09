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
