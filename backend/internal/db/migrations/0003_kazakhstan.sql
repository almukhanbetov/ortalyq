-- Адаптация под Казахстан.
-- Все денежные суммы (teacher_profiles.price, listings.price, lessons.price) — целые тенге (KZT), без минимальных единиц.
-- Реальные пользовательские суммы не конвертируются.
ALTER TABLE users ALTER COLUMN timezone SET DEFAULT 'Asia/Almaty';
ALTER TABLE users ADD COLUMN IF NOT EXISTS city text NOT NULL DEFAULT '';
-- Телефон виден только владельцу аккаунта (в профиле).
ALTER TABLE users ADD COLUMN IF NOT EXISTS phone text NOT NULL DEFAULT '';
ALTER TABLE teacher_profiles ALTER COLUMN price SET DEFAULT 5000;
