package api

// Встроенная диагностика занятий: «Почему не вижу занятие?» для участника и отчёт для администратора.
//
// Отдельной логики времени здесь нет — используются те же правила, что в рабочем коде:
//   - списки «Предстоящие»/«История» — lessonScopeCond (тот же SQL, что /api/lessons);
//   - вход в класс — classProblem и joinWindow (те же, что /join и WebSocket);
//   - доступность — mergeSpans (как при бронировании).
// Время сравнивается по серверным часам в абсолютных моментах; в ответе — UTC и пояс профиля.
//
// Данные не изменяются: диагностика только читает (кроме журнала обращений diagnostic_reports).
// Пароли, сессии и токены не читаются и не выводятся; email в отчёте администратора замаскирован.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"ortalyq/backend/internal/db"
)

// querier — пул или транзакция (CLI выполняет диагностику в транзакции только для чтения).
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var lessonCheckLimiter = &limiter{hits: map[string][]time.Time{}}

// ---- Размещение урока в списках и вход ----

// listPlacement — фактическое включение урока в списки участников (тот же SQL, что /api/lessons) и пояснение.
type listPlacement struct {
	Upcoming    bool   `json:"upcoming"`
	History     bool   `json:"history"`
	Explanation string `json:"explanation"`
}

func inScope(ctx context.Context, q querier, lessonID, scope string, now time.Time) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lessons l WHERE l.id=$1 AND `+lessonScopeCond(scope, "$2")+`)`, lessonID, now).Scan(&ok)
	return ok, err
}

// placementExplanation — человекочитаемая причина того, в каком списке урок (по тем же условиям, что lessonScopeCond).
func placementExplanation(l *lessonRow, now time.Time) string {
	switch {
	case l.Status == "cancelled":
		if l.CancelReason != "" {
			return "Урок отменён (" + l.CancelReason + ") — он в «Истории»"
		}
		return "Урок отменён — он в «Истории»"
	case l.Status == "completed":
		return "Урок завершён — он в «Истории»"
	case l.IsTrial:
		return "Бесплатный ознакомительный урок идёт, пока преподаватель его не завершит — он в «Предстоящих»"
	case l.AlwaysOpen:
		return "Демо-класс, открыт всегда — он в «Предстоящих»"
	case !now.Before(l.EndsAt):
		return "Время урока закончилось — он в «Истории» (статус обновится автоматически)"
	case !now.Before(l.StartsAt):
		return "Урок идёт — он в «Предстоящих»"
	}
	return "Урок запланирован — он в «Предстоящих»"
}

// expectedUpcoming — то же условие, что lessonScopeCond("upcoming"), в Go: для перекрёстной проверки.
func expectedUpcoming(l *lessonRow, now time.Time) bool {
	return (l.Status == "scheduled" || l.Status == "live") && (l.AlwaysOpen || l.EndsAt.After(now))
}

type entryCheck struct {
	Allowed bool      `json:"allowed"`
	Problem string    `json:"problem"`
	OpensAt time.Time `json:"opensAt"`
	Closes  time.Time `json:"closesAt"`
}

func (s *Server) entryFor(l *lessonRow, now time.Time) entryCheck {
	opens, closes := s.joinWindow(l)
	p := s.classProblem(l, now)
	return entryCheck{Allowed: p == "", Problem: p, OpensAt: opens, Closes: closes}
}

func (s *Server) loadLesson(ctx context.Context, q querier, id string) (*lessonRow, error) {
	l, err := scanLesson(q.QueryRow(ctx, lessonSelect+" WHERE l.id = $1", id))
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ---- Журнал обращений ----

func newReportCode() string {
	b := make([]byte, 3)
	rand.Read(b)
	return "D-" + strings.ToUpper(hex.EncodeToString(b))
}

func fingerprint(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:16])
}

// recordReport фиксирует обращение. Одинаковый открытый отпечаток не создаёт новую запись — увеличивается hits,
// возвращается тот же номер. Поэтому повторные проверки не размножают обращения.
func recordReport(ctx context.Context, q querier, kind, fp string, userID, lessonID *string, details any) (string, error) {
	d, _ := json.Marshal(details)
	var code string
	var err error
	for i := 0; i < 3; i++ {
		err = q.QueryRow(ctx, `INSERT INTO diagnostic_reports(code, kind, fingerprint, user_id, lesson_id, details) VALUES ($1,$2,$3,$4,$5,$6::jsonb)
			ON CONFLICT (fingerprint) WHERE status='open'
			DO UPDATE SET hits = diagnostic_reports.hits + 1, last_seen_at = now(), details = EXCLUDED.details
			RETURNING code`, newReportCode(), kind, fp, userID, lessonID, string(d)).Scan(&code)
		if c, constraint := db.ConstraintName(err); c == "23505" && constraint == "diagnostic_reports_code_key" {
			continue // совпал случайный номер — пробуем другой
		}
		break
	}
	return code, err
}

// ---- «Почему не вижу занятие?» (участник видит только свои уроки) ----

type ownLessonCheck struct {
	ID            string    `json:"id"`
	Subject       string    `json:"subject"`
	MyRole        string    `json:"myRole"`
	OtherName     string    `json:"otherName"` // второй участник СВОЕГО урока
	StartsAt      time.Time `json:"startsAt"`
	EndsAt        time.Time `json:"endsAt"`
	Status        string    `json:"status"`
	CancelReason  string    `json:"cancelReason"`
	AlwaysOpen    bool      `json:"alwaysOpen"`
	IsTrial       bool      `json:"isTrial"`
	List          string    `json:"list"` // upcoming | history
	Explanation   string    `json:"explanation"`
	CanEnter      bool      `json:"canEnter"`
	EntryProblem  string    `json:"entryProblem"`
	EntryOpensAt  time.Time `json:"entryOpensAt"`  // вход открывается (по серверным правилам)
	EntryClosesAt time.Time `json:"entryClosesAt"` // вход закрывается
}

// ownLessons — уроки, где пользователь участник (только свои), с размещением в списках и правом входа
// по тем же правилам, что /api/lessons и /join. Чужой или несуществующий lessonId дают пустой результат.
func (s *Server) ownLessons(ctx context.Context, u *ctxUser, lessonID, teacherID string, now time.Time) ([]ownLessonCheck, string, error) {
	q := lessonSelect + ` WHERE (l.teacher_id=$1 OR l.student_id=$1)`
	args := []any{u.ID}
	scopeText := "ваши занятия за последние 30 дней и будущие"
	switch {
	case lessonID != "":
		if !isUUID(lessonID) {
			q += ` AND false`
		} else {
			q += ` AND l.id=$2`
			args = append(args, lessonID)
		}
		scopeText = "занятие с указанным номером среди ваших"
	case teacherID != "" && isUUID(teacherID):
		q += ` AND l.teacher_id=$2 AND l.ends_at > $3`
		args = append(args, teacherID, now.Add(-30*24*time.Hour))
		scopeText = "ваши занятия у этого преподавателя за последние 30 дней и будущие"
	default:
		q += ` AND l.ends_at > $2`
		args = append(args, now.Add(-30*24*time.Hour))
	}
	rows, err := s.db.Query(ctx, q+` ORDER BY l.starts_at DESC LIMIT 20`, args...)
	if err != nil {
		return nil, "", err
	}
	var lessons []lessonRow
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		lessons = append(lessons, l)
	}
	rows.Close()

	out := []ownLessonCheck{}
	for i := range lessons {
		l := &lessons[i]
		up, err := inScope(ctx, s.db, l.ID, "upcoming", now)
		if err != nil {
			return nil, "", err
		}
		role, other := "student", l.TeacherName
		if l.TeacherID == u.ID {
			role, other = "teacher", l.StudentName
		}
		list := "history"
		if up {
			list = "upcoming"
		}
		e := s.entryFor(l, now)
		out = append(out, ownLessonCheck{ID: l.ID, Subject: l.Subject, MyRole: role, OtherName: other, StartsAt: l.StartsAt, EndsAt: l.EndsAt,
			Status: l.Status, CancelReason: l.CancelReason, AlwaysOpen: l.AlwaysOpen, IsTrial: l.IsTrial, List: list, Explanation: placementExplanation(l, now),
			CanEnter: e.Allowed, EntryProblem: e.Problem, EntryOpensAt: e.OpensAt, EntryClosesAt: e.Closes})
	}
	// Предстоящие — первыми (ближайшие сначала), затем история (свежие сначала).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].List != out[j].List {
			return out[i].List == "upcoming"
		}
		if out[i].List == "upcoming" {
			return out[i].StartsAt.Before(out[j].StartsAt)
		}
		return out[i].StartsAt.After(out[j].StartsAt)
	})
	return out, scopeText, nil
}

// POST /api/me/lesson-check {lessonId?, teacherId?}
func (s *Server) myLessonCheck(c *gin.Context) {
	u := currentUser(c)
	if u.Role != "student" && u.Role != "teacher" {
		fail(c, 403, "Проверка доступна ученикам и преподавателям")
		return
	}
	if !lessonCheckLimiter.allow(u.ID, 10, time.Minute) {
		fail(c, 429, "Слишком частые проверки — подождите минуту")
		return
	}
	var in struct {
		LessonID  string `json:"lessonId"`
		TeacherID string `json:"teacherId"`
	}
	c.ShouldBindJSON(&in)
	in.LessonID, in.TeacherID = strings.TrimSpace(in.LessonID), strings.TrimSpace(in.TeacherID)
	now := time.Now()
	out, scopeText, err := s.ownLessons(c, u, in.LessonID, in.TeacherID, now)
	if err != nil {
		internal(c, err)
		return
	}
	resp := gin.H{"checked": scopeText, "lessons": out, "serverTime": now}
	if len(out) == 0 {
		// Причину установить нельзя — фиксируем обращение (один номер на пользователя, запрос и день).
		lid := in.LessonID
		if lid != "" && !isUUID(lid) {
			lid = "invalid"
		}
		day := now.In(userLocation(u.Timezone)).Format("2006-01-02")
		fp := fingerprint("user_lesson_check", u.ID, lid, in.TeacherID, day)
		code, err := recordReport(c, s.db, "user_lesson_check", fp, &u.ID, nil, gin.H{
			"role": u.Role, "lessonId": lid, "teacherId": in.TeacherID, "result": "not_found", "checked": scopeText,
		})
		if err != nil {
			internal(c, err)
			return
		}
		resp["reportCode"] = code
	}
	c.JSON(200, resp)
}

// ---- Отчёт администратора ----

type participantReport struct {
	Role        string        `json:"role"` // teacher | student
	UserID      string        `json:"userId"`
	Name        string        `json:"name"`
	EmailMasked string        `json:"emailMasked"`
	AccountRole string        `json:"accountRole"`
	Blocked     bool          `json:"blocked"`
	Timezone    string        `json:"timezone"`
	StartsLocal string        `json:"startsLocal"`
	EndsLocal   string        `json:"endsLocal"`
	Lists       listPlacement `json:"lists"`
}

type finding struct {
	Code       string   `json:"code"`
	Severity   string   `json:"severity"` // info | warning | error
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion"`
	RelatedIDs []string `json:"relatedIds,omitempty"`
}

