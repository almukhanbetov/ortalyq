package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr          string
	DatabaseURL   string
	AppSecret     string   // ключ HMAC для временных ссылок и токенов шаблона записи
	PublicOrigins []string // разрешённые Origin для изменяющих запросов и WebSocket
	CookieSecure  bool
	SessionTTLHrs int

	// LiveKit
	LiveKitURL       string // URL для бэкенда (Twirp API), например http://localhost:7880
	LiveKitPublicURL string // URL для браузера; пусто = wss://<текущий хост>
	LiveKitAPIKey    string
	LiveKitAPISecret string

	// Запись (LiveKit Egress)
	RecordingEnabled      bool
	RecordingTemplateBase string // URL фронтенда, доступный из контейнера egress
	EgressS3Endpoint      string // адрес S3, доступный из контейнера egress
	EgressS3Region        string

	// S3 / MinIO
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3Region    string
	S3UseSSL    bool

	MaxUploadMB      int
	JoinEarlyMinutes int
	JoinLateMinutes  int
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	v := env(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := env(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func Load() (*Config, error) {
	c := &Config{
		Addr:                  env("BACKEND_ADDR", ":18080"),
		DatabaseURL:           env("DATABASE_URL", ""),
		AppSecret:             env("APP_SECRET", ""),
		CookieSecure:          envBool("COOKIE_SECURE", false),
		SessionTTLHrs:         envInt("SESSION_TTL_HOURS", 24*14),
		LiveKitURL:            env("LIVEKIT_URL", ""),
		LiveKitPublicURL:      env("LIVEKIT_PUBLIC_URL", ""),
		LiveKitAPIKey:         env("LIVEKIT_API_KEY", ""),
		LiveKitAPISecret:      env("LIVEKIT_API_SECRET", ""),
		RecordingEnabled:      envBool("RECORDING_ENABLED", false),
		RecordingTemplateBase: strings.TrimRight(env("RECORDING_TEMPLATE_BASE_URL", ""), "/"),
		EgressS3Endpoint:      env("EGRESS_S3_ENDPOINT", ""),
		EgressS3Region:        env("EGRESS_S3_REGION", ""),
		S3Endpoint:            env("S3_ENDPOINT", ""),
		S3AccessKey:           env("S3_ACCESS_KEY", ""),
		S3SecretKey:           env("S3_SECRET_KEY", ""),
		S3Bucket:              env("S3_BUCKET", "ortalyq-private"),
		S3Region:              env("S3_REGION", "us-east-1"),
		S3UseSSL:              envBool("S3_USE_SSL", false),
		MaxUploadMB:           envInt("MAX_UPLOAD_MB", 20),
		JoinEarlyMinutes:      envInt("JOIN_EARLY_MINUTES", 15),
		JoinLateMinutes:       envInt("JOIN_LATE_MINUTES", 60),
	}
	for _, o := range strings.Split(env("PUBLIC_ORIGINS", "http://localhost:13000"), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			c.PublicOrigins = append(c.PublicOrigins, o)
		}
	}
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(c.AppSecret) < 32 {
		return nil, fmt.Errorf("APP_SECRET must be at least 32 characters")
	}
	if c.EgressS3Endpoint == "" {
		c.EgressS3Endpoint = c.S3Endpoint
	}
	if c.EgressS3Region == "" {
		c.EgressS3Region = c.S3Region
	}
	return c, nil
}

func (c *Config) LiveKitConfigured() bool {
	return c.LiveKitURL != "" && c.LiveKitAPIKey != "" && c.LiveKitAPISecret != ""
}

func (c *Config) StorageConfigured() bool {
	return c.S3Endpoint != "" && c.S3AccessKey != "" && c.S3SecretKey != ""
}

// RecordingStatus возвращает пустую строку, если запись доступна, иначе причину на русском.
func (c *Config) RecordingProblem() string {
	switch {
	case !c.LiveKitConfigured():
		return "Видеосервис LiveKit не настроен (LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET)."
	case !c.StorageConfigured():
		return "S3-хранилище не настроено (S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY)."
	case !c.RecordingEnabled:
		return "Запись выключена: нужен запущенный сервис LiveKit Egress и RECORDING_ENABLED=true."
	case c.RecordingTemplateBase == "":
		return "Не задан RECORDING_TEMPLATE_BASE_URL — адрес фронтенда, доступный сервису Egress."
	}
	return ""
}
