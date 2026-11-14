-- Пробный урок: ознакомительные демонстрации виртуального класса без регистрации.
-- Только новые таблицы (расширяющая миграция). Уроки, пользователи, расписание и оплаты не затрагиваются:
-- демонстрация не создаёт строк в lessons, users, availability и не ссылается на них.
-- Содержимое класса (доска, код, текст, чат) хранится только в памяти бэкенда; здесь — служебные данные,
-- которые удаляются через DEMO_RETENTION_HOURS после окончания.

CREATE TABLE IF NOT EXISTS demo_rooms (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode         text NOT NULL CHECK (mode IN ('solo', 'pair')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    ended_at     timestamptz,
    end_reason   text NOT NULL DEFAULT '' CHECK (end_reason IN ('', 'expired', 'ended')),
    -- SHA-256 от IP создателя с секретом приложения: только для ограничения частоты, сам IP не хранится.
    creator_key  text NOT NULL DEFAULT '',
    CHECK (expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS demo_rooms_active_idx ON demo_rooms (expires_at) WHERE ended_at IS NULL;

-- Участники демонстрации: не больше одного преподавателя и одного ученика (уникальность по роли),
-- поэтому третий участник не может войти даже при одновременных запросах. Роль назначает сервер.
CREATE TABLE IF NOT EXISTS demo_participants (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id     uuid NOT NULL REFERENCES demo_rooms(id) ON DELETE CASCADE,
    role        text NOT NULL CHECK (role IN ('teacher', 'student')),
    name        text NOT NULL,
    token_hash  bytea NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (room_id, role)
);
