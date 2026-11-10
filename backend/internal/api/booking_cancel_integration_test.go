package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Интеграционный тест освобождения времени после отмены (HTTP + PostgreSQL). Требует TEST_DATABASE_URL.
// Будущий урок → отмена (учеником, затем преподавателем) → повторная запись на то же время;
// защита от двойного бронирования сохраняется; прошедшее время не бронируется и не показывается свободным.

func (b *bookEnv) bookID(who, teacher string, start time.Time, dur int) (int, string, string) {
	code, body := b.req(who, "POST", "/api/lessons", map[string]any{"teacherId": teacher, "startsAt": start.Format(time.RFC3339), "duration": dur, "subject": "Математика"})
	msg, _ := body["error"].(string)
	id, _ := body["id"].(string)
	return code, msg, id
}

// canStartAt — есть ли в ответе /slots интервал, в котором можно начать урок в момент start.
func canStartAt(body map[string]any, start time.Time) bool {
	ivs, _ := body["intervals"].([]any)
	for _, x := range ivs {
		iv := x.(map[string]any)
		from, _ := time.Parse(time.RFC3339, iv["startsAt"].(string))
		last, _ := time.Parse(time.RFC3339, iv["lastStartAt"].(string))
		if !start.Before(from) && !start.After(last) {
			return true
		}
	}
	return false
}

func (b *bookEnv) lessonStatus(t *testing.T, id string) (status, cancelledBy string) {
	t.Helper()
	var by *string
	if err := b.pool.QueryRow(context.Background(), `SELECT status, cancelled_by::text FROM lessons WHERE id=$1`, id).Scan(&status, &by); err != nil {
		t.Fatal(err)
	}
	if by != nil {
		cancelledBy = *by
	}
	return
}

func listIDs(body map[string]any) map[string]bool {
	out := map[string]bool{}
	ls, _ := body["lessons"].([]any)
	for _, x := range ls {
		out[x.(map[string]any)["id"].(string)] = true
	}
	return out
}

