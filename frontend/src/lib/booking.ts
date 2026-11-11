// Выбор времени записи: свободные интервалы преподавателя и проверка начала урока.
// Начало — любая минута (без сетки); время никогда не сдвигается автоматически —
// недопустимое отклоняется с понятной причиной. Окончательная проверка — на сервере.
import { addDaysIso, hmIn, isoDayIn, zonedToUtc } from "./format";
import { serverNow } from "./serverClock";

export interface FreeInterval {
  startsAt: string; // свободно с
  endsAt: string; // свободно до
  lastStartAt: string; // последнее начало урока выбранной длительности
}
export interface Busy {
  startsAt: string;
  endsAt: string;
}
/** Почему нет свободного времени (коды с сервера, их может быть несколько). */
export type EmptyReason = "" | "no_availability" | "later_only" | "booked" | "too_short" | "today_passed";

/** Предстоящий урок самого ученика у этого преподавателя (только свои данные). */
export interface OwnLesson {
  id: string;
  subject: string;
  startsAt: string;
  endsAt: string;
  status: "scheduled" | "live";
  alwaysOpen: boolean;
}

export interface SlotsDiagnosis {
  reasons: EmptyReason[];
  maxFreeMinutes: number;
  windowsChecked: number;
  bookedWindows: number;
  shortWindows: number;
  passedToday: number;
  nextWindowAt: string | null;
}

export interface SlotsResponse {
  intervals: FreeInterval[];
  myLessons: Busy[];
  emptyReason?: EmptyReason;
  emptyReasons?: EmptyReason[];
  diagnosis?: SlotsDiagnosis;
  maxFreeMinutes?: number;
  checkedUntil?: string;
  myLessonsWithTeacher?: OwnLesson[];
}

const DURATIONS = [30, 45, 60, 90];

/** Подтверждённые причины пустой выдачи — по строке на причину (без сведений о чужих занятиях). */
export function emptySlotsReasons(r: SlotsResponse, duration: number, fmtDate: (iso: string) => string): string[] {
  const d = r.diagnosis;
  const reasons = r.emptyReasons ?? (r.emptyReason ? [r.emptyReason] : []);
  const until = r.checkedUntil ? ` (проверено до ${fmtDate(r.checkedUntil)})` : " на ближайшие 3 недели";
  const out: string[] = [];
  for (const x of reasons) {
    switch (x) {
      case "no_availability":
        out.push(`Преподаватель не добавил будущие интервалы для записи${until}.`);
        break;
      case "later_only":
        out.push(`В ближайшие 3 недели интервалов нет; ближайший — ${d?.nextWindowAt ? fmtDate(d.nextWindowAt) : "позже"}.`);
        break;
      case "too_short": {
        const max = d?.maxFreeMinutes ?? r.maxFreeMinutes ?? 0;
        const fit = DURATIONS.filter((v) => v <= max && v < duration);
        out.push(
          `Свободные промежутки есть, но самый длинный — ${max} мин, а выбран урок ${duration} мин.` +
            (fit.length ? ` Подойдёт длительность ${fit.join(" или ")} мин.` : ""),
        );
        break;
      }
      case "booked":
        out.push(d && d.bookedWindows > 0 ? `Время в ${d.bookedWindows} из ${d.windowsChecked} интервалов уже полностью занято.` : "Подходящее время уже занято.");
        break;
      case "today_passed":
        out.push("Сегодняшнее доступное время преподавателя уже прошло.");
        break;
    }
  }
  if (!out.length) out.push(`Нет свободных интервалов для урока ${duration} мин${until}.`);
  return out;
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
export function rangesByDay(intervals: FreeInterval[], tz: string, now = new Date(serverNow())): Map<string, DayRange[]> {
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
  now = new Date(serverNow()),
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
