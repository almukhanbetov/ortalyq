"use client";

import Link from "next/link";
import { Lesson } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { formatRange, relativeStart } from "@/lib/format";
import { useNow } from "@/lib/hooks";
import { Avatar, DemoBadge, StatusBadge } from "./ui";

export function canEnter(l: Lesson, earlyMin = 15, lateMin = 60, now = Date.now()) {
  if (l.status !== "scheduled" && l.status !== "live") return false;
  if (l.alwaysOpen) return true;
  const opens = new Date(l.startsAt).getTime() - earlyMin * 60000;
  const closes = new Date(l.endsAt).getTime() + lateMin * 60000;
  return now >= opens && now <= closes;
}

export function LessonCard({ lesson, highlight }: { lesson: Lesson; highlight?: boolean }) {
  const { user, tz, config } = useAuth();
  const now = useNow(15000);
  const other = user?.id === lesson.teacherId ? lesson.studentName : lesson.teacherName;
  const otherLabel = user?.id === lesson.teacherId ? "Ученик" : "Преподаватель";
  const enter = canEnter(lesson, config?.joinEarlyMinutes ?? 15, 60, now);
  const upcoming = lesson.status === "scheduled" || lesson.status === "live";

  return (
    <div
      className={`card flex flex-col gap-4 p-5 sm:flex-row sm:items-center ${
        highlight ? "border-brand-300 bg-gradient-to-br from-white to-brand-50 ring-4 ring-brand-100/60" : ""
      }`}
    >
      <Avatar
        name={other}
        src={user?.id !== lesson.teacherId && lesson.hasTeacherPhoto ? `/api/teachers/${lesson.teacherId}/photo` : null}
        size={52}
      />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <Link href={`/lessons/${lesson.id}`} className="font-semibold hover:text-brand-700">
            {lesson.subject}
          </Link>
          <StatusBadge status={lesson.status} />
          {lesson.isDemo && <DemoBadge />}
          {lesson.alwaysOpen && upcoming && <span className="chip bg-emerald-100 text-emerald-800">Класс открыт</span>}
        </div>
        <div className="mt-0.5 text-sm text-muted">
          {otherLabel}: <span className="text-ink">{other}</span>
        </div>
        <div className="mt-0.5 text-sm">
          {lesson.alwaysOpen ? "Вход в любое время — без ожидания начала" : formatRange(lesson.startsAt, lesson.endsAt, tz)}
          {upcoming && !lesson.alwaysOpen && <span className="text-muted"> · {relativeStart(lesson.startsAt)}</span>}
        </div>
        {lesson.note && <div className="mt-1 line-clamp-1 text-xs text-muted">«{lesson.note}»</div>}
      </div>
      <div className="flex shrink-0 flex-wrap gap-2">
        {upcoming &&
          (enter ? (
            <Link href={`/lessons/${lesson.id}/room`} className="btn-primary px-5 py-3 text-base shadow-md shadow-brand-600/20">
              🎥 Войти в класс
            </Link>
          ) : (
            <button className="btn-primary px-5 py-3" disabled title={`Вход откроется за ${config?.joinEarlyMinutes ?? 15} минут до начала`}>
              Вход за {config?.joinEarlyMinutes ?? 15} мин до начала
            </button>
          ))}
        <Link href={`/lessons/${lesson.id}`} className="btn-secondary py-3">
          Подробнее
        </Link>
      </div>
    </div>
  );
}
