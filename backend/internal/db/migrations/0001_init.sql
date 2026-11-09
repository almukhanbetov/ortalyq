CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    password_hash text NOT NULL,
    role          text NOT NULL CHECK (role IN ('student', 'teacher', 'admin')),
    name          text NOT NULL,
    timezone      text NOT NULL DEFAULT 'Europe/Moscow',
    is_demo       boolean NOT NULL DEFAULT false,
    blocked       boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_uq ON users (lower(email));

CREATE TABLE sessions (
    token_hash  bytea PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    user_agent  text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE teacher_profiles (
    user_id      uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    headline     text NOT NULL DEFAULT '',
    bio          text NOT NULL DEFAULT '',
    subjects     text[] NOT NULL DEFAULT '{}',
    price        integer NOT NULL DEFAULT 1000 CHECK (price >= 0),
    photo_key    text,
    experience_years integer NOT NULL DEFAULT 0,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX teacher_profiles_subjects_idx ON teacher_profiles USING gin (subjects);

CREATE TABLE listings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('offer', 'request')),
    subject     text NOT NULL,
    title       text NOT NULL,
    body        text NOT NULL,
    price       integer CHECK (price IS NULL OR price >= 0),
    hidden      boolean NOT NULL DEFAULT false,
    hidden_reason text NOT NULL DEFAULT '',
    is_demo     boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX listings_created_idx ON listings (created_at DESC);

CREATE TABLE availability (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    teacher_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    starts_at   timestamptz NOT NULL,
    ends_at     timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    EXCLUDE USING gist (teacher_id WITH =, tstzrange(starts_at, ends_at) WITH &&)
);

CREATE TABLE lessons (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    teacher_id  uuid NOT NULL REFERENCES users(id),
    student_id  uuid NOT NULL REFERENCES users(id),
    subject     text NOT NULL,
    note        text NOT NULL DEFAULT '',
    starts_at   timestamptz NOT NULL,
    ends_at     timestamptz NOT NULL,
    status      text NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'live', 'completed', 'cancelled')),
    started_at  timestamptz,
    ended_at    timestamptz,
    cancelled_by uuid REFERENCES users(id),
    cancel_reason text NOT NULL DEFAULT '',
    price       integer NOT NULL DEFAULT 0,
    is_demo     boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    CHECK (teacher_id <> student_id),
    -- Ни преподаватель, ни ученик не могут иметь два пересекающихся активных занятия.
    -- Ограничение работает на уровне БД и поэтому защищает от одновременных запросов.
    CONSTRAINT lessons_teacher_no_overlap EXCLUDE USING gist (
        teacher_id WITH =, tstzrange(starts_at, ends_at) WITH &&
    ) WHERE (status <> 'cancelled'),
    CONSTRAINT lessons_student_no_overlap EXCLUDE USING gist (
        student_id WITH =, tstzrange(starts_at, ends_at) WITH &&
    ) WHERE (status <> 'cancelled')
);
CREATE INDEX lessons_teacher_idx ON lessons (teacher_id, starts_at);
CREATE INDEX lessons_student_idx ON lessons (student_id, starts_at);

CREATE TABLE chat_messages (
    id          bigserial PRIMARY KEY,
    lesson_id   uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id),
    body        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chat_messages_lesson_idx ON chat_messages (lesson_id, id);

CREATE TABLE boards (
    lesson_id   uuid PRIMARY KEY REFERENCES lessons(id) ON DELETE CASCADE,
    elements    jsonb NOT NULL DEFAULT '[]',
    revision    bigint NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE recordings (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id       uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    requested_by    uuid NOT NULL REFERENCES users(id),
    status          text NOT NULL CHECK (status IN ('awaiting_consent', 'starting', 'recording', 'processing', 'ready', 'failed', 'declined')),
    teacher_consent_at timestamptz,
    student_consent_at timestamptz,
    egress_id       text,
    object_key      text,
    size_bytes      bigint,
    duration_ms     bigint,
    error           text NOT NULL DEFAULT '',
    started_at      timestamptz,
    ended_at        timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recordings_lesson_idx ON recordings (lesson_id, created_at);
-- Одновременно только одна активная запись на занятие.
CREATE UNIQUE INDEX recordings_one_active ON recordings (lesson_id)
    WHERE status IN ('awaiting_consent', 'starting', 'recording');

CREATE TABLE materials (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id    uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    uploader_id  uuid NOT NULL REFERENCES users(id),
    filename     text NOT NULL,
    object_key   text NOT NULL,
    content_type text NOT NULL,
    size_bytes   bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE homeworks (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id    uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    title        text NOT NULL,
    description  text NOT NULL DEFAULT '',
    due_at       timestamptz,
    file_name    text,
    file_key     text,
    file_type    text,
    file_size    bigint,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE homework_submissions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    homework_id    uuid NOT NULL UNIQUE REFERENCES homeworks(id) ON DELETE CASCADE,
    student_id     uuid NOT NULL REFERENCES users(id),
    body           text NOT NULL DEFAULT '',
    file_name      text,
    file_key       text,
    file_type      text,
    file_size      bigint,
    status         text NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted', 'reviewed')),
    teacher_comment text NOT NULL DEFAULT '',
    grade          integer CHECK (grade IS NULL OR grade BETWEEN 1 AND 5),
    submitted_at   timestamptz NOT NULL DEFAULT now(),
    reviewed_at    timestamptz
);

CREATE TABLE reviews (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lesson_id   uuid NOT NULL UNIQUE REFERENCES lessons(id) ON DELETE CASCADE,
    student_id  uuid NOT NULL REFERENCES users(id),
    teacher_id  uuid NOT NULL REFERENCES users(id),
    rating      integer NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reviews_teacher_idx ON reviews (teacher_id, created_at DESC);
