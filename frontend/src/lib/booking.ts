// Выбор времени записи: свободные интервалы преподавателя и проверка начала урока.
// Начало — любая минута (без сетки); время никогда не сдвигается автоматически —
// недопустимое отклоняется с понятной причиной. Окончательная проверка — на сервере.
import { addDaysIso, hmIn, isoDayIn, zonedToUtc } from "./format";

export interface FreeInterval {
  startsAt: string; // свободно с
  endsAt: string; // свободно до
  lastStartAt: string; // последнее начало урока выбранной длительности
}
export interface Busy {
  startsAt: string;
  endsAt: string;
}
/** Допустимые начала в пределах одного дня (в часовом поясе пользователя). */
export interface DayRange {
  from: Date;
  to: Date;
  /** Начало свободного времени, но не раньше текущего момента. */
  freeFrom: Date;
  interval: FreeInterval;
}

const MIN = 60_000;

/** Первая целая минута строго позже now — как earliestStart на сервере. */
export function earliestStart(now: Date): number {
  return Math.floor(now.getTime() / MIN) * MIN + MIN;
}

/**
 * Группирует интервалы по дням пользователя: день → диапазоны допустимого начала.
 * Уже прошедшее время отбрасывается (интервалы могли быть загружены давно).
 */
export function rangesByDay(intervals: FreeInterval[], tz: string, now = new Date()): Map<string, DayRange[]> {
  const out = new Map<string, DayRange[]>();
  const min = earliestStart(now);
  for (const iv of intervals) {
    const ivFrom = Math.max(new Date(iv.startsAt).getTime(), min);
    if (ivFrom > new Date(iv.lastStartAt).getTime()) continue;
    const last = new Date(iv.lastStartAt).getTime();
    let day = isoDayIn(new Date(ivFrom), tz);
    const lastDay = isoDayIn(iv.lastStartAt, tz);
    for (let guard = 0; day <= lastDay && guard < 40; guard++, day = addDaysIso(day, 1)) {
      const dayStart = zonedToUtc(day, "00:00", tz).getTime();
      const next = zonedToUtc(addDaysIso(day, 1), "00:00", tz).getTime();
      const from = Math.max(ivFrom, dayStart);
      const to = Math.min(last, next - MIN);
      if (from > to) continue;
      if (!out.has(day)) out.set(day, []);
      out.get(day)!.push({ from: new Date(from), to: new Date(to), freeFrom: new Date(ivFrom), interval: iv });
    }
  }
  return out;
}

export type StartCheck = { ok: true; start: Date; end: Date } | { ok: false; reason: string };

export function checkStart(
  day: string,
  time: string,
  duration: number,
  tz: string,
  intervals: FreeInterval[],
  myLessons: Busy[],
  now = new Date(),
): StartCheck {
  if (!day) return { ok: false, reason: "Выберите дату." };
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(time)) return { ok: false, reason: "Укажите время начала в формате ЧЧ:ММ, например 10:05." };
  const start = zonedToUtc(day, time, tz);
  if (hmIn(start, tz) !== time || isoDayIn(start, tz) !== day) {
    return { ok: false, reason: `Времени ${time} в этот день нет из-за перевода часов. Укажите другое время.` };
  }
  const end = new Date(start.getTime() + duration * MIN);
  if (start.getTime() <= now.getTime()) {
    return { ok: false, reason: `Время начала уже прошло (сейчас ${hmIn(now, tz)}). Выберите время в будущем.` };
  }
  const t = start.getTime();
  // Интервал, в котором свободно в момент начала
  const iv = intervals.find((x) => new Date(x.startsAt).getTime() <= t && t < new Date(x.endsAt).getTime());
  if (!iv) {
    return { ok: false, reason: `Преподаватель недоступен в ${time}. Выберите время внутри одного из свободных интервалов.` };
  }
  if (t > new Date(iv.lastStartAt).getTime()) {
    return {
      ok: false,
      reason: `Урок ${duration} мин закончится в ${hmIn(end, tz)} — позже конца свободного времени (${hmIn(iv.endsAt, tz)}). Последнее возможное начало — ${hmIn(iv.lastStartAt, tz)}.`,
    };
  }
  const mine = myLessons.find((l) => new Date(l.startsAt).getTime() < end.getTime() && t < new Date(l.endsAt).getTime());
  if (mine) {
    return { ok: false, reason: `У вас уже есть занятие ${hmIn(mine.startsAt, tz)}–${hmIn(mine.endsAt, tz)}, которое пересекается с выбранным временем.` };
  }
  return { ok: true, start, end };
}
