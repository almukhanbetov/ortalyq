package config

import (
	"fmt"
	"os"
	"runtime/debug"
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

	// ИИ «Учебной мастерской» (OpenAI Responses API). Ключ — только серверный секрет, в браузер не передаётся.
	// Пустой ключ или модель — функции ИИ недоступны, совместный текст и сохранённые материалы работают.
	OpenAIAPIKey          string
	OpenAIModel           string
	OpenAIBaseURL         string // по умолчанию https://api.openai.com/v1
	OpenAIReasoningEffort string // необязательно: low, medium, high… (только для моделей с рассуждением)
	OpenAITimeoutSeconds  int    // таймаут одного запроса к провайдеру
	OpenAIMaxOutputTokens int    // ограничение длины ответа (включая токены рассуждения)
	WorkshopAIPerMinute   int    // запросов к ИИ в минуту на преподавателя
	WorkshopAIPerHour     int    // запросов к ИИ в час на урок

	// Пробный урок (ознакомительная демонстрация класса без регистрации). Каждый запуск — отдельная
	// временная комната; содержимое хранится только в памяти и удаляется при завершении.
	DemoEnabled        bool
	DemoMinutes        int // длительность демонстрации по серверному времени
	DemoMaxActive      int // одновременно активных демонстраций на весь бэкенд
	DemoPerIPPerHour   int // новых демонстраций с одного IP в час
	DemoCodeRuns       int // запусков кода на одну демонстрацию (0 — запуск в демо выключен)
	DemoRetentionHours int // через сколько часов после окончания удаляются служебные записи демонстрации

	// Версия приложения для диагностики (APP_VERSION; иначе ревизия сборки из Go build info; иначе "dev").
	AppVersion string
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
		OpenAIAPIKey:          env("OPENAI_API_KEY", ""),
		OpenAIModel:           env("OPENAI_MODEL", ""),
		OpenAIBaseURL:         strings.TrimRight(env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		OpenAIReasoningEffort: env("OPENAI_REASONING_EFFORT", ""),
		OpenAITimeoutSeconds:  envInt("OPENAI_TIMEOUT_SECONDS", 60),
		OpenAIMaxOutputTokens: envInt("OPENAI_MAX_OUTPUT_TOKENS", 4000),
		WorkshopAIPerMinute:   envInt("WORKSHOP_AI_PER_MINUTE", 6),
		WorkshopAIPerHour:     envInt("WORKSHOP_AI_PER_HOUR", 40),
		DemoEnabled:           envBool("DEMO_ENABLED", true),
		DemoMinutes:           envInt("DEMO_MINUTES", 15),
		DemoMaxActive:         envInt("DEMO_MAX_ACTIVE", 30),
		DemoPerIPPerHour:      envInt("DEMO_PER_IP_PER_HOUR", 6),
		DemoCodeRuns:          envInt("DEMO_CODE_RUNS", 20),
		DemoRetentionHours:    envInt("DEMO_RETENTION_HOURS", 24),
		AppVersion:            Version(),
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
	if !strings.HasPrefix(c.OpenAIBaseURL, "https://") && !strings.HasPrefix(c.OpenAIBaseURL, "http://") {
		return nil, fmt.Errorf("OPENAI_BASE_URL must start with http:// or https://")
	}
	// Прокси перед API ждёт ответа не дольше 120 с (deploy/nginx), поэтому таймаут ИИ — до 100 с.
	c.OpenAITimeoutSeconds = max(min(c.OpenAITimeoutSeconds, 100), 5)
	c.OpenAIMaxOutputTokens = max(min(c.OpenAIMaxOutputTokens, 16000), 500)
	c.WorkshopAIPerMinute = max(min(c.WorkshopAIPerMinute, 60), 1)
	c.WorkshopAIPerHour = max(min(c.WorkshopAIPerHour, 1000), 1)
	c.DemoMinutes = max(min(c.DemoMinutes, 60), 1)
	c.DemoMaxActive = max(min(c.DemoMaxActive, 500), 1)
	c.DemoPerIPPerHour = max(min(c.DemoPerIPPerHour, 100), 1)
	c.DemoCodeRuns = max(min(c.DemoCodeRuns, 200), 0)
	c.DemoRetentionHours = max(min(c.DemoRetentionHours, 24*30), 1)
	return c, nil
}

// buildVersion задаётся при сборке образа: -ldflags "-X ortalyq/backend/internal/config.buildVersion=<SHA>"
// (Dockerfile, ARG APP_VERSION; в GitHub Actions — SHA коммита релиза).
var buildVersion = ""

// Version — версия приложения для диагностики: APP_VERSION из окружения, иначе версия из сборки,
// иначе ревизия VCS из Go build info (локальная сборка из git), иначе "dev".
func Version() string {
	if v := env("APP_VERSION", ""); v != "" {
		return v
	}
	if buildVersion != "" && buildVersion != "dev" {
		return buildVersion
	}
	return buildRevision()
}

// buildRevision — короткая ревизия VCS, встроенная Go при сборке из git; "dev", если её нет.
func buildRevision() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, st := range bi.Settings {
			if st.Key == "vcs.revision" && len(st.Value) >= 7 {
				return st.Value[:7]
			}
		}
	}
	return "dev"
}

// AIProblem — почему функции ИИ мастерской недоступны; "" — настроены. Секреты в текст не попадают.
func (c *Config) AIProblem() string {
	switch {
	case c.OpenAIAPIKey == "":
		return "ИИ не настроен на сервере (не задан OPENAI_API_KEY)"
	case c.OpenAIModel == "":
		return "ИИ не настроен на сервере (не задан OPENAI_MODEL)"
	}
	return ""
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
