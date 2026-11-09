package seed

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Демонстрационные данные для Казахстана. Все имена, телефоны и тексты вымышлены.
// Суммы — целые тенге (KZT). Телефоны вида +7 700 000 00 XX — заведомо демонстрационные.
// Email и пароли демо-аккаунтов не меняются.

const DemoTimezone = "Asia/Almaty"

type demoTeacher struct {
	email, name, city, phone, headline, bio string
	subjects                                []string
	price, exp                              int
}

type demoStudent struct {
	email, name, city, phone string
}

type demoListing struct {
	author, kind, subject, title, body string
	price                              *int
}

var teachers = []demoTeacher{
	{TeacherEmail, "Айгерим Нурланова (ДЕМО)", "Алматы", "+77000000001",
		"Математика для 7–11 классов и подготовка к ЕНТ",
		"Вымышленный демо-преподаватель. Объясняю сложное простыми словами, разбираем задачи на интерактивной доске. Подготовка к ЕНТ и олимпиадам.",
		[]string{"Математика", "Физика"}, 6000, 8},
	{"ivan.demo@demo.ortalyq.local", "Ерлан Жумабаев (ДЕМО)", "Астана", "+77000000002",
		"Английский язык: разговорная практика и IELTS",
		"Вымышленный демо-преподаватель. Разговорный английский, грамматика, подготовка к IELTS. Занятия с упором на живую речь.",
		[]string{"Английский язык"}, 8000, 6},
	{"aigerim.demo@demo.ortalyq.local", "Сауле Касымова (ДЕМО)", "Шымкент", "+77000000003",
		"Химия и биология без зубрёжки",
		"Вымышленный демо-преподаватель. Готовлю к ЕНТ по химии и биологии, много схем и опорных конспектов.",
		[]string{"Химия", "Биология"}, 5000, 5},
	{"oleg.demo@demo.ortalyq.local", "Нурлан Ким (ДЕМО)", "Караганда", "+77000000004",
		"Информатика и Python с нуля",
		"Вымышленный демо-преподаватель. Программирование на Python, алгоритмы, подготовка к ЕНТ по информатике.",
		[]string{"Информатика", "Математика"}, 9000, 10},
	{"maria.demo@demo.ortalyq.local", "Динара Омарова (ДЕМО)", "Алматы", "+77000000005",
		"Казахский язык и история Казахстана",
		"Вымышленный демо-преподаватель. Казахский язык для русскоязычных школьников, грамматика и разговорная практика, подготовка к ЕНТ по истории Казахстана.",
		[]string{"Казахский язык", "История Казахстана"}, 4500, 12},
}

var students = []demoStudent{
	{StudentEmail, "Данияр Сейтов (ДЕМО)", "Алматы", "+77000000011"},
	{Student2Email, "Аружан Бекова (ДЕМО)", "Астана", "+77000000012"},
	{"student3@demo.ortalyq.local", "Тимур Абенов (ДЕМО)", "Караганда", "+77000000013"},
}

var listings = []demoListing{
	{TeacherEmail, "offer", "Математика", "Интенсив по подготовке к ЕНТ: математика",
		"Демо-объявление. 10 занятий по ключевым темам ЕНТ: уравнения, текстовые задачи, геометрия. Домашние задания с разбором.", ptr(6000)},
	{"ivan.demo@demo.ortalyq.local", "offer", "Английский язык", "Разговорный клуб один на один",
		"Демо-объявление. Свободная практика английского на любые темы, исправление ошибок, новый словарь после каждого занятия.", ptr(7000)},
	{"oleg.demo@demo.ortalyq.local", "offer", "Информатика", "Python для школьников: первые проекты",
		"Демо-объявление. За 8 занятий напишем игру и телеграм-бота. Подходит для 6–9 классов.", ptr(9000)},
	{"aigerim.demo@demo.ortalyq.local", "offer", "Химия", "Органическая химия за месяц",
		"Демо-объявление. Систематизируем органику: классы веществ, реакции, цепочки превращений. Подготовка к ЕНТ.", ptr(5000)},
	{StudentEmail, "request", "Физика", "Ищу преподавателя по физике, 10 класс",
		"Демо-объявление. Нужна помощь с механикой и подготовкой к СОР и СОЧ. Удобно по вечерам в будни, я из Алматы.", ptr(5000)},
	{Student2Email, "request", "Английский язык", "Нужна подготовка к IELTS за 2 месяца",
		"Демо-объявление. Текущий уровень B1, цель — 6.5. Особенно нужны Writing и Speaking. Астана, занятия онлайн.", nil},
	{"student3@demo.ortalyq.local", "request", "Математика", "Подтянуть алгебру, 8 класс",
		"Демо-объявление. Ищу спокойного преподавателя, чтобы разобраться с квадратными корнями и уравнениями. Караганда.", ptr(4000)},
}

