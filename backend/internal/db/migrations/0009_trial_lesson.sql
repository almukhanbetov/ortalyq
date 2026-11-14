-- Бесплатный ознакомительный урок: обычная запись lessons, которую преподаватель начинает в любой момент.
-- Ученика ещё нет — он присоединяется по ссылке-приглашению, поэтому student_id у такого урока может быть пуст.
-- Вход определяется состоянием урока (always_open + status), а не расписанием; интервалы платных
-- бронирований ознакомительный урок не занимает.

-- Гостевая демонстрация (0008_demo) заменена ознакомительным уроком: её таблицы больше не используются.
DROP TABLE IF EXISTS demo_participants;
DROP TABLE IF EXISTS demo_rooms;

ALTER TABLE lessons ADD COLUMN is_trial boolean NOT NULL DEFAULT false;
ALTER TABLE lessons ALTER COLUMN student_id DROP NOT NULL;
ALTER TABLE lessons ADD CONSTRAINT lessons_student_required CHECK (is_trial OR student_id IS NOT NULL);
-- Ознакомительный урок бесплатный и не привязан ко времени расписания.
ALTER TABLE lessons ADD CONSTRAINT lessons_trial_free CHECK (NOT is_trial OR (price = 0 AND always_open));

-- Пересечения по-прежнему запрещены для обычных уроков; ознакомительный урок интервалы не занимает.
ALTER TABLE lessons DROP CONSTRAINT lessons_teacher_no_overlap;
ALTER TABLE lessons ADD CONSTRAINT lessons_teacher_no_overlap EXCLUDE USING gist (
    teacher_id WITH =, tstzrange(starts_at, ends_at) WITH &&
) WHERE (status <> 'cancelled' AND NOT is_trial);
ALTER TABLE lessons DROP CONSTRAINT lessons_student_no_overlap;
ALTER TABLE lessons ADD CONSTRAINT lessons_student_no_overlap EXCLUDE USING gist (
    student_id WITH =, tstzrange(starts_at, ends_at) WITH &&
) WHERE (status <> 'cancelled' AND NOT is_trial);

-- Не больше одного идущего ознакомительного урока у преподавателя: повторное нажатие не создаёт дубликат
-- (и при одновременных запросах).
CREATE UNIQUE INDEX lessons_one_active_trial ON lessons (teacher_id) WHERE is_trial AND status IN ('scheduled', 'live');
