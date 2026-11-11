package workshop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Ограничения размера. Входные — до обращения к ИИ; выходные — для ответа ИИ и правок преподавателя.
const (
	MaxFragmentRunes     = 4000  // выделенный фрагмент для разбора
	MaxHomeworkTextRunes = 12000 // учебный текст для домашнего задания
	MaxPublishedForHW    = 10    // опубликованных разборов в запросе домашнего задания
	maxAnalysisInHW      = 1500  // символов одного разбора в запросе домашнего задания

	maxHeadingRunes  = 160
	maxBodyRunes     = 6000
	maxSections      = 10
	maxReviewRunes   = 6000
	maxExerciseRunes = 3000
	maxAnswerRunes   = 2000
	maxTipsRunes     = 4000
	HomeworkCount    = 5
)

// Вердикт проверки ответа.
const (
	VerdictNone         = "none"
	VerdictCorrect      = "correct"
	VerdictAcceptable   = "acceptable"
	VerdictError        = "error"
	VerdictInsufficient = "insufficient_data"
)

var verdicts = []string{VerdictNone, VerdictCorrect, VerdictAcceptable, VerdictError, VerdictInsufficient}

type Section struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// Analysis — разбор фрагмента (рекомендация ИИ или её отредактированная преподавателем версия).
type Analysis struct {
	Verdict  string    `json:"verdict"`
	Sections []Section `json:"sections"`
}

type Exercise struct {
	Title       string `json:"title"`
	Instruction string `json:"instruction"`
	Content     string `json:"content"`
}

// HomeworkDraft — то, что увидит ученик после публикации: повторение темы и упражнения (без ответов).
type HomeworkDraft struct {
	Review    string     `json:"review"`
	Exercises []Exercise `json:"exercises"`
}

// AnswerKey — ответы и рекомендации по проверке. Только для преподавателя.
type AnswerKey struct {
	Answers      []string `json:"answers"`
	CheckingTips string   `json:"checkingTips"`
}

// ErrInvalid — структура не прошла проверку (ответ ИИ или правка преподавателя).
var ErrInvalid = errors.New("invalid content")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// cleanText убирает управляющие символы (кроме перевода строки и табуляции), нормализует переводы строк
// и обрезает пробелы по краям. Текст отображается только как текст (без HTML), но мусор не храним.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func tooLong(s string, max int) bool { return utf8.RuneCountInString(s) > max }

// NormalizeAnalysis проверяет и очищает разбор. verdictAction — действие проверки ответа (вердикт обязателен).
func NormalizeAnalysis(a *Analysis, verdictAction bool) error {
	if a == nil {
		return invalid("empty")
	}
	a.Verdict = strings.TrimSpace(a.Verdict)
	if !verdictAction {
		a.Verdict = VerdictNone
	} else if !contains(verdicts, a.Verdict) || a.Verdict == VerdictNone {
		return invalid("verdict")
	}
	out := make([]Section, 0, len(a.Sections))
	for _, s := range a.Sections {
		s.Heading, s.Body = cleanText(s.Heading), cleanText(s.Body)
		if s.Heading == "" && s.Body == "" {
			continue
		}
		if tooLong(s.Heading, maxHeadingRunes) {
			return invalid("heading too long")
		}
		if tooLong(s.Body, maxBodyRunes) {
			return invalid("section too long")
		}
		out = append(out, s)
	}
	if len(out) == 0 || len(out) > maxSections {
		return invalid("sections count")
	}
	a.Sections = out
	return nil
}

// NormalizeHomework проверяет и очищает черновик задания и ключ ответов.
func NormalizeHomework(d *HomeworkDraft, k *AnswerKey) error {
	if d == nil || k == nil {
		return invalid("empty")
	}
	d.Review = cleanText(d.Review)
	if d.Review == "" {
		return invalid("review empty")
	}
	if tooLong(d.Review, maxReviewRunes) {
		return invalid("review too long")
	}
	if len(d.Exercises) != HomeworkCount {
		return invalid("exercises count")
	}
	for i := range d.Exercises {
		e := &d.Exercises[i]
		e.Title, e.Instruction, e.Content = cleanText(e.Title), cleanText(e.Instruction), cleanText(e.Content)
		if e.Instruction == "" {
			return invalid("exercise instruction empty")
		}
		for _, s := range []string{e.Title, e.Instruction, e.Content} {
			if tooLong(s, maxExerciseRunes) {
				return invalid("exercise too long")
			}
		}
	}
	if len(k.Answers) != HomeworkCount {
		return invalid("answers count")
	}
	for i := range k.Answers {
		k.Answers[i] = cleanText(k.Answers[i])
		if tooLong(k.Answers[i], maxAnswerRunes) {
			return invalid("answer too long")
		}
	}
	k.CheckingTips = cleanText(k.CheckingTips)
	if tooLong(k.CheckingTips, maxTipsRunes) {
		return invalid("tips too long")
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---- JSON-схемы ответа (Structured Outputs, strict) ----

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func object(props map[string]any, order ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": order, "additionalProperties": false}
}

func analysisSchema() map[string]any {
	return object(map[string]any{
		"verdict": map[string]any{"type": "string", "enum": verdicts,
			"description": "Только для проверки ответа: correct, acceptable, error или insufficient_data. Для остальных действий — none."},
		"sections": map[string]any{"type": "array", "minItems": 1, "maxItems": maxSections,
			"items": object(map[string]any{"heading": strProp("Заголовок раздела"), "body": strProp("Текст раздела, обычный текст без HTML")}, "heading", "body")},
	}, "verdict", "sections")
}

// homeworkAI — формат ответа ИИ для домашнего задания (ответы разделяются на сервере).
type homeworkAI struct {
	Review       string     `json:"review"`
	Exercises    []Exercise `json:"exercises"`
	Answers      []string   `json:"answers"`
	CheckingTips string     `json:"checking_tips"`
}

func homeworkSchema() map[string]any {
	return object(map[string]any{
		"review": strProp("Краткое повторение темы для ученика"),
		"exercises": map[string]any{"type": "array", "minItems": HomeworkCount, "maxItems": HomeworkCount,
			"items": object(map[string]any{
				"title":       strProp("Короткое название упражнения"),
				"instruction": strProp("Понятная инструкция: что сделать"),
				"content":     strProp("Само задание (текст, предложения, вопросы)"),
			}, "title", "instruction", "content")},
		"answers": map[string]any{"type": "array", "minItems": HomeworkCount, "maxItems": HomeworkCount,
			"items": strProp("Ответ к упражнению с тем же номером"), "description": "Ответы для преподавателя, по одному на упражнение"},
		"checking_tips": strProp("Рекомендации преподавателю по проверке"),
	}, "review", "exercises", "answers", "checking_tips")
}

// ParseAnalysis разбирает и проверяет ответ ИИ для разбора фрагмента.
func ParseAnalysis(text string, verdictAction bool) (Analysis, error) {
	var a Analysis
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, invalid("json")
	}
	return a, NormalizeAnalysis(&a, verdictAction)
}

// ParseHomework разбирает ответ ИИ для домашнего задания и отделяет ответы от того, что увидит ученик.
func ParseHomework(text string) (HomeworkDraft, AnswerKey, error) {
	var h homeworkAI
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return HomeworkDraft{}, AnswerKey{}, invalid("json")
	}
	d := HomeworkDraft{Review: h.Review, Exercises: h.Exercises}
	k := AnswerKey{Answers: h.Answers, CheckingTips: h.CheckingTips}
	return d, k, NormalizeHomework(&d, &k)
}
