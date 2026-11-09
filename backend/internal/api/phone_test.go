package api

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"   ", "", true},
		{"+77000000011", "+77000000011", true},
		{"8 (700) 000-00-11", "+77000000011", true},
		{"+7 700 000 00 11", "+77000000011", true},
		{"87000000011", "+77000000011", true},
		{"12345", "", false},
		{"+7700000001", "", false},   // 10 цифр
		{"+770000000111", "", false}, // 12 цифр
		{"+1 202 555 0100", "", false},
		{"+7700000001a", "", false},
	}
	for _, c := range cases {
		got, ok := normalizePhone(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("normalizePhone(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
