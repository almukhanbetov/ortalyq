package workshop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request — один запрос к ИИ: системные инструкции, данные урока и JSON-схема ответа.
type Request struct {
	Instructions string
	Input        string
	SchemaName   string
	Schema       map[string]any
}

type Response struct {
	Text  string // JSON по схеме
	Model string
}

// Provider — внешний ИИ. В тестах подменяется; реальная реализация — OpenAI.
type Provider interface {
	Generate(ctx context.Context, r Request) (Response, error)
}

// ErrorKind — категория ошибки провайдера. В интерфейс попадает только понятное сообщение по категории,
// без тела ответа провайдера, ключей и содержимого документа.
type ErrorKind string

const (
	ErrTimeout     ErrorKind = "timeout"
	ErrUnavailable ErrorKind = "unavailable"
	ErrRateLimited ErrorKind = "rate_limited"
	ErrQuota       ErrorKind = "quota"
	ErrAuth        ErrorKind = "auth"
	ErrRefused     ErrorKind = "refused"
	ErrIncomplete  ErrorKind = "incomplete"
	ErrBadResponse ErrorKind = "invalid_response"
	ErrRejected    ErrorKind = "rejected"
)

type ProviderError struct {
	Kind   ErrorKind
	Status int // HTTP-статус провайдера (0 — не HTTP-ошибка)
}

func (e *ProviderError) Error() string { return "ai provider: " + string(e.Kind) }

// UserMessage — текст для преподавателя.
func (e *ProviderError) UserMessage() string {
	switch e.Kind {
	case ErrTimeout:
		return "ИИ не ответил вовремя. Попробуйте ещё раз или выделите фрагмент поменьше."
	case ErrRateLimited:
		return "Сервис ИИ перегружен или превышен лимит запросов. Попробуйте через минуту."
	case ErrQuota:
		return "Исчерпан лимит использования ИИ у провайдера. Обратитесь к администратору платформы."
	case ErrAuth:
		return "Сервис ИИ отклонил ключ доступа. Обратитесь к администратору платформы."
	case ErrRefused:
		return "ИИ отказался обрабатывать этот фрагмент. Измените выделение или сформулируйте задачу иначе."
	case ErrIncomplete:
		return "Ответ ИИ получился слишком длинным и был обрезан. Выделите фрагмент поменьше."
	case ErrBadResponse:
		return "ИИ вернул ответ в неожиданном формате. Попробуйте ещё раз."
	case ErrRejected:
		return "Сервис ИИ отклонил запрос. Обратитесь к администратору платформы."
	}
	return "Сервис ИИ временно недоступен. Попробуйте позже."
}

func AsProviderError(err error) *ProviderError {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ProviderError{Kind: ErrTimeout}
	}
	if errors.Is(err, ErrInvalid) {
		return &ProviderError{Kind: ErrBadResponse}
	}
	return &ProviderError{Kind: ErrUnavailable}
}

// OpenAI — провайдер на OpenAI Responses API (POST {base}/responses, Structured Outputs json_schema strict).
type OpenAI struct {
	BaseURL         string
	APIKey          string
	Model           string
	ReasoningEffort string
	MaxOutputTokens int
	Timeout         time.Duration
	HTTP            *http.Client
}

const maxProviderBody = 4 << 20

func (o *OpenAI) Generate(ctx context.Context, r Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	body := map[string]any{
		"model":             o.Model,
		"instructions":      r.Instructions,
		"input":             r.Input,
		"max_output_tokens": o.MaxOutputTokens,
		"store":             false, // учебные тексты не сохраняются у провайдера для последующего доступа через API
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": r.SchemaName, "schema": r.Schema, "strict": true,
		}},
	}
	if o.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": o.ReasoningEffort}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return Response{}, &ProviderError{Kind: ErrBadResponse}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/responses", bytes.NewReader(b))
	if err != nil {
		return Response{}, &ProviderError{Kind: ErrUnavailable}
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := o.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// Текст ошибки транспорта не возвращаем наружу (может содержать адреса); категория достаточна.
		if ctx.Err() != nil {
			return Response{}, &ProviderError{Kind: ErrTimeout}
		}
		return Response{}, &ProviderError{Kind: ErrUnavailable}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, &ProviderError{Kind: ErrTimeout}
		}
		return Response{}, &ProviderError{Kind: ErrUnavailable, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, statusError(resp.StatusCode, raw)
	}
	return parseResponses(raw)
}

func statusError(status int, raw []byte) error {
	var e struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	json.Unmarshal(raw, &e)
	code := e.Error.Code + " " + e.Error.Type
	switch {
	case status == 401 || status == 403:
		return &ProviderError{Kind: ErrAuth, Status: status}
	case status == 429 && (strings.Contains(code, "quota") || strings.Contains(code, "limit_exceeded") || strings.Contains(code, "credit")):
		return &ProviderError{Kind: ErrQuota, Status: status}
	case status == 429:
		return &ProviderError{Kind: ErrRateLimited, Status: status}
	case status == 400 || status == 404 || status == 422:
		return &ProviderError{Kind: ErrRejected, Status: status}
	}
	return &ProviderError{Kind: ErrUnavailable, Status: status}
}

func parseResponses(raw []byte) (Response, error) {
	var r struct {
		Status string `json:"status"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Response{}, &ProviderError{Kind: ErrBadResponse}
	}
	var text strings.Builder
	refused := false
	for _, it := range r.Output {
		if it.Type != "message" {
			continue // рассуждения и прочие элементы не нужны
		}
		for _, c := range it.Content {
			switch c.Type {
			case "output_text":
				text.WriteString(c.Text)
			case "refusal":
				refused = true
			}
		}
	}
	switch {
	case refused && text.Len() == 0:
		return Response{}, &ProviderError{Kind: ErrRefused}
	case r.Status == "incomplete":
		return Response{}, &ProviderError{Kind: ErrIncomplete}
	case r.Status != "completed":
		return Response{}, &ProviderError{Kind: ErrUnavailable}
	case text.Len() == 0:
		return Response{}, &ProviderError{Kind: ErrBadResponse}
	}
	return Response{Text: text.String(), Model: r.Model}, nil
}
