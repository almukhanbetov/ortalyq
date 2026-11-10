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
	JoinEarlyMinutes int // вход в класс открывается за столько минут до начала; закрывается в момент окончания

	// Исполнитель кода (Judge0 CE на отдельном изолированном сервере). Пустой URL — запуск недоступен.
	CodeRunnerURL        string // например https://runner.internal:2358 (доступен только бэкенду)
	CodeRunnerToken      string // значение AUTHN_TOKEN исполнителя (серверный секрет)
	CodeRunnerAuthHeader string // AUTHN_HEADER исполнителя, по умолчанию X-Auth-Token
	CodeRunnerPythonID   int    // language_id Python 3 в Judge0 (CE 1.13.1: 71 = Python 3.8.1)
	CodeRunnerCppID      int    // 0 — язык не показывается
	CodeRunnerJSID       int    // 0 — язык не показывается
	CodeRunCPUSeconds    float64
	CodeRunWallSeconds   float64
	CodeRunMemoryMB      int
	CodeRunMaxConcurrent int // одновременных запусков на весь бэкенд
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

func envFloat(key string, def float64) float64 {
	v := env(key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return f
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
		CodeRunnerURL:         strings.TrimRight(env("CODE_RUNNER_URL", ""), "/"),
		CodeRunnerToken:       env("CODE_RUNNER_TOKEN", ""),
		CodeRunnerAuthHeader:  env("CODE_RUNNER_AUTH_HEADER", "X-Auth-Token"),
		CodeRunnerPythonID:    envInt("CODE_RUNNER_PYTHON_ID", 71),
		CodeRunnerCppID:       envInt("CODE_RUNNER_CPP_ID", 0),
		CodeRunnerJSID:        envInt("CODE_RUNNER_JS_ID", 0),
		CodeRunCPUSeconds:     envFloat("CODE_RUN_CPU_SECONDS", 2),
		CodeRunWallSeconds:    envFloat("CODE_RUN_WALL_SECONDS", 5),
		CodeRunMemoryMB:       envInt("CODE_RUN_MEMORY_MB", 128),
		CodeRunMaxConcurrent:  envInt("CODE_RUN_MAX_CONCURRENT", 4),
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
	if c.CodeRunnerURL != "" && !strings.HasPrefix(c.CodeRunnerURL, "http://") && !strings.HasPrefix(c.CodeRunnerURL, "https://") {
		return nil, fmt.Errorf("CODE_RUNNER_URL must start with http:// or https://")
	}
	// Ограничения не выше максимумов Judge0 по умолчанию (MAX_CPU_TIME_LIMIT=15, MAX_WALL_TIME_LIMIT=20, MAX_MEMORY_LIMIT=512000 KB).
	c.CodeRunCPUSeconds = min(c.CodeRunCPUSeconds, 10)
	c.CodeRunWallSeconds = max(min(c.CodeRunWallSeconds, 20), 1)
	c.CodeRunMemoryMB = max(min(c.CodeRunMemoryMB, 512), 32)
	c.CodeRunMaxConcurrent = max(min(c.CodeRunMaxConcurrent, 32), 1)
	return c, nil
}

// CodeRunnerConfigured — задан адрес исполнителя кода.
func (c *Config) CodeRunnerConfigured() bool { return c.CodeRunnerURL != "" }

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
