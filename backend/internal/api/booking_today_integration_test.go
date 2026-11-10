package api

import (
	"testing"
	"time"
)

// Интервал на сегодня (через 10 минут после «сейчас») доступен для записи так же, как такой же интервал завтра.
// Аналог «сейчас 19:20 — преподаватель добавляет на сегодня 19:30–20:30». Требует TEST_DATABASE_URL.
func TestBookingTodayLikeTomorrow(t *testing.T) {
	b := setupBooking(t)
	now := time.Now().In(almaty)
	start := now.Truncate(time.Minute).Add(10 * time.Minute)
	if start.Add(time.Hour).YearDay() != now.YearDay() {
		t.Skip("до конца суток по Алматы меньше 70 минут — интервал на сегодня не помещается")
	}
	teacher := b.ids["t2"]
	tomorrow := start.AddDate(0, 0, 1)
	for _, from := range []time.Time{start, tomorrow} {
		if code, body := b.req("t2", "POST", "/api/my/availability", map[string]any{"startsAt": from.UTC(), "endsAt": from.Add(time.Hour).UTC()}); code != 201 {
			t.Fatalf("добавление %s: %d %v", from, code, body)
		}
	}
	// Свободный интервал сам по себе не является занятием
	for _, who := range []string{"t2", "a"} {
		if _, body := b.req(who, "GET", "/api/lessons?scope=upcoming", nil); len(body["lessons"].([]any)) != 0 {
			t.Fatalf("%s: до записи «Мои занятия» должны быть пустыми: %v", who, body["lessons"])
		}
	}
	slots := func() map[string]bool {
		_, body := b.req("a", "GET", "/api/teachers/"+teacher+"/slots?duration=60", nil)
		out := map[string]bool{}
		for _, x := range body["intervals"].([]any) {
			iv := x.(map[string]any)
			out[iv["startsAt"].(string)+"/"+iv["lastStartAt"].(string)] = true
		}
		return out
	}
	key := func(t time.Time) string { return t.UTC().Format(time.RFC3339) + "/" + t.UTC().Format(time.RFC3339) }
	got := slots()
	if !got[key(start)] || !got[key(tomorrow)] {
		t.Fatalf("ожидались интервалы сегодня %s и завтра %s (начало = последнее начало для 60 мин): %v", key(start), key(tomorrow), got)
	}
	if code, msg := b.book("a", teacher, start, 60); code != 201 {
		t.Fatalf("запись на сегодня %s: %d %s", hm(start, almaty), code, msg)
	}
	for _, who := range []string{"t2", "a"} {
		_, body := b.req(who, "GET", "/api/lessons?scope=upcoming", nil)
		ls := body["lessons"].([]any)
		if len(ls) != 1 {
			t.Fatalf("%s: ожидалось одно занятие: %v", who, ls)
		}
		l := ls[0].(map[string]any)
		s, _ := time.Parse(time.RFC3339, l["startsAt"].(string))
		e, _ := time.Parse(time.RFC3339, l["endsAt"].(string))
		if !s.Equal(start) || !e.Equal(start.Add(time.Hour)) {
			t.Errorf("%s: занятие %v–%v, ожидалось %v–%v", who, s, e, start, start.Add(time.Hour))
		}
	}
	if got := slots(); got[key(start)] || !got[key(tomorrow)] {
		t.Errorf("после записи сегодняшний интервал занят, завтрашний свободен: %v", got)
	}
}
