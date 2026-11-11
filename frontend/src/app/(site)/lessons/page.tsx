"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage, Lesson } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { useLessonsRefresh, useNow } from "@/lib/hooks";
import { isUpcoming } from "@/lib/lessonTime";
import { formatDate, plural, tzLabel } from "@/lib/format";
import { LessonCard } from "@/components/LessonCard";
import { LessonCheckButton } from "@/components/LessonCheck";
import { Empty, ErrorBox, Loading, PageHeader, Skeleton } from "@/components/ui";

interface Student {
  id: string;
  name: string;
  completed: number;
  upcoming: number;
  lastLessonAt: string;
}

type Tab = "upcoming" | "history" | "students";

export default function LessonsPage() {
  const { user, ready, tz } = useRequireUser(["student", "teacher"]);
  const [tab, setTab] = useState<Tab>("upcoming");
  // Ссылка «Открыть историю» из проверки: /lessons?tab=history
  useEffect(() => {
    const t = new URLSearchParams(location.search).get("tab");
    if (t === "history" || t === "students") setTab(t);
  }, []);
  const [lessons, setLessons] = useState<Lesson[] | null>(null);
  const [students, setStudents] = useState<Student[] | null>(null);
  const [error, setError] = useState("");

  // silent — фоновое обновление без скелетона; при ошибке остаётся прежний список.
  const load = useCallback(
    async (silent = false) => {
      if (!silent) setError("");
      try {
        if (tab === "students") {
          if (!silent) setStudents(null);
          setStudents((await api<{ students: Student[] }>("/api/my/students")).students);
        } else {
          if (!silent) setLessons(null);
          setLessons((await api<{ lessons: Lesson[] }>(`/api/lessons?scope=${tab}`)).lessons);
        }
      } catch (e) {
        if (silent) return;
        setError(errorMessage(e));
        setLessons([]);
        setStudents([]);
      }
    },
    [tab],
  );
  useEffect(() => {
    if (ready) load();
  }, [ready, load]);
  useLessonsRefresh(() => ready && load(true));
  // Урок уходит из «Предстоящих» ровно в момент окончания, не дожидаясь следующего обновления с сервера.
  const now = useNow(15000);
  const shown = lessons && tab === "upcoming" ? lessons.filter((l) => isUpcoming(l, now)) : lessons;

  if (!ready || !user) return <Loading />;
  const tabs: [Tab, string][] = [
    ["upcoming", "Предстоящие"],
    ["history", "История"],
  ];
  if (user.role === "teacher") tabs.push(["students", "Мои ученики"]);

  return (
    <div>
      <PageHeader
        title="Мои занятия"
        subtitle={`Вход в виртуальный класс, расписание и история уроков · время указано в часовом поясе ${tzLabel(tz)}`}
        action={<LessonCheckButton className="btn-secondary" />}
      />
      <div className="mb-6 inline-flex rounded-xl border border-line bg-white p-1">
        {tabs.map(([t, label]) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`rounded-lg px-4 py-2 text-sm font-medium ${tab === t ? "bg-brand-600 text-white" : "text-muted hover:text-ink"}`}
          >
            {label}
          </button>
        ))}
      </div>
      {error && <ErrorBox message={error} onRetry={() => load()} />}
      {tab === "students" ? (
        students === null ? (
          <Skeleton className="h-32" />
        ) : students.length === 0 ? (
          <Empty icon="🎒" title="Учеников пока нет" text="Ученики появятся здесь после первой записи на занятие." />
        ) : (
          <div className="card divide-y divide-line">
            {students.map((s) => (
              <div key={s.id} className="flex flex-wrap items-center justify-between gap-2 p-4">
                <div className="font-medium">{s.name}</div>
                <div className="text-sm text-muted">
                  {s.completed} {plural(s.completed, "занятие проведено", "занятия проведено", "занятий проведено")} · {s.upcoming} впереди · последнее{" "}
                  {formatDate(s.lastLessonAt, tz)}
                </div>
              </div>
            ))}
          </div>
        )
      ) : shown === null ? (
        <div className="space-y-3">
          <Skeleton className="h-28" />
          <Skeleton className="h-28" />
        </div>
      ) : shown.length === 0 ? (
        <Empty
          icon={tab === "upcoming" ? "🗓️" : "📚"}
          title={tab === "upcoming" ? "Нет предстоящих занятий" : "История пока пуста"}
          text={tab === "upcoming" ? "Запланированные занятия появятся здесь." : "Здесь будут завершённые и отменённые занятия."}
          action={user.role === "student" && tab === "upcoming" ? <Link href="/teachers" className="btn-primary">Найти преподавателя</Link> : undefined}
        />
      ) : (
        <div className="space-y-3">
          {shown.map((l) => (
            <LessonCard key={l.id} lesson={l} />
          ))}
        </div>
      )}
    </div>
  );
}