// LocalizeKZ обновляет уже существующие демо-записи в БД под Казахстан: имена, города, телефоны,
// часовой пояс, профили и цены преподавателей, объявления и цены демо-занятий.
// Затрагиваются только записи с is_demo=true; пользователи, занятия, файлы и записи уроков не удаляются.
// Функцию можно запускать повторно — результат одинаков.
func LocalizeKZ(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var users, profiles, ads int64
	for _, t := range teachers {
		tag, err := tx.Exec(ctx, `UPDATE users SET name=$2, city=$3, phone=$4, timezone=$5 WHERE lower(email)=lower($1) AND is_demo`,
			t.email, t.name, t.city, t.phone, DemoTimezone)
		if err != nil {
			return "", err
		}
		users += tag.RowsAffected()
		tag, err = tx.Exec(ctx, `UPDATE teacher_profiles p SET headline=$2, bio=$3, subjects=$4, price=$5, experience_years=$6, updated_at=now()
			FROM users u WHERE u.id=p.user_id AND lower(u.email)=lower($1) AND u.is_demo`, t.email, t.headline, t.bio, t.subjects, t.price, t.exp)
		if err != nil {
			return "", err
		}
		profiles += tag.RowsAffected()
	}
	for _, s := range students {
		tag, err := tx.Exec(ctx, `UPDATE users SET name=$2, city=$3, phone=$4, timezone=$5 WHERE lower(email)=lower($1) AND is_demo`,
			s.email, s.name, s.city, s.phone, DemoTimezone)
		if err != nil {
			return "", err
		}
		users += tag.RowsAffected()
	}
	for _, l := range listings {
		tag, err := tx.Exec(ctx, `UPDATE listings l SET kind=$2, subject=$3, title=$4, body=$5, price=$6
			FROM users u WHERE u.id=l.author_id AND l.is_demo AND u.is_demo AND lower(u.email)=lower($1)`, l.author, l.kind, l.subject, l.title, l.body, l.price)
		if err != nil {
			return "", err
		}
		ads += tag.RowsAffected()
	}
	// Предмет демо-занятия должен входить в предметы преподавателя (например, «Русский язык» → «Казахский язык»).
	if _, err := tx.Exec(ctx, `UPDATE lessons l SET subject = p.subjects[1]
		FROM teacher_profiles p JOIN users u ON u.id = p.user_id
		WHERE l.is_demo AND u.is_demo AND l.teacher_id = p.user_id AND NOT (l.subject = ANY(p.subjects))`); err != nil {
		return "", err
	}
	// Цена демо-занятия = ставка преподавателя (₸ за 60 минут) × длительность.
	lessonTag, err := tx.Exec(ctx, `UPDATE lessons l SET price = round(p.price * extract(epoch FROM (l.ends_at - l.starts_at)) / 3600)::int
		FROM teacher_profiles p JOIN users u ON u.id = p.user_id
		WHERE l.is_demo AND u.is_demo AND l.teacher_id = p.user_id`)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return fmt.Sprintf("пользователей: %d, профилей: %d, объявлений: %d, занятий: %d", users, profiles, ads, lessonTag.RowsAffected()), nil
}
