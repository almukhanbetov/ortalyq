-- Совместный редактор кода в классе.
-- Только новые таблицы (расширяющая миграция): предыдущая версия приложения их не использует и продолжает работать.

-- Состояние документа Yjs (код и входные данные) для каждого урока: журнал бинарных обновлений Yjs.
-- Сервер не разбирает их содержимое — только хранит и рассылает. seq — номер последнего принятого обновления.
CREATE TABLE IF NOT EXISTS code_docs (
    lesson_id   uuid PRIMARY KEY REFERENCES lessons(id) ON DELETE CASCADE,
    updates     bytea[] NOT NULL DEFAULT '{}',
    seq         bigint NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Запуски кода. Исходный код и входные данные здесь не хранятся — только их хеш и размер
-- (сам код лежит в code_docs); вывод программы хранится, чтобы показать результат после обновления страницы.
CREATE TABLE IF NOT EXISTS code_runs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id      uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    user_id        uuid NOT NULL REFERENCES users(id),
    language       text NOT NULL,
    status         text NOT NULL DEFAULT 'running',
    stdout         text NOT NULL DEFAULT '',
    stderr         text NOT NULL DEFAULT '',
    message        text NOT NULL DEFAULT '',
    exit_code      integer,
    time_ms        integer,
    memory_kb      integer,
    output_truncated boolean NOT NULL DEFAULT false,
    source_sha256  text NOT NULL,
    source_bytes   integer NOT NULL,
    stdin_bytes    integer NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz
);
CREATE INDEX IF NOT EXISTS code_runs_lesson_idx ON code_runs (lesson_id, created_at DESC);
