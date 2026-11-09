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
