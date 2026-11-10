"use client";

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

export default function AvailabilityPage() {
  const { ready, tz } = useRequireUser(["teacher"]);
  const [items, setItems] = useState<Window[] | null>(null);
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
      setItems((await api<{ availability: Window[] }>("/api/my/availability")).availability);
    } catch (e) {
      setError(errorMessage(e));
      setItems([]);
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

  const grouped = useMemo(() => {
    const m = new Map<string, Window[]>();
    for (const w of items ?? []) {
      const k = dayKey(w.startsAt, tz);
      if (!m.has(k)) m.set(k, []);
      m.get(k)!.push(w);
    }
    return m;
  }, [items, tz]);

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
          ) : items.length === 0 ? (
            <Empty icon="🕒" title="Интервалов пока нет" text="Добавьте время, когда вы готовы проводить занятия." />
          ) : (
            <div className="space-y-3">
              {[...grouped.entries()].map(([k, list]) => (
                <div key={k} className="card flex flex-wrap items-center gap-3 p-4">
                  <div className="w-32 font-medium capitalize">{formatDayLabel(list[0].startsAt, tz)}</div>
                  <div className="flex flex-1 flex-wrap gap-2">
                    {list.map((w) => (
                      <span key={w.id} className="chip gap-2 bg-brand-50 py-1.5 text-sm text-brand-800">
                        {formatTime(w.startsAt, tz)}–{formatTime(w.endsAt, tz)}
                        <button className="text-brand-400 hover:text-red-600" aria-label="Удалить интервал" onClick={() => setRemoving(w)}>
                          ✕
                        </button>
                      </span>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
      <ConfirmModal
        open={!!removing}
        title="Удалить интервал?"
        text="Ученики больше не смогут записаться на это время. Уже созданные занятия сохранятся."
        confirmLabel="Удалить"
        danger
        onClose={() => setRemoving(null)}
        onConfirm={remove}
      />
    </div>
  );
}
