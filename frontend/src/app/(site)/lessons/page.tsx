"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage, Lesson } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { formatDate, plural } from "@/lib/format";
import { LessonCard } from "@/components/LessonCard";
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
  const [lessons, setLessons] = useState<Lesson[] | null>(null);
  const [students, setStudents] = useState<Student[] | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    try {
      if (tab === "students") {
        setStudents(null);
        setStudents((await api<{ students: Student[] }>("/api/my/students")).students);
      } else {
        setLessons(null);
        setLessons((await api<{ lessons: Lesson[] }>(`/api/lessons?scope=${tab}`)).lessons);
      }
    } catch (e) {
      setError(errorMessage(e));
      setLessons([]);
      setStudents([]);
    }
  }, [tab]);
  useEffect(() => {
    if (ready) load();
  }, [ready, load]);

  if (!ready || !user) return <Loading />;
  const tabs: [Tab, string][] = [
    ["upcoming", "Предстоящие"],
    ["history", "История"],
  ];
  if (user.role === "teacher") tabs.push(["students", "Мои ученики"]);

  return (
    <div>
      <PageHeader title="Мои занятия" subtitle="Вход в виртуальный класс, расписание и история уроков" />
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
      {error && <ErrorBox message={error} onRetry={load} />}
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
      ) : lessons === null ? (
        <div className="space-y-3">
          <Skeleton className="h-28" />
          <Skeleton className="h-28" />
        </div>
      ) : lessons.length === 0 ? (
        <Empty
          icon={tab === "upcoming" ? "🗓️" : "📚"}
          title={tab === "upcoming" ? "Нет предстоящих занятий" : "История пока пуста"}
          text={tab === "upcoming" ? "Запланированные занятия появятся здесь." : "Здесь будут завершённые и отменённые занятия."}
          action={user.role === "student" && tab === "upcoming" ? <Link href="/teachers" className="btn-primary">Найти преподавателя</Link> : undefined}
        />
      ) : (
        <div className="space-y-3">
          {lessons.map((l) => (
            <LessonCard key={l.id} lesson={l} />
          ))}
        </div>
      )}
    </div>
  );
}
