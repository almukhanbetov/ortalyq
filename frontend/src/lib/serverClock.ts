// Серверное время на клиенте. Статус урока («Запланировано» / «Идёт занятие»), остаток времени
// и кнопка входа считаются от абсолютного момента «сейчас». Часы устройства могут быть неверны:
// например, при часовом поясе устройства UTC+3 и вручную выставленном алматинском времени
// Date.now() опережает реальное на 2 часа, и урок у такого участника «идёт», а у другого — «через 2 часа».
// Поэтому «сейчас» = часы устройства + смещение до сервера, измеренное по заголовку X-Server-Time
// каждого ответа API. Пояс отображения — из профиля, на сравнение моментов он не влияет.

export const SERVER_TIME_HEADER = "X-Server-Time";

let offset = 0; // серверное время − часы устройства, мс
let bestRtt = Infinity;
let measuredAt = 0;
const listeners = new Set<() => void>();

/** Учитывает ответ сервера: sentAt и receivedAt — часы устройства до запроса и после ответа. */
export function noteServerTime(header: string | null, sentAt: number, receivedAt: number) {
  const server = Number(header);
  if (!header || !Number.isFinite(server) || receivedAt < sentAt) return;
  const rtt = receivedAt - sentAt;
  // Предпочитаем самый быстрый ответ (меньше погрешность), но раз в минуту принимаем свежий замер —
  // на случай, если часы устройства переставили, пока страница открыта.
  if (rtt > bestRtt && receivedAt - measuredAt < 60_000) return;
  const next = server - (sentAt + receivedAt) / 2;
  bestRtt = rtt;
  measuredAt = receivedAt;
  if (Math.abs(next - offset) < 1000) {
    offset = next;
    return;
  }
  offset = next;
  listeners.forEach((l) => l());
}

/** Текущий момент по серверным часам (мс с эпохи UTC). */
export function serverNow(): number {
  return Date.now() + offset;
}

/** Смещение часов устройства относительно сервера (мс); для диагностики. */
export function clockOffset(): number {
  return offset;
}

/** Подписка на заметное (≥ 1 с) изменение смещения; возвращает отписку. */
export function onServerClockChange(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