func TestCancelFreesSlot(t *testing.T) {
	b := setupBooking(t)
	ctx := context.Background()
	start := at(b.day, "10:05")
	slots := func(who string) map[string]any {
		code, body := b.req(who, "GET", "/api/teachers/"+b.teacher+"/slots?duration=60", nil)
		if code != 200 {
			t.Fatalf("slots: %d", code)
		}
		return body
	}

	// 1. Ученик a записывается на 10:05–11:05 — время занято для всех
	code, msg, l1 := b.bookID("a", b.teacher, start, 60)
	if code != 201 {
		t.Fatalf("запись: %d %q", code, msg)
	}
	if canStartAt(slots("b"), start) {
		t.Fatal("занятое время 10:05 показано свободным")
	}
	if code, msg, _ := b.bookID("b", b.teacher, start, 60); code != 409 || !strings.Contains(msg, "другим занятием преподавателя") {
		t.Fatalf("двойная запись до отмены: %d %q", code, msg)
	}

	// 2. Отмена учеником: статус в БД, посторонний и повторная отмена отклоняются
	if code, _ := b.req("b", "POST", "/api/lessons/"+l1+"/cancel", map[string]any{}); code != 404 {
		t.Fatalf("отмена посторонним: %d", code)
	}
	if code, body := b.req("a", "POST", "/api/lessons/"+l1+"/cancel", map[string]any{}); code != 200 {
		t.Fatalf("отмена учеником: %d %v", code, body)
	}
	if st, by := b.lessonStatus(t, l1); st != "cancelled" || by != b.ids["a"] {
		t.Fatalf("после отмены: status=%s cancelled_by=%s", st, by)
	}
	if code, _ := b.req("a", "POST", "/api/lessons/"+l1+"/cancel", map[string]any{}); code != 409 {
		t.Fatalf("повторная отмена: %d", code)
	}

	// 3. Время сразу свободно и для ученика, и в списках занятий
	if body := slots("b"); !canStartAt(body, start) {
		t.Fatalf("после отмены 10:05 не освободилось: %v", body["intervals"])
	}
	if body := slots("a"); len(body["myLessons"].([]any)) != 0 {
		t.Fatalf("отменённое занятие осталось в myLessons: %v", body["myLessons"])
	}
	for _, who := range []string{"a", "t1"} {
		_, up := b.req(who, "GET", "/api/lessons?scope=upcoming", nil)
		_, hist := b.req(who, "GET", "/api/lessons?scope=history", nil)
		if listIDs(up)[l1] || !listIDs(hist)[l1] {
			t.Errorf("%s: отменённое занятие должно быть только в истории", who)
		}
	}

	// 4. Повторная запись на то же время другим учеником; занятие не удалено
	code, msg, l2 := b.bookID("b", b.teacher, start, 60)
	if code != 201 {
		t.Fatalf("повторная запись после отмены учеником: %d %q", code, msg)
	}
	var n int
	b.pool.QueryRow(ctx, `SELECT count(*) FROM lessons WHERE id=$1`, l1).Scan(&n)
	if n != 1 {
		t.Fatal("отменённое занятие удалено из БД")
	}
	// Защита от двойного бронирования снова действует
	if code, _, _ := b.bookID("a", b.teacher, at(b.day, "10:30"), 60); code != 409 {
		t.Fatalf("двойная запись после повторной: %d", code)
	}

	// 5. Отмена преподавателем — и снова запись на то же время (ученик из Москвы)
	if code, body := b.req("t1", "POST", "/api/lessons/"+l2+"/cancel", map[string]any{"reason": "Заболел"}); code != 200 {
		t.Fatalf("отмена преподавателем: %d %v", code, body)
	}
	if st, by := b.lessonStatus(t, l2); st != "cancelled" || by != b.ids["t1"] {
		t.Fatalf("после отмены преподавателем: status=%s cancelled_by=%s", st, by)
	}
	if !canStartAt(slots("msk"), start) {
		t.Fatal("после отмены преподавателем 10:05 не освободилось")
	}

	// Одновременная повторная запись восемью запросами: ровно одна успешна
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _, _ = b.bookID([]string{"msk", "a"}[i%2], b.teacher, start, 60)
		}(i)
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		if c == 201 {
			created++
		} else if c != 409 {
			t.Errorf("неожиданный код %d", c)
		}
	}
	if created != 1 {
		t.Fatalf("одновременная запись после отмены: создано %d (%v)", created, codes)
	}
	b.pool.QueryRow(ctx, `SELECT count(*) FROM lessons WHERE teacher_id=$1 AND status <> 'cancelled'`, b.teacher).Scan(&n)
	if n != 1 {
		t.Fatalf("действующих занятий %d, ожидалось 1", n)
	}

	// 6. Прошедшее время: отмена не открывает его для записи, прошлое не показывается свободным
	past := time.Now().Add(-3 * time.Hour).Truncate(time.Minute)
	if _, err := b.pool.Exec(ctx, `INSERT INTO availability(teacher_id, starts_at, ends_at) VALUES ($1,$2,$3)`,
		b.ids["t2"], past.Add(-time.Hour), time.Now().Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var pastID string
	if err := b.pool.QueryRow(ctx, `INSERT INTO lessons(teacher_id, student_id, subject, starts_at, ends_at) VALUES ($1,$2,'Математика',$3,$4) RETURNING id`,
		b.ids["t2"], b.ids["b"], past, past.Add(time.Hour)).Scan(&pastID); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.req("b", "POST", "/api/lessons/"+pastID+"/cancel", map[string]any{}); code != 409 {
		t.Errorf("отмена прошедшего занятия: %d (ожидалось 409)", code)
	}
	b.pool.Exec(ctx, `UPDATE lessons SET status='cancelled' WHERE id=$1`, pastID) // как фоновая задача «не состоялось»
	if code, msg, _ := b.bookID("a", b.ids["t2"], past, 60); code != 400 || !strings.Contains(msg, "уже прошло") {
		t.Errorf("запись на прошедшее время после отмены: %d %q", code, msg)
	}
	_, body := b.req("a", "GET", "/api/teachers/"+b.ids["t2"]+"/slots?duration=30", nil)
	min := earliestStart(time.Now().Add(-time.Minute))
	for _, x := range body["intervals"].([]any) {
		s, _ := time.Parse(time.RFC3339, x.(map[string]any)["startsAt"].(string))
		if s.Before(min) {
			t.Errorf("прошедшее время показано свободным: %v", x)
		}
	}
}
