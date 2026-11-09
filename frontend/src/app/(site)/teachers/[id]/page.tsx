"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, errorMessage, Teacher } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { dayKey, formatDateTime, formatDayLabel, formatPrice, formatTime, plural, tzLabel } from "@/lib/format";
import { Avatar, BackLink, ConfirmModal, DemoBadge, Empty, ErrorBox, Loading, Notice, Spinner, Stars, toast } from "@/components/ui";

interface Review {
  rating: number;
  body: string;
  createdAt: string;
  studentName: string;
}
interface Slot {
  startsAt: string;
  endsAt: string;
}

export default function TeacherPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const [data, setData] = useState<{ teacher: Teacher; reviews: Review[] } | null>(null);
  const [error, setError] = useState("");
  const { tz } = useAuth();

  const load = useCallback(() => {
    setError("");
    api<{ teacher: Teacher; reviews: Review[] }>(`/api/teachers/${id}`)
      .then(setData)
      .catch((e) => setError(errorMessage(e)));
  }, [id]);
  useEffect(load, [load]);

  if (error) return <ErrorBox message={error} onRetry={load} />;
  if (!data) return <Loading />;
  const t = data.teacher;

  return (
    <div>
      <BackLink href="/teachers" label="Все преподаватели" />
      <div className="grid gap-6 lg:grid-cols-[1fr_420px]">
        <div className="space-y-6">
          <div className="card p-6">
            <div className="flex flex-col gap-5 sm:flex-row">
              <Avatar name={t.name} src={t.photoUrl} size={112} />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <h1 className="text-2xl font-bold">{t.name}</h1>
                  {t.isDemo && <DemoBadge />}
                </div>
                <p className="mt-1 text-muted">{t.headline}</p>
                {t.city && <p className="mt-1 text-sm text-muted">📍 {t.city} · занятия онлайн</p>}
                <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
                  {t.rating != null ? (
                    <span className="flex items-center gap-1">
                      <Stars value={t.rating} /> <b>{t.rating.toFixed(1)}</b>
                      <span className="text-muted">· {t.reviewCount} {plural(t.reviewCount, "отзыв", "отзыва", "отзывов")}</span>
                    </span>
                  ) : (
                    <span className="text-muted">Пока нет отзывов</span>
                  )}
                  {t.experienceYears > 0 && (
                    <span className="text-muted">
                      Опыт {t.experienceYears} {plural(t.experienceYears, "год", "года", "лет")}
                    </span>
                  )}
                </div>
                <div className="mt-3 flex flex-wrap gap-1.5">
                  {t.subjects.map((s) => (
                    <span key={s} className="chip bg-brand-50 text-brand-700">
                      {s}
                    </span>
                  ))}
                </div>
              </div>
            </div>
            {t.bio && <p className="mt-6 whitespace-pre-line text-sm leading-relaxed text-ink/85">{t.bio}</p>}
            <div className="mt-6 flex items-center justify-between rounded-2xl bg-brand-50 px-4 py-3">
              <span className="text-sm text-brand-900">Стоимость занятия</span>
              <span className="text-lg font-bold text-brand-800">{formatPrice(t.price)} / 60 мин</span>
            </div>
          </div>

          <div className="card p-6">
            <h2 className="mb-4 text-lg font-semibold">Отзывы учеников</h2>
            {data.reviews.length === 0 ? (
              <p className="text-sm text-muted">Отзывов пока нет — они появляются после завершённых занятий.</p>
            ) : (
              <ul className="space-y-4">
                {data.reviews.map((r, i) => (
                  <li key={i} className="border-b border-line pb-4 last:border-0 last:pb-0">
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-medium">{r.studentName}</span>
                      <Stars value={r.rating} />
                    </div>
                    {r.body && <p className="mt-1 text-sm text-ink/80">{r.body}</p>}
                    <div className="mt-1 text-xs text-muted">{formatDateTime(r.createdAt, tz)}</div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
        <BookingPanel teacher={t} />
      </div>
    </div>
  );
}

function BookingPanel({ teacher }: { teacher: Teacher }) {
  const { user, tz } = useAuth();
  const router = useRouter();
  const [duration, setDuration] = useState(60);
  const [subject, setSubject] = useState(teacher.subjects[0] ?? "");
  const [note, setNote] = useState("");
  const [slots, setSlots] = useState<Slot[] | null>(null);
  const [error, setError] = useState("");
  const [day, setDay] = useState("");
  const [picked, setPicked] = useState<Slot | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    setSlots(null);
    setError("");
    api<{ slots: Slot[] }>(`/api/teachers/${teacher.id}/slots?duration=${duration}`)
      .then((r) => setSlots(r.slots))
      .catch((e) => {
        setError(errorMessage(e));
        setSlots([]);
      });
  }, [teacher.id, duration]);
  useEffect(load, [load]);

  const byDay = useMemo(() => {
    const m = new Map<string, Slot[]>();
    for (const s of slots ?? []) {
      const k = dayKey(s.startsAt, tz);
      if (!m.has(k)) m.set(k, []);
      m.get(k)!.push(s);
    }
    return m;
  }, [slots, tz]);

  useEffect(() => {
    if (!day || !byDay.has(day)) setDay(byDay.keys().next().value ?? "");
  }, [byDay, day]);

  const book = async () => {
    if (!picked) return;
    setBusy(true);
    try {
      const r = await api<{ id: string }>("/api/lessons", {
        method: "POST",
        json: { teacherId: teacher.id, startsAt: picked.startsAt, duration, subject, note },
      });
      toast("Вы записаны на занятие");
      router.push(`/lessons/${r.id}`);
    } catch (e) {
      toast(errorMessage(e), "err");
      setPicked(null);
      if (e instanceof ApiError && e.status === 409) load();
    } finally {
      setBusy(false);
    }
  };

  const canBook = user?.role === "student";

  return (
    <aside className="card h-fit p-6 lg:sticky lg:top-24" id="book">
      <h2 className="text-lg font-semibold">Записаться на занятие</h2>
      <p className="mt-1 text-xs text-muted">Время показано в часовом поясе {tzLabel(tz)}</p>

      {!user && (
        <Notice tone="info">
          <Link href={`/login?next=/teachers/${teacher.id}`} className="font-semibold underline">
            Войдите
          </Link>{" "}
          или{" "}
          <Link href="/register" className="font-semibold underline">
            зарегистрируйтесь
          </Link>{" "}
          как ученик, чтобы записаться.
        </Notice>
      )}
      {user && user.role !== "student" && (
        <div className="mt-4">
          <Notice tone="warn">Записываться на занятия могут только ученики. Вы вошли как {user.role === "teacher" ? "преподаватель" : "администратор"}.</Notice>
        </div>
      )}

      <div className="mt-4 grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="subj">Предмет</label>
          <select id="subj" className="input" value={subject} onChange={(e) => setSubject(e.target.value)}>
            {teacher.subjects.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        </div>
        <div>
          <label className="label" htmlFor="dur">Длительность</label>
          <select id="dur" className="input" value={duration} onChange={(e) => setDuration(Number(e.target.value))}>
            {[30, 45, 60, 90].map((d) => (
              <option key={d} value={d}>
                {d} мин
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="mt-5">
        {error && <ErrorBox message={error} onRetry={load} />}
        {slots === null ? (
          <div className="flex items-center gap-2 py-6 text-sm text-muted">
            <Spinner className="h-4 w-4" /> Загружаем свободное время…
          </div>
        ) : byDay.size === 0 ? (
          <Empty icon="🗓️" title="Нет свободного времени" text="Преподаватель пока не открыл интервалы на ближайшие 3 недели." />
        ) : (
          <>
            <div className="-mx-1 flex gap-2 overflow-x-auto px-1 pb-2">
              {[...byDay.keys()].map((k) => (
                <button
                  key={k}
                  onClick={() => setDay(k)}
                  className={`shrink-0 rounded-xl border px-3 py-2 text-xs font-medium ${
                    day === k ? "border-brand-500 bg-brand-600 text-white" : "border-line bg-white hover:border-brand-300"
                  }`}
                >
                  {formatDayLabel(byDay.get(k)![0].startsAt, tz)}
                </button>
              ))}
            </div>
            <div className="mt-3 grid grid-cols-3 gap-2 sm:grid-cols-4 lg:grid-cols-3">
              {(byDay.get(day) ?? []).map((s) => (
                <button
                  key={s.startsAt}
                  disabled={!canBook}
                  onClick={() => setPicked(s)}
                  className="rounded-xl border border-line bg-white px-2 py-2.5 text-sm font-medium transition hover:border-brand-400 hover:bg-brand-50 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {formatTime(s.startsAt, tz)}
                </button>
              ))}
            </div>
          </>
        )}
      </div>

      {canBook && (
        <div className="mt-4">
          <label className="label" htmlFor="note">Комментарий для преподавателя</label>
          <textarea id="note" className="input min-h-20" maxLength={1000} placeholder="Например: готовлюсь к ЕНТ, сложно с тригонометрией" value={note} onChange={(e) => setNote(e.target.value)} />
        </div>
      )}

      <ConfirmModal
        open={!!picked}
        title="Подтвердите запись"
        busy={busy}
        confirmLabel="Записаться"
        onClose={() => setPicked(null)}
        onConfirm={book}
        text={
          picked && (
            <div className="space-y-2">
              <p>
                <b>{teacher.name}</b> · {subject}
              </p>
              <p>
                {formatDateTime(picked.startsAt, tz)} – {formatTime(picked.endsAt, tz)} ({duration} мин)
              </p>
              <p className="text-muted">Стоимость: {formatPrice(Math.round((teacher.price * duration) / 60))}. Оплата в этой версии не подключена.</p>
            </div>
          )
        }
      />
    </aside>
  );
}
