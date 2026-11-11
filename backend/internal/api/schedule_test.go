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

// Интервал на сегодня не исключается целиком: отсекается только прошедшее время.
// Сейчас 10.11.2026 19:20 (Алматы), преподаватель доступен сегодня и завтра 19:30–20:30.
func TestFreeIntervalsTodayLikeTomorrow(t *testing.T) {
	today := time.Date(2026, 11, 10, 0, 0, 0, 0, almaty)
	tomorrow := today.AddDate(0, 0, 1)
	now := at(today, "19:20")
	avail := []span{{at(today, "19:30"), at(today, "20:30")}, {at(tomorrow, "19:30"), at(tomorrow, "20:30")}}
	got := freeIntervals(avail, nil, now, 60*time.Minute)
	if len(got) != 2 {
		t.Fatalf("ожидались интервалы на сегодня и завтра: %+v", got)
	}
	for i, day := range []time.Time{today, tomorrow} {
		if !got[i].StartsAt.Equal(at(day, "19:30")) || !got[i].EndsAt.Equal(at(day, "20:30")) || !got[i].LastStartAt.Equal(at(day, "19:30")) {
			t.Errorf("%s: %+v", dm(day, almaty), got[i])
		}
	}
	// Урок 60 мин в 19:30 сегодня целиком входит в доступность.
	if msg := availabilityProblem(avail, at(today, "19:30"), at(today, "20:30"), almaty); msg != "" {
		t.Errorf("19:30–20:30 сегодня: %s", msg)
	}
	// Интервал, начавшийся в прошлом (19:00–20:30), остаётся с первой будущей минуты 19:21.
	got = freeIntervals([]span{{at(today, "19:00"), at(today, "20:30")}}, nil, now, 60*time.Minute)
	if len(got) != 1 || !got[0].StartsAt.Equal(at(today, "19:21")) || !got[0].LastStartAt.Equal(at(today, "19:30")) {
		t.Errorf("19:00–20:30 в 19:20: %+v", got)
	}
}

func TestDiagnoseSlots(t *testing.T) {
	b := time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC) // 12:00 Asia/Almaty
	now := b.Add(55 * time.Minute)                     // 12:55
	at := func(min int) time.Time { return b.Add(time.Duration(min) * time.Minute) }
	has := func(d slotsDiagnosis, r string) bool {
		for _, x := range d.Reasons {
			if x == r {
				return true
			}
		}
		return false
	}
	avail := []span{{at(40), at(150)}}                 // 12:40–14:30
	busy := []span{{at(0), at(60)}, {at(60), at(120)}} // 12:00–13:00 и 13:00–14:00
	if d := diagnoseSlots(avail, busy, now, 60*time.Minute, 0, nil); len(d.Reasons) != 1 || !has(d, slotsTooShort) || d.MaxFreeMinutes != 30 || d.WindowsChecked != 1 {
		t.Fatalf("60 мин: %+v", d)
	}
	if d := diagnoseSlots(avail, busy, now, 30*time.Minute, 0, nil); len(d.Reasons) != 0 {
		t.Fatalf("30 мин помещаются: %+v", d)
	}
	full := append(append([]span{}, busy...), span{at(120), at(150)})
	if d := diagnoseSlots(avail, full, now, 30*time.Minute, 0, nil); len(d.Reasons) != 1 || !has(d, slotsBooked) {
		t.Fatalf("всё занято: %+v", d)
	}
	// Несколько причин: один интервал занят, в другом промежуток короче, сегодняшний интервал уже прошёл
	two := []span{{at(40), at(150)}, {at(24 * 60), at(24*60 + 45)}}
	two2 := append([]span{}, full...)
	if d := diagnoseSlots(two, two2, now, 60*time.Minute, 1, nil); !has(d, slotsBooked) || !has(d, slotsTooShort) || !has(d, slotsTodayPassed) || d.WindowsChecked != 2 || d.MaxFreeMinutes != 45 {
		t.Fatalf("несколько причин: %+v", d)
	}
	if d := diagnoseSlots(avail, busy, at(151), 30*time.Minute, 1, nil); !has(d, slotsNoAvailability) || !has(d, slotsTodayPassed) {
		t.Fatalf("интервал прошёл: %+v", d)
	}
	later := at(30 * 24 * 60)
	if d := diagnoseSlots(nil, nil, now, 30*time.Minute, 0, &later); len(d.Reasons) != 1 || !has(d, slotsLaterOnly) {
		t.Fatalf("только позже горизонта: %+v", d)
	}
	// Граница: урок, заканчивающийся ровно в момент начала интервала, не занимает его
	if d := diagnoseSlots([]span{{at(120), at(180)}}, []span{{at(60), at(120)}}, now, 60*time.Minute, 0, nil); len(d.Reasons) != 0 {
		t.Fatalf("граница [начало, конец): %+v", d)
	}
}
