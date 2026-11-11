package api

import (
	"fmt"
	"sort"
	"time"
)

// Расчёт свободного времени преподавателя. Время начала урока — любое с точностью до минуты
// (без сетки 30 минут); урок должен целиком входить в доступность и не пересекаться с занятиями.

type span struct{ From, To time.Time } // полуинтервал [From, To)

// mergeSpans сортирует и объединяет пересекающиеся и соприкасающиеся интервалы
// (окна 10:00–12:00 и 12:00–14:00 дают одно окно 10:00–14:00).
func mergeSpans(in []span) []span {
	s := make([]span, 0, len(in))
	for _, x := range in {
		if x.To.After(x.From) {
			s = append(s, span{x.From.UTC(), x.To.UTC()})
		}
	}
	sort.Slice(s, func(i, j int) bool { return s[i].From.Before(s[j].From) })
	out := []span{}
	for _, x := range s {
		if n := len(out); n > 0 && !x.From.After(out[n-1].To) {
			if x.To.After(out[n-1].To) {
				out[n-1].To = x.To
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

// subtractSpans вычитает из free занятые интервалы busy.
func subtractSpans(free, busy []span) []span {
	busy = mergeSpans(busy)
	out := []span{}
	for _, f := range mergeSpans(free) {
		cur := f.From
		for _, b := range busy {
			if !b.To.After(cur) || !b.From.Before(f.To) {
				continue
			}
			if b.From.After(cur) {
				out = append(out, span{cur, b.From})
			}
			if b.To.After(cur) {
				cur = b.To
			}
		}
		if f.To.After(cur) {
			out = append(out, span{cur, f.To})
		}
	}
	return out
}

// earliestStart — первая целая минута строго позже now (начало урока должно быть в будущем).
func earliestStart(now time.Time) time.Time {
	return now.UTC().Truncate(time.Minute).Add(time.Minute)
}

type freeInterval struct {
	StartsAt    time.Time `json:"startsAt"`    // свободно с (целая минута)
	EndsAt      time.Time `json:"endsAt"`      // свободно до
	LastStartAt time.Time `json:"lastStartAt"` // последнее начало урока выбранной длительности
}

// freeIntervals — доступность минус занятия, начиная с earliestStart(now); только интервалы,
// в которые помещается урок длительностью dur. Любая минута в [StartsAt, LastStartAt] — допустимое начало.
func freeIntervals(avail, busy []span, now time.Time, dur time.Duration) []freeInterval {
	min := earliestStart(now)
	out := []freeInterval{}
	for _, f := range subtractSpans(avail, busy) {
		from := f.From
		if from.Before(min) {
			from = min
		}
		if t := from.Truncate(time.Minute); t.Before(from) {
			from = t.Add(time.Minute)
		}
		last := f.To.Add(-dur).Truncate(time.Minute)
		if last.Before(from) {
			continue
		}
		out = append(out, freeInterval{StartsAt: from, EndsAt: f.To, LastStartAt: last})
	}
	return out
}

// availabilityProblem объясняет, почему урок [start, end) не входит в доступность avail; "" — входит.
func availabilityProblem(avail []span, start, end time.Time, loc *time.Location) string {
	for _, a := range mergeSpans(avail) {
		if a.From.After(start) || !a.To.After(start) {
			continue
		}
		if a.To.Before(end) {
			return fmt.Sprintf("Урок выходит за границу доступности преподавателя: свободное время заканчивается в %s, а урок закончится в %s. Последнее возможное начало — %s.",
				hm(a.To, loc), hm(end, loc), hm(a.To.Add(-end.Sub(start)).Truncate(time.Minute), loc))
		}
		return ""
	}
	return fmt.Sprintf("Преподаватель недоступен %s в %s. Выберите время внутри одного из свободных интервалов.", dm(start, loc), hm(start, loc))
}

func hm(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04") }
func dm(t time.Time, loc *time.Location) string { return t.In(loc).Format("02.01.2006") }

func userLocation(tz string) *time.Location {
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		return loc
	}
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		return time.UTC
	}
	return loc
}

// Диагностика пустой выдачи свободного времени. Использует те же функции, что и freeIntervals
// (mergeSpans, subtractSpans, earliestStart), — отдельной логики времени нет.
// Причин может быть несколько: каждый будущий интервал в горизонте оценивается отдельно.
// Сведения о чужих занятиях (имена, время) сюда не попадают — только счётчики и длительности.
const (
	slotsNoAvailability = "no_availability" // нет будущих интервалов доступности
	slotsLaterOnly      = "later_only"      // в горизонте интервалов нет, но есть более поздние
	slotsBooked         = "booked"          // есть интервалы, всё время которых уже занято
	slotsTooShort       = "too_short"       // есть свободные промежутки, но короче выбранной длительности
	slotsTodayPassed    = "today_passed"    // сегодняшние интервалы уже закончились
)

type slotsDiagnosis struct {
	Reasons        []string   `json:"reasons"`
	MaxFreeMinutes int        `json:"maxFreeMinutes"` // самый длинный свободный промежуток
	WindowsChecked int        `json:"windowsChecked"` // будущих интервалов в горизонте
	BookedWindows  int        `json:"bookedWindows"`  // из них полностью заняты
	ShortWindows   int        `json:"shortWindows"`   // из них со свободными промежутками короче урока
	PassedToday    int        `json:"passedToday"`    // интервалов сегодня, которые уже закончились
	NextWindowAt   *time.Time `json:"nextWindowAt"`   // ближайший интервал за пределами горизонта
}

// futureFree — доступность минус занятия, начиная с earliestStart(now), без учёта длительности урока.
func futureFree(avail, busy []span, now time.Time) []span {
	min := earliestStart(now)
	out := []span{}
	for _, f := range subtractSpans(avail, busy) {
		if f.From.Before(min) {
			f.From = min
		}
		if f.To.After(f.From) {
			out = append(out, f)
		}
	}
	return out
}

// diagnoseSlots объясняет пустую выдачу для урока длительностью dur. avail и busy — те же данные,
// что получает freeIntervals; passedToday и nextWindowAt — дополнительные факты для пояснения.
// Если подходящие интервалы есть, Reasons пуст.
func diagnoseSlots(avail, busy []span, now time.Time, dur time.Duration, passedToday int, nextWindowAt *time.Time) slotsDiagnosis {
	d := slotsDiagnosis{Reasons: []string{}, PassedToday: passedToday, NextWindowAt: nextWindowAt}
	if len(freeIntervals(avail, busy, now, dur)) > 0 {
		return d
	}
	min := earliestStart(now)
	for _, w := range mergeSpans(avail) {
		if !w.To.After(min) {
			continue
		}
		if w.From.Before(min) {
			w.From = min
		}
		d.WindowsChecked++
		free := futureFree([]span{w}, busy, now)
		if len(free) == 0 {
			d.BookedWindows++
			continue
		}
		longest := 0
		for _, f := range free {
			if m := int(f.To.Sub(f.From) / time.Minute); m > longest {
				longest = m
			}
		}
		if longest > d.MaxFreeMinutes {
			d.MaxFreeMinutes = longest
		}
		d.ShortWindows++
	}
	switch {
	case d.WindowsChecked == 0 && nextWindowAt != nil:
		d.Reasons = append(d.Reasons, slotsLaterOnly)
	case d.WindowsChecked == 0:
		d.Reasons = append(d.Reasons, slotsNoAvailability)
	}
	if d.ShortWindows > 0 {
		d.Reasons = append(d.Reasons, slotsTooShort)
	}
	if d.BookedWindows > 0 {
		d.Reasons = append(d.Reasons, slotsBooked)
	}
	if passedToday > 0 {
		d.Reasons = append(d.Reasons, slotsTodayPassed)
	}
	return d
}
