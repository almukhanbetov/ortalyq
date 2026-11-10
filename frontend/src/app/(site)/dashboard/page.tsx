"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage, Lesson, Teacher } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { useLessonsRefresh } from "@/lib/hooks";
import { formatDate } from "@/lib/format";
import { canEnter, LessonCard } from "@/components/LessonCard";
import { Empty, ErrorBox, Loading, Notice, PageHeader, Skeleton } from "@/components/ui";

interface HomeworkItem {
  id: string;
  title: string;
  dueAt: string | null;
  lessonId: string;
  subject: string;
  teacherName: string;
  studentName: string;
  status: "assigned" | "submitted" | "reviewed";
}

const hwStatus = {
  assigned: { label: "Нужно выполнить", cls: "bg-amber-100 text-amber-800" },
  submitted: { label: "На проверке", cls: "bg-sky-100 text-sky-800" },
  reviewed: { label: "Проверено", cls: "bg-emerald-100 text-emerald-800" },
};

export default function Dashboard() {
  const { user, ready, tz, config } = useRequireUser();
  const [lessons, setLessons] = useState<Lesson[] | null>(null);
  const [homework, setHomework] = useState<HomeworkItem[] | null>(null);
  const [profile, setProfile] = useState<Teacher | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async (silent = false) => {
    if (!user || user.role === "admin") return;
    if (!silent) setError("");
    try {
      const [l, h] = await Promise.all([
        api<{ lessons: Lesson[] }>("/api/lessons?scope=upcoming"),
        api<{ homework: HomeworkItem[] }>("/api/my/homework"),
      ]);
      setLessons(l.lessons);
      setHomework(h.homework);
      if (user.role === "teacher") {
        const p = await api<{ profile: Teacher }>("/api/me/profile");
        setProfile(p.profile);
      }
    } catch (e) {
      if (silent) return; // фоновое обновление не затирает показанные данные
      setError(errorMessage(e));
      setLessons([]);
      setHomework([]);
    }
  }, [user]);
  useEffect(() => {
    load();
  }, [load]);
  useLessonsRefresh(() => load(true));

  if (!ready || !user) return <Loading />;
  if (user.role === "admin") return <AdminDashboard />;

  const openNow = lessons?.find((l) => canEnter(l, config?.joinEarlyMinutes ?? 15));
  const isTeacher = user.role === "teacher";
  const hwTodo = (homework ?? []).filter((h) => (isTeacher ? h.status === "submitted" : h.status === "assigned"));

  return (
    <div className="space-y-8">
      <PageHeader
        title={`Здравствуйте, ${user.name.replace(/\s*\(ДЕМО\)/, "").split(" ")[0]}!`}
        subtitle={isTeacher ? "Кабинет преподавателя" : "Кабинет ученика"}
        action={
          isTeacher ? (
            <div className="flex gap-2">
              <Link href="/availability" className="btn-secondary">Расписание</Link>
              <Link href="/profile" className="btn-secondary">Профиль</Link>
            </div>
          ) : (
            <Link href="/teachers" className="btn-primary">Найти преподавателя</Link>
          )
        }
      />
      {config && !config.videoConfigured && (
        <Notice tone="warn" title="Видеосвязь не настроена">
          Администратору нужно указать LIVEKIT_URL, LIVEKIT_API_KEY и LIVEKIT_API_SECRET — без них вход в видеокласс недоступен.
        </Notice>
      )}
      {isTeacher && profile && profile.subjects.length === 0 && (
        <Notice tone="warn" title="Заполните профиль">
          Укажите предметы и стоимость — без этого вас не видно в каталоге. <Link href="/profile" className="font-semibold underline">Перейти в профиль</Link>
        </Notice>
      )}
      {error && <ErrorBox message={error} onRetry={() => load()} />}

      {openNow && (
        <section className="overflow-hidden rounded-3xl bg-gradient-to-br from-brand-600 to-indigo-700 p-6 text-white shadow-lg sm:p-8" aria-label="Открытый класс">
          <div className="flex flex-col gap-5 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <div className="chip mb-2 bg-white/15 text-white">
                <span className="h-2 w-2 rounded-full bg-emerald-300" /> Виртуальный класс открыт
              </div>
              <h2 className="text-xl font-bold sm:text-2xl">
                {openNow.subject} · {isTeacher ? openNow.studentName : openNow.teacherName}
              </h2>
              <p className="mt-1 text-sm text-white/80">Видео, общая доска, чат и запись урока — в одном окне.</p>
            </div>
            <Link href={`/lessons/${openNow.id}/room`} className="btn bg-white px-7 py-4 text-base text-brand-700 hover:bg-brand-50">
              🎥 Войти в класс
            </Link>
          </div>
        </section>
      )}

      <div className="grid gap-8 lg:grid-cols-[1fr_380px]">
        <section id="my-lessons">
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-lg font-semibold">Мои занятия</h2>
            <Link href="/lessons" className="text-sm font-medium text-brand-700 hover:underline">
              Все и история →
            </Link>
          </div>
          {lessons === null ? (
            <Skeleton className="h-28" />
          ) : lessons.length === 0 ? (
            <Empty
              icon="🗓️"
              title="Запланированных занятий нет"
              text={isTeacher ? "Откройте интервалы в расписании — ученики смогут записаться." : "Выберите преподавателя и запишитесь на удобное время."}
              action={isTeacher ? <Link href="/availability" className="btn-primary">Открыть расписание</Link> : <Link href="/teachers" className="btn-primary">Выбрать преподавателя</Link>}
            />
          ) : (
            <div className="space-y-3">
              {lessons.map((l) => (
                <LessonCard key={l.id} lesson={l} highlight={l.id === openNow?.id} />
              ))}
            </div>
          )}
        </section>
        <section>
          <h2 className="mb-3 text-lg font-semibold">{isTeacher ? "Работы на проверку" : "Домашние задания"}</h2>
          {homework === null ? (
            <Skeleton className="h-24" />
          ) : (homework ?? []).length === 0 ? (
            <p className="rounded-2xl border border-dashed border-line bg-white/60 p-6 text-center text-sm text-muted">Заданий пока нет</p>
          ) : (
            <div className="card divide-y divide-line">
              {(hwTodo.length ? hwTodo : homework!.slice(0, 5)).map((h) => (
                <Link key={h.id} href={`/lessons/${h.lessonId}#homework`} className="block p-4 hover:bg-brand-50/50">
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-medium">{h.title}</span>
                    <span className={`chip shrink-0 ${hwStatus[h.status].cls}`}>{isTeacher && h.status === "assigned" ? "Ждём ответа" : hwStatus[h.status].label}</span>
                  </div>
                  <div className="mt-1 text-xs text-muted">
                    {h.subject} · {isTeacher ? h.studentName : h.teacherName}
                    {h.dueAt && ` · срок ${formatDate(h.dueAt, tz)}`}
                  </div>
                </Link>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function AdminDashboard() {
  const [stats, setStats] = useState<Record<string, number> | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api<Record<string, number>>("/api/admin/stats").then(setStats).catch((e) => setError(errorMessage(e)));
  }, []);
  const items: [string, string][] = [
    ["users", "Пользователей"],
    ["teachers", "Преподавателей"],
    ["students", "Учеников"],
    ["lessons", "Занятий"],
    ["listings", "Объявлений"],
    ["hiddenListings", "Скрыто объявлений"],
    ["readyRecordings", "Готовых записей"],
  ];
  return (
    <div>
      <PageHeader title="Кабинет администратора" action={<Link href="/admin" className="btn-primary">Модерация объявлений</Link>} />
      <Notice tone="info">
        Администратор модерирует доску объявлений и видит сводку. Доступа к содержимому чужих занятий, записям и материалам у администратора нет.
      </Notice>
      {error && <ErrorBox message={error} />}
      <div className="mt-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        {items.map(([k, label]) => (
          <div key={k} className="card p-5">
            <div className="text-2xl font-bold">{stats ? stats[k] : "—"}</div>
            <div className="text-sm text-muted">{label}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