type lessonReport struct {
	LessonID     string              `json:"lessonId"`
	TeacherID    string              `json:"teacherId"`
	StudentID    string              `json:"studentId"`
	Subject      string              `json:"subject"`
	Status       string              `json:"status"`
	CancelReason string              `json:"cancelReason"`
	CancelledBy  *string             `json:"cancelledBy"`
	IsDemo       bool                `json:"isDemo"`
	AlwaysOpen   bool                `json:"alwaysOpen"`
	IsTrial      bool                `json:"isTrial"`
	StartsAtUTC  time.Time           `json:"startsAtUtc"`
	EndsAtUTC    time.Time           `json:"endsAtUtc"`
	StartedAt    *time.Time          `json:"startedAt"`
	EndedAt      *time.Time          `json:"endedAt"`
	CreatedAt    time.Time           `json:"createdAt"`
	Participants []participantReport `json:"participants"`
	Entry        entryCheck          `json:"entry"`
	Findings     []finding           `json:"findings"`
	ServerTime   time.Time           `json:"serverTime"`
	ReportCode   string              `json:"reportCode,omitempty"`
}

func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at < 0 {
		return "***"
	}
	local := e[:at]
	if len([]rune(local)) > 2 {
		local = string([]rune(local)[:2])
	}
	return local + "***" + e[at:]
}

