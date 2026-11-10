// Состояние забронированного урока по времени. Сравниваются абсолютные моменты (мс с эпохи),
// поэтому результат не зависит от часового пояса браузера и пользователя; отображается время в поясе пользователя.
// «Сейчас» — по серверным часам (serverNow), а не по часам устройства: у обоих участников один статус.
import type { Lesson } from "./api";
import { plural } from "./format";
import { serverNow } from "./serverClock";

/** scheduled — до начала; live — с начала до окончания; ended — время урока прошло (сервер скоро закроет его). */
export type LessonPhase = "scheduled" | "live" | "ended" | "completed" | "cancelled";

type Timed = Pick<Lesson, "status" | "startsAt" | "endsAt" | "alwaysOpen">;

export function lessonPhase(l: Timed, now = serverNow()): LessonPhase {
  if (l.status === "cancelled" || l.status === "completed") return l.status;
  if (l.alwaysOpen) return l.status; // постоянно открытый демо-класс не стареет
  if (now < Date.parse(l.startsAt)) return "scheduled";
  if (now < Date.parse(l.endsAt)) return "live";
  return "ended";
}

/** Урок ещё предстоит или идёт — показывается в «Предстоящих» до момента окончания. */
export function isUpcoming(l: Timed, now = serverNow()) {
  const p = lessonPhase(l, now);
  return p === "scheduled" || p === "live";
}

/** Вход в класс: за earlyMin минут до начала и до окончания урока (без продления при опоздании). */
export function canEnter(l: Timed, earlyMin = 15, now = serverNow()) {
  if (!isUpcoming(l, now)) return false;
  return l.alwaysOpen || now >= Date.parse(l.startsAt) - earlyMin * 60000;
}

function minutesText(min: number) {
  if (min < 60) return `${min} ${plural(min, "минута", "минуты", "минут")}`;
  const h = Math.floor(min / 60),
    m = min % 60;
  return `${h} ч${m ? ` ${m} мин` : ""}`;
}

/** Подпись к времени урока: «через 25 минут», «осталось 55 минут»; после окончания — пусто. */
export function lessonTiming(l: Timed, now = serverNow()): string {
  if (l.alwaysOpen) return "";
  switch (lessonPhase(l, now)) {
    case "scheduled": {
      const min = Math.ceil((Date.parse(l.startsAt) - now) / 60000);
      if (min < 60) return `через ${min} ${plural(min, "минуту", "минуты", "минут")}`;
      const h = Math.round(min / 60);
      if (h < 24) return `через ${h} ${plural(h, "час", "часа", "часов")}`;
      const d = Math.round(h / 24);
      return `через ${d} ${plural(d, "день", "дня", "дней")}`;
    }
    case "live": {
      // Округление вверх: в 17:25:00 на урок до 18:20 — «осталось 55 минут», в 18:19:30 — «осталось 1 минута».
      const left = Math.ceil((Date.parse(l.endsAt) - now) / 60000);
      return `осталось ${minutesText(left)}`;
    }
    default: // закончившийся, завершённый, отменённый — статус говорит сам за себя
      return "";
  }
}
