"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { api, errorMessage } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { dayKey, formatDayLabel, formatTime, todayIn, tzLabel, zonedToUtc } from "@/lib/format";
import { hmProblem, hmToMinutes, isCompleteHm, padHm } from "@/lib/time";
import { TimeInput } from "@/components/TimeInput";
import { ConfirmModal, Empty, ErrorBox, Loading, PageHeader, Skeleton, Spinner, toast } from "@/components/ui";

interface Window {
  id: string;
  startsAt: string;
  endsAt: string;
}

/** Занятие преподавателя, занимающее время (неотменённое, ещё не закончившееся). */
interface BookedLesson {
  id: string;
  startsAt: string;
  endsAt: string;
  status: string;
  subject: string;
  studentName: string;
  alwaysOpen: boolean;
}

interface Schedule {
  availability: Window[];
  lessons: BookedLesson[];
  /** Доступность минус занятия, с текущего момента — то, что видят ученики при записи. */
  free: { startsAt: string; endsAt: string }[];
}

export default function AvailabilityPage() {
  const { ready, tz } = useRequireUser(["teacher"]);
  const [schedule, setSchedule] = useState<Schedule | null>(null);
  const items = schedule?.availability ?? null;
  const [error, setError] = useState("");
  const [date, setDate] = useState("");
  const [from, setFrom] = useState("10:00");
  const [to, setTo] = useState("14:00");
  const [repeat, setRepeat] = useState(1);
  // Поле, из которого ушёл фокус (или была попытка отправки), — для него показываем ошибки неполного ввода.
  const [touched, setTouched] = useState({ from: false, to: false });
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState<Window | null>(null);

  // По умолчанию — сегодня в часовом поясе пользователя (не завтра): интервал на сегодня
  // доступен ученикам, уже прошедшая часть отсекается при расчёте свободного времени.
  useEffect(() => {
    if (ready && !date) setDate(todayIn(tz));
  }, [ready, tz, date]);

  const load = useCallback(async () => {
    setError("");
    try {
      const r = await api<Schedule>("/api/my/availability");
      setSchedule({ availability: r.availability ?? [], lessons: r.lessons ?? [], free: r.free ?? [] });
    } catch (e) {
      setError(errorMessage(e));
      setSchedule({ availability: [], lessons: [], free: [] });
    }
  }, []);
  useEffect(() => {
    if (ready) load();
  }, [ready, load]);

  // Ошибки времени: формат ЧЧ:ММ (00–23, 00–59) и окончание позже начала (в пределах одной даты).
  const fromProblem = hmProblem(from);
  const toProblem = hmProblem(to);
  const fromMin = hmToMinutes(from);
  const toMin = hmToMinutes(to);
  const orderProblem =
    fromMin !== null && toMin !== null && toMin <= fromMin
      ? `Время окончания (${padHm(to)}) должно быть позже начала (${padHm(from)}). Интервал не может переходить через полночь.`
      : "";
  const timeInvalid = !!(fromProblem || toProblem || orderProblem);
  // Вне диапазона (24:00, 10:75) — сразу; пустое или неполное — после выхода из поля или попытки отправки.
  const shownFrom = touched.from || isCompleteHm(from) ? fromProblem : "";
  const shownTo = touched.to || isCompleteHm(to) ? toProblem : "";

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    if (timeInvalid) {
      setTouched({ from: true, to: true });
      return;
    }
    const fromHm = padHm(from);
    const toHm = padHm(to);
    setBusy(true);
    let created = 0;
    const errors: string[] = [];
    for (let i = 0; i < repeat; i++) {
      const d = new Date(`${date}T12:00:00Z`);
      d.setUTCDate(d.getUTCDate() + i * 7);
      const ds = d.toISOString().slice(0, 10);
      try {
        await api("/api/my/availability", {
          method: "POST",
          json: { startsAt: zonedToUtc(ds, fromHm, tz).toISOString(), endsAt: zonedToUtc(ds, toHm, tz).toISOString() },
        });
        created++;
      } catch (err) {
        errors.push(`${ds}: ${errorMessage(err)}`);
      }
    }
    setBusy(false);
    if (created) toast(`Добавлено интервалов: ${created}`);
    if (errors.length) toast(errors[0], "err");
    load();
  };

  const remove = async () => {
    if (!removing) return;
    try {
      await api(`/api/my/availability/${removing.id}`, { method: "DELETE" });
      toast("Интервал удалён");
      load();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setRemoving(null);
    }
  };

  // По дням: доступность, занятые интервалы и свободное время. День показывается, если есть хоть что-то
  // (в том числе урок вне интервалов доступности — например, если интервал изменили после записи).
  const grouped = useMemo(() => {
    type Day = { first: string; windows: Window[]; lessons: BookedLesson[]; free: Schedule["free"] };
    const m = new Map<string, Day>();
    const day = (iso: string) => {
      const k = dayKey(iso, tz);
      if (!m.has(k)) m.set(k, { first: iso, windows: [], lessons: [], free: [] });
      return m.get(k)!;
    };
    for (const w of schedule?.availability ?? []) day(w.startsAt).windows.push(w);
    for (const l of schedule?.lessons ?? []) if (!l.alwaysOpen) day(l.startsAt).lessons.push(l);
    for (const f of schedule?.free ?? []) day(f.startsAt).free.push(f);
    return new Map([...m.entries()].sort(([a], [b]) => a.localeCompare(b)));
  }, [schedule, tz]);
  const insideAvailability = (l: BookedLesson) =>
    (schedule?.availability ?? []).some((w) => Date.parse(w.startsAt) <= Date.parse(l.startsAt) && Date.parse(l.endsAt) <= Date.parse(w.endsAt));

  if (!ready) return <Loading />;
  return (
    <div>
      <PageHeader title="Расписание" subtitle={<>Интервалы, в которые ученики могут записаться. Часовой пояс: {tzLabel(tz)}</>} />
      <div className="grid gap-6 lg:grid-cols-[360px_1fr]">
        <form onSubmit={add} className="card h-fit space-y-4 p-5">
          <h2 className="font-semibold">Добавить интервал</h2>
          <div>
            <label className="label" htmlFor="date">Дата</label>
            <input id="date" type="date" className="input" required min={todayIn(tz)} value={date} onChange={(e) => setDate(e.target.value)} />
          </div>
          <div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="label" htmlFor="from">С (ЧЧ:ММ)</label>
                <TimeInput
                  id="from"
                  value={from}
                  onChange={(v) => {
                    setFrom(v);
                    setTouched((t) => ({ ...t, from: false }));
                  }}
                  onBlur={() => setTouched((t) => ({ ...t, from: true }))}
                  invalid={!!shownFrom || !!orderProblem}
                  describedBy="time-hint"
                />
              </div>
              <div>
                <label className="label" htmlFor="to">До (ЧЧ:ММ)</label>
                <TimeInput
                  id="to"
                  value={to}
                  onChange={(v) => {
                    setTo(v);
                    setTouched((t) => ({ ...t, to: false }));
                  }}
                  onBlur={() => setTouched((t) => ({ ...t, to: true }))}
                  invalid={!!shownTo || !!orderProblem}
                  describedBy="time-hint"
                />
              </div>
            </div>
            <div id="time-hint" className="mt-1 min-h-5 text-sm" aria-live="polite" data-testid="time-status">
              {shownFrom || shownTo || orderProblem ? (
                <span className="text-red-700">
                  {shownFrom && <>«С»: {shownFrom} </>}
                  {shownTo && <>«До»: {shownTo} </>}
                  {orderProblem}
                </span>
              ) : (
                <span className="text-muted">24-часовой формат, любая минута. Часовой пояс: {tzLabel(tz)}.</span>
              )}
            </div>
          </div>
          <div>
            <label className="label" htmlFor="repeat">Повторить</label>
            <select id="repeat" className="input" value={repeat} onChange={(e) => setRepeat(Number(e.target.value))}>
              <option value={1}>Только эта дата</option>
              <option value={2}>2 недели подряд</option>
              <option value={4}>4 недели подряд</option>
              <option value={8}>8 недель подряд</option>
            </select>
          </div>
          <button className="btn-primary w-full" disabled={busy} data-testid="add-window">
            {busy && <Spinner className="h-4 w-4 text-white" />} Добавить
          </button>
          <p className="text-xs text-muted">Ученики выбирают любое начало занятия (с точностью до минуты), при котором урок целиком помещается в интервал. Уже занятое время скрывается автоматически.</p>
        </form>
        <div>
          {error && <ErrorBox message={error} onRetry={load} />}
          {items === null ? (
            <Skeleton className="h-40" />
          ) : grouped.size === 0 ? (
            <Empty icon="🕒" title="Интервалов пока нет" text="Добавьте время, когда вы готовы проводить занятия." />
          ) : (
            <div className="space-y-3">
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted" aria-label="Обозначения">
                <span><span className="mr-1 inline-block h-2.5 w-2.5 rounded-full bg-brand-400 align-middle" />Доступность — когда можно записаться</span>
                <span><span className="mr-1 inline-block h-2.5 w-2.5 rounded-full bg-amber-400 align-middle" />Занято — уже записанные занятия</span>
                <span><span className="mr-1 inline-block h-2.5 w-2.5 rounded-full bg-emerald-500 align-middle" />Свободно — что сейчас видят ученики</span>
              </div>
              {[...grouped.entries()].map(([k, d]) => (
                <div key={k} className="card space-y-2 p-4" data-testid="schedule-day">
                  <div className="font-medium capitalize">{formatDayLabel(d.first, tz)}</div>
                  <Row label="Доступность">
                    {d.windows.length === 0 ? (
                      <span className="text-sm text-muted">интервалов нет</span>
                    ) : (
                      d.windows.map((w) => (
                        <span key={w.id} className="chip gap-2 bg-brand-50 py-1.5 text-sm text-brand-800" data-testid="schedule-window">
                          {formatTime(w.startsAt, tz)}–{formatTime(w.endsAt, tz)}
                          <button className="text-brand-400 hover:text-red-600" aria-label="Удалить интервал" onClick={() => setRemoving(w)}>
                            ✕
                          </button>
                        </span>
                      ))
                    )}
                  </Row>
                  {d.lessons.length > 0 && (
                    <Row label="Занято">
                      {d.lessons.map((l) => (
                        <Link
                          key={l.id}
                          href={`/lessons/${l.id}`}
                          className="chip gap-1 bg-amber-50 py-1.5 text-sm text-amber-900 ring-1 ring-amber-200 hover:bg-amber-100"
                          data-testid="schedule-busy"
                        >
                          {formatTime(l.startsAt, tz)}–{formatTime(l.endsAt, tz)} · {l.studentName}
                          {l.status === "live" && <span className="text-emerald-700"> · идёт</span>}
                          {!insideAvailability(l) && <span className="text-amber-700"> · вне интервала доступности</span>}
                        </Link>
                      ))}
                    </Row>
                  )}
                  {(d.windows.length > 0 || d.free.length > 0) && (
                    <Row label="Свободно">
                      {d.free.length === 0 ? (
                        <span className="text-sm text-muted" data-testid="schedule-free-none">
                          свободного времени для записи нет
                        </span>
                      ) : (
                        d.free.map((f) => (
                          <span key={f.startsAt} className="chip bg-emerald-50 py-1.5 text-sm text-emerald-800" data-testid="schedule-free">
                            {formatTime(f.startsAt, tz)}–{formatTime(f.endsAt, tz)} · {minutesBetween(f.startsAt, f.endsAt)} мин
                          </span>
                        ))
                      )}
                    </Row>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
      <ConfirmModal
        open={!!removing}
        title="Удалить интервал?"
        text="Ученики больше не смогут записаться на это время. Уже созданные занятия сохранятся и останутся в расписании как «Занято»."
        confirmLabel="Удалить"
        danger
        onClose={() => setRemoving(null)}
        onConfirm={remove}
      />
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="w-24 shrink-0 text-xs font-semibold text-muted">{label}</div>
      <div className="flex flex-1 flex-wrap gap-2">{children}</div>
    </div>
  );
}

function minutesBetween(a: string, b: string) {
  return Math.round((Date.parse(b) - Date.parse(a)) / 60000);
}
