package api

import (
	"strings"
	"testing"
	"time"
)

var almaty = userLocation("Asia/Almaty")

func at(day time.Time, hhmm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day.Format("2006-01-02")+" "+hhmm, almaty)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestFreeIntervalsAnyMinute(t *testing.T) {
	day := time.Date(2030, 3, 15, 0, 0, 0, 0, almaty)
	now := at(day, "08:00")
	avail := []span{{at(day, "10:00"), at(day, "14:00")}}
	got := freeIntervals(avail, nil, now, 60*time.Minute)
	if len(got) != 1 || !got[0].StartsAt.Equal(at(day, "10:00")) || !got[0].LastStartAt.Equal(at(day, "13:00")) {
		t.Fatalf("10–14, 60 мин: %+v", got)
	}
	// Пример из задания: 10:05, 10:42, 13:00 — допустимы; 13:01 и 09:59 — нет.
	for _, hhmm := range []string{"10:05", "10:42", "13:00"} {
		s := at(day, hhmm)
		if msg := availabilityProblem(avail, s, s.Add(time.Hour), almaty); msg != "" {
			t.Errorf("%s должно быть допустимо: %s", hhmm, msg)
		}
	}
	s := at(day, "13:01")
	if msg := availabilityProblem(avail, s, s.Add(time.Hour), almaty); !strings.Contains(msg, "заканчивается в 14:00") || !strings.Contains(msg, "закончится в 14:01") || !strings.Contains(msg, "Последнее возможное начало — 13:00") {
		t.Errorf("13:01: %q", msg)
	}
	s = at(day, "09:59")
	if msg := availabilityProblem(avail, s, s.Add(time.Hour), almaty); !strings.Contains(msg, "недоступен 15.03.2030 в 09:59") {
		t.Errorf("09:59: %q", msg)
	}
}

func TestFreeIntervalsBusyAdjacentAndNow(t *testing.T) {
	day := time.Date(2030, 3, 15, 0, 0, 0, 0, almaty)
	// Соседние окна 10–12 и 12–14 объединяются: урок 11:30–12:30 допустим.
	avail := []span{{at(day, "12:00"), at(day, "14:00")}, {at(day, "10:00"), at(day, "12:00")}}
	if msg := availabilityProblem(avail, at(day, "11:30"), at(day, "12:30"), almaty); msg != "" {
		t.Fatalf("соседние окна: %s", msg)
	}
	// Занятие 11:07–12:07 делит окно; 45 мин помещается только во второй части.
	busy := []span{{at(day, "11:07"), at(day, "12:07")}}
	got := freeIntervals(avail, busy, at(day, "08:00"), 45*time.Minute)
	if len(got) != 2 || !got[0].LastStartAt.Equal(at(day, "10:22")) || !got[1].StartsAt.Equal(at(day, "12:07")) || !got[1].LastStartAt.Equal(at(day, "13:15")) {
		t.Fatalf("с занятием: %+v", got)
	}
	// Сейчас 10:41:30 → первое возможное начало 10:42 (следующая целая минута).
	now := at(day, "10:41").Add(30 * time.Second)
	got = freeIntervals(avail, nil, now, 60*time.Minute)
	if len(got) != 1 || !got[0].StartsAt.Equal(at(day, "10:42")) {
		t.Fatalf("с учётом текущего времени: %+v", got)
	}
	// Слишком короткий остаток не показывается.
	if got := freeIntervals(avail, nil, at(day, "13:30"), 60*time.Minute); len(got) != 0 {
		t.Fatalf("остаток 30 мин для урока 60 мин: %+v", got)
	}
}
