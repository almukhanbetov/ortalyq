"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, errorMessage, Teacher } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { checkStart, rangesByDay, type Busy, type FreeInterval } from "@/lib/booking";
import { formatDateTime, formatDayLabel, formatPrice, hmIn, plural, tzLabel } from "@/lib/format";
import { Avatar, BackLink, ConfirmModal, DemoBadge, Empty, ErrorBox, Loading, Notice, Spinner, Stars, toast } from "@/components/ui";

interface Review {
  rating: number;
  body: string;
  createdAt: string;
  studentName: string;
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
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_420px]">
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

/** Маска ввода ЧЧ:ММ (24 ч): только цифры и двоеточие; время не изменяется, только форматируется. */
function maskHm(raw: string): string {
  if (raw.includes(":")) {
    const [h, m = ""] = raw.split(":");
    return `${h.replace(/\D/g, "").slice(0, 2)}:${m.replace(/\D/g, "").slice(0, 2)}`;
  }
  const d = raw.replace(/\D/g, "").slice(0, 4);
  return d.length <= 2 ? d : `${d.slice(0, 2)}:${d.slice(2)}`;
}

function BookingPanel({ teacher }: { teacher: Teacher }) {
  const { user, tz } = useAuth();
  const router = useRouter();
  const [duration, setDuration] = useState(60);
  const [subject, setSubject] = useState(teacher.subjects[0] ?? "");
  const [note, setNote] = useState("");
  const [data, setData] = useState<{ intervals: FreeInterval[]; myLessons: Busy[] } | null>(null);
  const [error, setError] = useState("");
  const [day, setDay] = useState("");
  const [time, setTime] = useState("");
  const [serverReason, setServerReason] = useState("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(() => new Date());

  const load = useCallback(() => {
    setData(null);
    setError("");
    api<{ intervals: FreeInterval[]; myLessons: Busy[] }>(`/api/teachers/${teacher.id}/slots?duration=${duration}`)
      .then((r) => setData({ intervals: r.intervals ?? [], myLessons: r.myLessons ?? [] }))
      .catch((e) => {
        setError(errorMessage(e));
        setData({ intervals: [], myLessons: [] });
      });
  }, [teacher.id, duration]);
  useEffect(load, [load]);
  // Текущее время для проверки «в прошлом» (раз в 30 с)
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 30_000);
    return () => clearInterval(t);
  }, []);

  const byDay = useMemo(() => rangesByDay(data?.intervals ?? [], tz), [data, tz]);
  useEffect(() => {
    // Выбранный день сохраняется, пока в нём есть свободное время; время начала не трогаем.
    if (data && (!day || !byDay.has(day))) setDay(byDay.keys().next().value ?? "");
  }, [byDay, day, data]);

  const check = useMemo(
    // Проверяем только полностью введённое время (Ч:ММ или ЧЧ:ММ), без ошибок на середине ввода.
    () => (/^\d{1,2}:\d\d$/.test(time) ? checkStart(day, time.padStart(5, "0"), duration, tz, data?.intervals ?? [], data?.myLessons ?? [], now) : null),
    [day, time, duration, tz, data, now],
  );
  useEffect(() => setServerReason(""), [day, time, duration]);

  const book = async () => {
    if (!check?.ok) return;
    setBusy(true);
    try {
      const r = await api<{ id: string }>("/api/lessons", {
        method: "POST",
        json: { teacherId: teacher.id, startsAt: check.start.toISOString(), duration, subject, note },
      });
      toast("Вы записаны на занятие");
      router.push(`/lessons/${r.id}`);
    } catch (e) {
      // Причину показываем у поля; выбранное время не меняем.
      setServerReason(errorMessage(e));
      setConfirmOpen(false);
      if (e instanceof ApiError && e.status === 409) load();
    } finally {
      setBusy(false);
    }
  };

  const canBook = user?.role === "student";
  const ranges = byDay.get(day) ?? [];
  const reason = serverReason || (check && !check.ok ? check.reason : "");

  return (
    <aside className="card h-fit min-w-0 p-6 lg:sticky lg:top-24" id="book">
      <h2 className="text-lg font-semibold">Записаться на занятие</h2>
      <p className="mt-1 text-xs text-muted" data-testid="booking-tz">
        Часовой пояс: <b>{tzLabel(tz)}</b>. Всё время в форме — в нём.{" "}
        {user && (
          <Link href="/profile" className="underline">
            Изменить
          </Link>
        )}
      </p>

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
        {data === null ? (
          <div className="flex items-center gap-2 py-6 text-sm text-muted">
            <Spinner className="h-4 w-4" /> Загружаем свободное время…
          </div>
        ) : byDay.size === 0 ? (
          <Empty icon="🗓️" title="Нет свободного времени" text={`Нет свободных интервалов для урока ${duration} мин на ближайшие 3 недели.`} />
        ) : (
          <>
            <div className="label">Дата</div>
            <div className="-mx-1 flex gap-2 overflow-x-auto px-1 pb-2" role="listbox" aria-label="Дата занятия">
              {[...byDay.keys()].map((k) => (
                <button
                  key={k}
                  role="option"
                  aria-selected={day === k}
                  data-day={k}
                  onClick={() => setDay(k)}
                  className={`shrink-0 rounded-xl border px-3 py-2 text-xs font-medium ${
                    day === k ? "border-brand-500 bg-brand-600 text-white" : "border-line bg-white hover:border-brand-300"
                  }`}
                >
                  {formatDayLabel(byDay.get(k)![0].from.toISOString(), tz)}
                </button>
              ))}
            </div>

            <div className="mt-3 rounded-xl bg-canvas p-3 text-sm" data-testid="free-intervals">
              <div className="mb-1 text-xs font-semibold text-muted">Свободно в этот день (урок {duration} мин):</div>
              <ul className="space-y-1">
                {ranges.map((r) => (
                  <li key={r.from.toISOString()}>
                    <button
                      type="button"
                      className="text-left hover:underline disabled:no-underline"
                      disabled={!canBook}
                      onClick={() => setTime(hmIn(r.from, tz))}
                      title="Подставить самое раннее начало"
                    >
                      <b>
                        {hmIn(r.interval.startsAt, tz)}–{hmIn(r.interval.endsAt, tz)}
                      </b>{" "}
                      <span className="text-muted">
                        · начало с {hmIn(r.from, tz)} до {hmIn(r.to, tz)}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>

            <div className="mt-3">
              <label className="label" htmlFor="start-time">
                Время начала (ЧЧ:ММ)
              </label>
              <div className="flex items-center gap-2">
                <input
                  id="start-time"
                  type="text"
                  inputMode="numeric"
                  autoComplete="off"
                  placeholder="ЧЧ:ММ"
                  maxLength={5}
                  className="input w-28 font-mono tabular-nums"
                  value={time}
                  disabled={!canBook}
                  onChange={(e) => setTime(maskHm(e.target.value))}
                  onBlur={() => setTime((v) => (/^\d:\d\d$/.test(v) ? `0${v}` : v))}
                  aria-describedby="start-hint"
                  aria-invalid={!!reason}
                />
                <span className="text-xs text-muted">{tzLabel(tz)}</span>
              </div>
              <div id="start-hint" className="mt-1 min-h-5 text-sm" aria-live="polite" data-testid="start-status">
                {reason ? (
                  <span className="text-red-700">{reason}</span>
                ) : check?.ok ? (
                  <span className="text-emerald-700">
                    {formatDayLabel(check.start.toISOString(), tz)}, {hmIn(check.start, tz)}–{hmIn(check.end, tz)} — время свободно
                  </span>
                ) : (
                  <span className="text-muted">Любая минута внутри свободного интервала, например 10:05 или 10:42.</span>
                )}
              </div>
            </div>
          </>
        )}
      </div>

      {canBook && (
        <div className="mt-4">
          <label className="label" htmlFor="note">Комментарий для преподавателя</label>
          <textarea id="note" className="input min-h-20" maxLength={1000} placeholder="Например: готовлюсь к ЕНТ, сложно с тригонометрией" value={note} onChange={(e) => setNote(e.target.value)} />
          <button className="btn-primary mt-3 w-full" disabled={!check?.ok || !!serverReason} onClick={() => setConfirmOpen(true)} data-testid="book-open">
            Записаться
          </button>
        </div>
      )}

      <ConfirmModal
        open={confirmOpen && !!check?.ok}
        title="Подтвердите запись"
        busy={busy}
        confirmLabel="Записаться"
        onClose={() => setConfirmOpen(false)}
        onConfirm={book}
        text={
          check?.ok && (
            <div className="space-y-2">
              <p>
                <b>{teacher.name}</b> · {subject}
              </p>
              <p>
                {formatDateTime(check.start.toISOString(), tz)} – {hmIn(check.end, tz)} ({duration} мин), {tzLabel(tz)}
              </p>
              <p className="text-muted">Стоимость: {formatPrice(Math.round((teacher.price * duration) / 60))}. Оплата в этой версии не подключена.</p>
            </div>
          )
        }
      />
    </aside>
  );
}
