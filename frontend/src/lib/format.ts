// Время хранится в UTC; показываем в часовом поясе пользователя.

export const DEFAULT_TZ = "Asia/Almaty";
export const CURRENCY = { code: "KZT", symbol: "₸" } as const;

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || DEFAULT_TZ;
  } catch {
    return DEFAULT_TZ;
  }
}

function fmt(tz: string, opts: Intl.DateTimeFormatOptions) {
  try {
    return new Intl.DateTimeFormat("ru-RU", { timeZone: tz, ...opts });
  } catch {
    return new Intl.DateTimeFormat("ru-RU", opts);
  }
}

export function formatDateTime(iso: string, tz: string) {
  return fmt(tz, { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" }).format(new Date(iso));
}

export function formatDate(iso: string, tz: string) {
  return fmt(tz, { day: "numeric", month: "long", year: "numeric" }).format(new Date(iso));
}

export function formatDayLabel(iso: string, tz: string) {
  return fmt(tz, { weekday: "short", day: "numeric", month: "short" }).format(new Date(iso));
}

export function formatTime(iso: string, tz: string) {
  return fmt(tz, { hour: "2-digit", minute: "2-digit" }).format(new Date(iso));
}

export function formatRange(start: string, end: string, tz: string) {
  return `${formatDateTime(start, tz)} – ${formatTime(end, tz)}`;
}

export function dayKey(iso: string, tz: string) {
  return fmt(tz, { year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date(iso));
}

export function tzLabel(tz: string) {
  try {
    const parts = new Intl.DateTimeFormat("ru-RU", { timeZone: tz, timeZoneName: "shortOffset" }).formatToParts(new Date());
    const off = parts.find((p) => p.type === "timeZoneName")?.value ?? "";
    return `${tz.replace(/_/g, " ")} (${off})`;
  } catch {
    return tz;
  }
}

// Смещение часового пояса (мс) в момент t: локальное время пояса минус UTC.
function tzOffset(t: number, tz: string): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).formatToParts(new Date(t));
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  const asUTC = Date.UTC(get("year"), get("month") - 1, get("day"), get("hour"), get("minute"), get("second"));
  return asUTC - Math.floor(t / 1000) * 1000;
}

/** Переводит «дата + время на часах пользователя» в UTC-момент. */
export function zonedToUtc(date: string, time: string, tz: string): Date {
  const [y, m, d] = date.split("-").map(Number);
  const [hh, mm] = time.split(":").map(Number);
  const wall = Date.UTC(y, m - 1, d, hh, mm);
  let guess = wall - tzOffset(wall, tz);
  guess = wall - tzOffset(guess, tz);
  return new Date(guess);
}

/** Дата YYYY-MM-DD момента t в часовом поясе tz. */
export function isoDayIn(t: Date | string, tz: string): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: tz, year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date(t));
}

/** Время HH:mm (24 ч) момента t в часовом поясе tz. */
export function hmIn(t: Date | string, tz: string): string {
  const p = new Intl.DateTimeFormat("en-GB", { timeZone: tz, hourCycle: "h23", hour: "2-digit", minute: "2-digit" }).formatToParts(new Date(t));
  return `${p.find((x) => x.type === "hour")?.value}:${p.find((x) => x.type === "minute")?.value}`;
}

/** YYYY-MM-DD + n календарных дней. */
export function addDaysIso(day: string, n: number): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10);
}

/** Сегодняшняя дата YYYY-MM-DD в часовом поясе пользователя. */
export function todayIn(tz: string): string {
  return isoDayIn(new Date(), tz);
}

/** Сумма в тенге (целые ₸): 5 000 ₸, 10 000 ₸. Разделитель тысяч — неразрывный пробел, в том числе для 4-значных сумм. */
export function formatMoney(v: number) {
  const n = Math.round(v);
  const digits = String(Math.abs(n)).replace(/\B(?=(\d{3})+(?!\d))/g, "\u00a0");
  return `${n < 0 ? "−" : ""}${digits}\u00a0${CURRENCY.symbol}`;
}

export function formatPrice(v: number | null | undefined) {
  if (v == null) return "по договорённости";
  return formatMoney(v);
}

/** +77000000001 → +7 700 000 00 01 */
export function formatPhone(v: string) {
  const m = v.match(/^\+7(\d{3})(\d{3})(\d{2})(\d{2})$/);
  return m ? `+7 ${m[1]} ${m[2]} ${m[3]} ${m[4]}` : v;
}

export function formatSize(bytes: number | null | undefined) {
  if (!bytes) return "";
  if (bytes < 1024) return `${bytes} Б`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} КБ`;
  return `${(bytes / 1024 / 1024).toFixed(1)} МБ`;
}

export function formatDuration(ms: number | null | undefined) {
  if (!ms) return "";
  const s = Math.round(ms / 1000);
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}

export function plural(n: number, one: string, few: string, many: string) {
  const m10 = n % 10,
    m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

export const statusLabels: Record<string, string> = {
  scheduled: "Запланировано",
  live: "Идёт занятие",
  ended: "Завершено", // время урока прошло, сервер ещё не закрыл его (до 30 с)
  completed: "Завершено",
  cancelled: "Отменено",
};

export const statusColors: Record<string, string> = {
  scheduled: "bg-brand-100 text-brand-800",
  live: "bg-emerald-100 text-emerald-800",
  ended: "bg-slate-100 text-slate-700",
  completed: "bg-slate-100 text-slate-700",
  cancelled: "bg-red-50 text-red-700",
};

export const recordingLabels: Record<string, string> = {
  awaiting_consent: "Ожидает согласия",
  starting: "Запускается",
  recording: "Идёт запись",
  processing: "Обрабатывается",
  ready: "Готова",
  failed: "Ошибка",
  declined: "Отклонена",
};