func localTime(t time.Time, tz string) string {
	return t.In(userLocation(tz)).Format("02.01.2006 15:04 MST") + " (" + userLocation(tz).String() + ")"
}

// DiagnoseLesson — полный отчёт по уроку для администратора (HTTP) и CLI. Только чтение.
func (s *Server) DiagnoseLesson(ctx context.Context, q querier, id string, now time.Time) (*lessonReport, error) {
	l, err := s.loadLesson(ctx, q, id)
	if err != nil {
		return nil, err
	}
	r := &lessonReport{LessonID: l.ID, TeacherID: l.TeacherID, StudentID: l.StudentID, Subject: l.Subject, Status: l.Status, CancelReason: l.CancelReason,
		IsDemo: l.IsDemo, AlwaysOpen: l.AlwaysOpen, IsTrial: l.IsTrial, StartsAtUTC: l.StartsAt.UTC(), EndsAtUTC: l.EndsAt.UTC(), StartedAt: l.StartedAt, EndedAt: l.EndedAt,
		Entry: s.entryFor(l, now), Findings: []finding{}, ServerTime: now.UTC()}
	if err := q.QueryRow(ctx, `SELECT created_at, cancelled_by FROM lessons WHERE id=$1`, id).Scan(&r.CreatedAt, &r.CancelledBy); err != nil {
		return nil, err
	}
	// Все моменты отчёта — в UTC (поля *Utc и createdAt), пояса участников — в participants.
	r.CreatedAt = r.CreatedAt.UTC()
	if r.StartedAt != nil {
		t := r.StartedAt.UTC()
		r.StartedAt = &t
	}
	if r.EndedAt != nil {
		t := r.EndedAt.UTC()
		r.EndedAt = &t
	}
	add := func(f finding) { r.Findings = append(r.Findings, f) }

	up, err := inScope(ctx, q, id, "upcoming", now)
	if err != nil {
		return nil, err
	}
	hist, err := inScope(ctx, q, id, "history", now)
	if err != nil {
		return nil, err
	}
	// Перекрёстная проверка: Go-описание правила совпадает с SQL выдачи (иначе ошибка в правилах).
	if up != expectedUpcoming(l, now) || up == hist {
		add(finding{Code: "list_rule_mismatch", Severity: "error", Message: "Правило списков дало противоречивый результат для этого урока.",
			Suggestion: "Ошибка в коде выдачи списков — сообщите разработчикам с номером обращения."})
	}
	placement := listPlacement{Upcoming: up, History: hist, Explanation: placementExplanation(l, now)}

	for _, p := range []struct{ role, id string }{{"teacher", l.TeacherID}, {"student", l.StudentID}} {
		if p.id == "" && l.IsTrial {
			continue // ознакомительный урок: ученик ещё не присоединился по приглашению
		}
		pr := participantReport{Role: p.role, UserID: p.id, Lists: placement}
		var email string
		err := q.QueryRow(ctx, `SELECT name, email, role, blocked, timezone FROM users WHERE id=$1`, p.id).Scan(&pr.Name, &email, &pr.AccountRole, &pr.Blocked, &pr.Timezone)
		if err != nil {
			if db.IsNoRows(err) {
				add(finding{Code: "missing_user", Severity: "error", Message: "Пользователь " + p.id + " не найден.", Suggestion: "Проверьте связи урока вручную."})
				continue
			}
			return nil, err
		}
		pr.EmailMasked = maskEmail(email)
		pr.StartsLocal, pr.EndsLocal = localTime(l.StartsAt, pr.Timezone), localTime(l.EndsAt, pr.Timezone)
		r.Participants = append(r.Participants, pr)
		if pr.AccountRole != p.role {
			add(finding{Code: "wrong_role", Severity: "error", Message: fmt.Sprintf("У участника-%s роль аккаунта «%s».", map[string]string{"teacher": "преподавателя", "student": "ученика"}[p.role], pr.AccountRole),
				Suggestion: "Проверьте, тот ли аккаунт указан в уроке. student_id и teacher_id не меняются автоматически.", RelatedIDs: []string{p.id}})
		}
		if pr.Blocked {
			add(finding{Code: "blocked_user", Severity: "warning", Message: "Аккаунт участника заблокирован — он не может войти и увидеть урок.",
				Suggestion: "Разблокировать аккаунт, если блокировка ошибочна.", RelatedIDs: []string{p.id}})
		}
		// Одинаковые отображаемые имена — не доказательство одного аккаунта: показываем ID для сверки.
		ns, err := idsOf(ctx, q, `SELECT id FROM users WHERE lower(name)=lower($1) AND id<>$2 ORDER BY created_at`, pr.Name, p.id)
		if err != nil {
			return nil, err
		}
		if len(ns) > 0 {
			add(finding{Code: "namesake_" + p.role, Severity: "info",
				Message:    fmt.Sprintf("Ещё %d аккаунт(а) с тем же отображаемым именем «%s».", len(ns), pr.Name),
				Suggestion: "Сверьте, под каким аккаунтом вошёл пользователь (ID или email в профиле). Урок не переносится по совпадению имени.", RelatedIDs: ns})
		}
	}

	// Пересечения неотменённых уроков (их должны исключать ограничения БД). Ознакомительный урок интервалы
	// расписания не занимает, поэтому пересечения и «дубли по времени» для него не проверяются.
	if l.Status != "cancelled" && !l.IsTrial {
		ov, err := idsOf(ctx, q, `SELECT id FROM lessons WHERE id<>$1 AND status<>'cancelled' AND NOT is_trial AND (teacher_id=$2 OR student_id=$3)
			AND tstzrange(starts_at, ends_at) && tstzrange($4, $5) ORDER BY starts_at`, id, l.TeacherID, l.StudentID, l.StartsAt, l.EndsAt)
		if err != nil {
			return nil, err
		}
		if len(ov) > 0 {
			add(finding{Code: "overlap", Severity: "error", Message: "Есть неотменённые уроки участников, пересекающиеся по времени.",
				Suggestion: "Решите вручную, какой урок действует; отмена — отдельным действием с подтверждением, автоматически ничего не меняется.", RelatedIDs: ov})
		}
	}
	// Подозрительные дубли: та же пара и то же время начала.
	if !l.IsTrial {
		dup, err := idsOf(ctx, q, `SELECT id FROM lessons WHERE id<>$1 AND teacher_id=$2 AND student_id=$3 AND starts_at=$4 ORDER BY created_at`, id, l.TeacherID, l.StudentID, l.StartsAt)
		if err != nil {
			return nil, err
		}
		if len(dup) > 0 {
			add(finding{Code: "duplicate", Severity: "warning", Message: "Есть другие записи той же пары на то же время.",
				Suggestion: "Сравните статусы дублей; лишнюю запись отменяет администратор вручную с подтверждением (история сохраняется).", RelatedIDs: dup})
		}
	}
	// Урок вне текущей доступности — допустимо: интервал могли изменить после записи, бронирование действует.
	if !l.AlwaysOpen && l.Status != "cancelled" {
		var av []span
		rows, err := q.Query(ctx, `SELECT starts_at, ends_at FROM availability WHERE teacher_id=$1 AND ends_at >= $2 AND starts_at <= $3`, l.TeacherID, l.StartsAt, l.EndsAt)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var x span
			if err := rows.Scan(&x.From, &x.To); err != nil {
				rows.Close()
				return nil, err
			}
			av = append(av, x)
		}
		rows.Close()
		inside := false
		for _, w := range mergeSpans(av) {
			if !w.From.After(l.StartsAt) && !w.To.Before(l.EndsAt) {
				inside = true
			}
		}
		if !inside {
			add(finding{Code: "outside_availability", Severity: "info",
				Message:    "Урок не входит в текущие интервалы доступности преподавателя. Вероятно, интервал изменили или удалили после записи.",
				Suggestion: "Исправлять не нужно: изменение доступности не отменяет бронирования. Урок показывается в «Расписании» как «Занято»."})
		}
	}
	// Статус не обновлён после окончания (фоновая задача обновляет его раз в 30 с).
	if (l.Status == "scheduled" || l.Status == "live") && !l.AlwaysOpen && now.Sub(l.EndsAt) > 2*time.Minute {
		add(finding{Code: "stale_status", Severity: "warning", Message: "Время урока прошло, а статус всё ещё «" + l.Status + "».",
			Suggestion: "Проверьте, что фоновые задачи backend работают (RunJobs). Списки уже показывают урок в «Истории»."})
	}
	if l.Status == "cancelled" && l.CancelReason == "" && r.CancelledBy == nil {
		add(finding{Code: "cancel_without_reason", Severity: "info", Message: "Урок отменён без причины и без автора отмены."})
	}
	return r, nil
}

