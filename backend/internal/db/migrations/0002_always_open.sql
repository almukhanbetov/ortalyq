-- Демо-класс: вход открыт в любое время (без окна «за 15 минут до начала»),
-- фоновые задачи не отменяют и не завершают такое занятие автоматически.
ALTER TABLE lessons ADD COLUMN always_open boolean NOT NULL DEFAULT false;
