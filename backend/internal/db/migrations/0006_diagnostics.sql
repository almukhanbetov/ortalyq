-- Обращения встроенной диагностики расписания и занятий.
-- Только новая таблица (расширяющая миграция): предыдущая версия приложения её не использует.
-- Повторная проверка с тем же отпечатком (fingerprint) не создаёт новое обращение, а увеличивает hits.
-- В details хранятся только идентификаторы, коды причин и время — без паролей, токенов и секретов.
CREATE TABLE IF NOT EXISTS diagnostic_reports (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code          text NOT NULL UNIQUE,              -- номер обращения для пользователя, например D-7F3A2C
    kind          text NOT NULL,                     -- user_lesson_check | admin_lesson
    fingerprint   text NOT NULL,                     -- что именно проверялось (для защиты от дублей)
    user_id       uuid REFERENCES users(id) ON DELETE SET NULL,
    lesson_id     uuid REFERENCES lessons(id) ON DELETE SET NULL,
    details       jsonb NOT NULL DEFAULT '{}',
    status        text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    hits          integer NOT NULL DEFAULT 1,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS diagnostic_reports_open_fp ON diagnostic_reports (fingerprint) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS diagnostic_reports_lesson_idx ON diagnostic_reports (lesson_id);