func idsOf(ctx context.Context, q querier, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RecordLessonReport фиксирует найденные предупреждения и ошибки как обращение (без дублей: отпечаток —
// урок и набор кодов). Возвращает "" если фиксировать нечего.
func RecordLessonReport(ctx context.Context, q querier, r *lessonReport, adminID *string) (string, error) {
	var codes []string
	for _, f := range r.Findings {
		if f.Severity != "info" {
			codes = append(codes, f.Code)
		}
	}
	if len(codes) == 0 {
		return "", nil
	}
	sort.Strings(codes)
	return recordReport(ctx, q, "admin_lesson", fingerprint("admin_lesson", r.LessonID, strings.Join(codes, ",")), adminID, &r.LessonID,
		gin.H{"codes": codes, "status": r.Status})
}

// POST /api/admin/diagnostics/lessons/:id — отчёт по уроку; предупреждения фиксируются как обращение.
func (s *Server) adminDiagnoseLesson(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if !isUUID(id) {
		fail(c, 400, "Некорректный номер занятия: ожидается UUID из 36 символов (например, e94083a8-6ecd-4582-b879-d2fcfb57f668)")
		return
	}
	r, err := s.DiagnoseLesson(c, s.db, id, time.Now())
	if err != nil {
		if db.IsNoRows(err) {
			fail(c, 404, "Занятие с таким номером не найдено")
			return
		}
		internal(c, err)
		return
	}
	u := currentUser(c)
	if r.ReportCode, err = RecordLessonReport(c, s.db, r, &u.ID); err != nil {
		internal(c, err)
		return
	}
	c.JSON(200, gin.H{"report": r})
}

// PrintLessonReport — текстовый отчёт для CLI.
func PrintLessonReport(w io.Writer, r *lessonReport) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }
	p("Занятие %s · %s", r.LessonID, r.Subject)
	p("  статус: %s%s · демо: %v · всегда открыт: %v · ознакомительный: %v", r.Status, map[bool]string{true: " (" + r.CancelReason + ")", false: ""}[r.CancelReason != ""], r.IsDemo, r.AlwaysOpen, r.IsTrial)
	p("  время UTC: %s – %s", r.StartsAtUTC.Format("2006-01-02 15:04"), r.EndsAtUTC.Format("15:04"))
	p("  создано: %s UTC · сервер сейчас: %s UTC", r.CreatedAt.UTC().Format("2006-01-02 15:04:05"), r.ServerTime.Format("2006-01-02 15:04:05"))
	for _, x := range r.Participants {
		p("  %s %s · %s · %s · аккаунт: %s%s", map[string]string{"teacher": "преподаватель", "student": "ученик"}[x.Role], x.UserID, x.Name, x.EmailMasked, x.AccountRole,
			map[bool]string{true: " · ЗАБЛОКИРОВАН", false: ""}[x.Blocked])
		p("    в его поясе: %s – %s", x.StartsLocal, x.EndsLocal)
		p("    «Предстоящие»: %v · «История»: %v — %s", x.Lists.Upcoming, x.Lists.History, x.Lists.Explanation)
	}
	if r.Entry.Allowed {
		p("  вход в класс: разрешён (окно до %s UTC)", r.Entry.Closes.UTC().Format("2006-01-02 15:04"))
	} else {
		p("  вход в класс: закрыт — %s", r.Entry.Problem)
	}
	if len(r.Findings) == 0 {
		p("  несоответствий не найдено")
	}
	for _, f := range r.Findings {
		p("  [%s] %s: %s", f.Severity, f.Code, f.Message)
		if f.Suggestion != "" {
			p("    → %s", f.Suggestion)
		}
		if len(f.RelatedIDs) > 0 {
			p("    ID: %s", strings.Join(f.RelatedIDs, ", "))
		}
	}
	if r.ReportCode != "" {
		p("  обращение: %s", r.ReportCode)
	}
}
