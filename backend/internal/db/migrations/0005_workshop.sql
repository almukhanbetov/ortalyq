-- «Учебная мастерская» в классе: совместный учебный текст, разборы фрагментов и домашние задания.
-- Только новые таблицы (расширяющая миграция): предыдущая версия приложения их не использует и продолжает работать.
-- Существующие таблицы (code_docs, boards, homeworks и др.) не изменяются.

-- Учебный текст урока — отдельный документ Yjs (не связан с редактором кода code_docs).
-- Сервер не разбирает содержимое — только хранит журнал бинарных обновлений и рассылает его участникам.
CREATE TABLE IF NOT EXISTS workshop_docs (
    lesson_id   uuid PRIMARY KEY REFERENCES lessons(id) ON DELETE CASCADE,
    updates     bytea[] NOT NULL DEFAULT '{}',
    seq         bigint NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Настройки мастерской урока: режим (идентификатор из конфигурации режимов в коде, например general, english)
-- и момент, когда преподаватель подтвердил, что выбранный текст передаётся внешнему ИИ.
CREATE TABLE IF NOT EXISTS workshop_settings (
    lesson_id        uuid PRIMARY KEY REFERENCES lessons(id) ON DELETE CASCADE,
    mode             text NOT NULL DEFAULT 'general',
    ai_notice_ack_at timestamptz,
    updated_by       uuid REFERENCES users(id),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Разбор выбранного фрагмента. draft — рекомендация ИИ, которую редактирует преподаватель (только преподавателю);
-- published — версия, которую преподаватель показал ученику (NULL — ученик разбор не видит).
CREATE TABLE IF NOT EXISTS workshop_analyses (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id     uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    created_by    uuid NOT NULL REFERENCES users(id),
    mode          text NOT NULL,
    action        text NOT NULL,
    level         text NOT NULL,
    fragment      text NOT NULL,
    draft         jsonb NOT NULL,
    published     jsonb,
    published_at  timestamptz,
    model         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS workshop_analyses_lesson_idx ON workshop_analyses (lesson_id, created_at);

-- Домашнее задание мастерской. draft — повторение темы и упражнения (черновик);
-- answer_key — ответы и рекомендации по проверке: только преподавателю, никогда не отдаётся ученику;
-- published — опубликованная версия без ответов (NULL — ученик задание не видит).
CREATE TABLE IF NOT EXISTS workshop_homework (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id     uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    created_by    uuid NOT NULL REFERENCES users(id),
    mode          text NOT NULL,
    level         text NOT NULL,
    source_text   text NOT NULL,
    draft         jsonb NOT NULL,
    answer_key    jsonb NOT NULL,
    published     jsonb,
    published_at  timestamptz,
    model         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS workshop_homework_lesson_idx ON workshop_homework (lesson_id, created_at);
