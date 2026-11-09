package seed

import (
	"regexp"
	"strings"
	"testing"
)

// Демо-данные согласованы: объявления ссылаются на существующих демо-пользователей,
// предмет объявления преподавателя входит в его предметы, телефоны — демо-номера +7.
func TestDemoDataConsistent(t *testing.T) {
	phone := regexp.MustCompile(`^\+77000000\d{3}$`)
	byEmail := map[string][]string{}
	seen := map[string]bool{}
	for _, tc := range teachers {
		if !phone.MatchString(tc.phone) || tc.price <= 0 || tc.price%500 != 0 {
			t.Errorf("преподаватель %s: телефон %q, цена %d", tc.email, tc.phone, tc.price)
		}
		byEmail[tc.email] = tc.subjects
		if seen[tc.name] {
			t.Errorf("повтор имени %s", tc.name)
		}
		seen[tc.name] = true
	}
	isStudent := map[string]bool{}
	for _, s := range students {
		if !phone.MatchString(s.phone) {
			t.Errorf("ученик %s: телефон %q", s.email, s.phone)
		}
		isStudent[s.email] = true
	}
	for _, l := range listings {
		subj, isTeacher := byEmail[l.author]
		switch {
		case isTeacher:
			if l.kind != "offer" {
				t.Errorf("%q: преподаватель публикует только offer", l.title)
			}
			ok := false
			for _, s := range subj {
				ok = ok || s == l.subject
			}
			if !ok {
				t.Errorf("%q: предмет %q не входит в предметы автора", l.title, l.subject)
			}
		case isStudent[l.author]:
			if l.kind != "request" {
				t.Errorf("%q: ученик публикует только request", l.title)
			}
		default:
			t.Errorf("%q: неизвестный автор %s", l.title, l.author)
		}
		if strings.Contains(l.body, "ЕГЭ") || strings.Contains(l.title, "ЕГЭ") {
			t.Errorf("%q: остался ЕГЭ", l.title)
		}
	}
}
