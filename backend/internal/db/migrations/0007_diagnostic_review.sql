-- Обращения «Не вижу урок / Не могу войти» с устройства пользователя и их разбор администратором.
-- Расширяющая миграция: только новые столбцы со значениями по умолчанию — предыдущая версия продолжает работать.
--   review_status   — разбор администратором: new (Новое) | in_progress (В работе) | resolved (Решено);
--   confirmed_cause — подтверждённая администратором причина (обязательна для «Решено»);
--   client          — сведения с устройства: версия, страница без параметров, результаты запросов (коды,
--                     идентификаторы запросов), синхронизация времени, браузер. Без паролей, токенов, cookies
--                     и тел запросов/ответов;
--   history         — журнал смены статуса (кто, когда, с какого на какой).
ALTER TABLE diagnostic_reports ADD COLUMN IF NOT EXISTS review_status text NOT NULL DEFAULT 'new';
ALTER TABLE diagnostic_reports ADD COLUMN IF NOT EXISTS confirmed_cause text NOT NULL DEFAULT '';
ALTER TABLE diagnostic_reports ADD COLUMN IF NOT EXISTS app_version text NOT NULL DEFAULT '';
ALTER TABLE diagnostic_reports ADD COLUMN IF NOT EXISTS client jsonb NOT NULL DEFAULT '{}';
ALTER TABLE diagnostic_reports ADD COLUMN IF NOT EXISTS history jsonb NOT NULL DEFAULT '[]';
ALTER TABLE diagnostic_reports ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
DO $$ BEGIN
    ALTER TABLE diagnostic_reports ADD CONSTRAINT diagnostic_reports_review_status_check
        CHECK (review_status IN ('new', 'in_progress', 'resolved'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
CREATE INDEX IF NOT EXISTS diagnostic_reports_review_idx ON diagnostic_reports (review_status, last_seen_at DESC);
