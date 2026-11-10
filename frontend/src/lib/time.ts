// Время суток ЧЧ:ММ в 24-часовом формате — независимо от языка браузера и ОС.
// Используется вместо <input type="time">, который в части окружений показывает AM/PM.

/** Маска ввода ЧЧ:ММ: только цифры и двоеточие, не более 2 цифр в части. Значение не сдвигается, только форматируется. */
export function maskHm(raw: string): string {
  if (raw.includes(":")) {
    const [h, m = ""] = raw.split(":");
    return `${h.replace(/\D/g, "").slice(0, 2)}:${m.replace(/\D/g, "").slice(0, 2)}`;
  }
  const d = raw.replace(/\D/g, "").slice(0, 4);
  return d.length <= 2 ? d : `${d.slice(0, 2)}:${d.slice(2)}`;
}

/** Дополняет «9:30» до «09:30»; остальное возвращает как есть. */
export function padHm(v: string): string {
  return /^\d:\d\d$/.test(v) ? `0${v}` : v;
}

/** Введено ли время полностью (Ч:ММ или ЧЧ:ММ) — до этого ошибки не показываем. */
export function isCompleteHm(v: string): boolean {
  return /^\d{1,2}:\d\d$/.test(v);
}

/** Причина, по которой значение не является временем ЧЧ:ММ (00:00–23:59); "" — значение корректно. */
export function hmProblem(v: string): string {
  if (!v) return "Укажите время в формате ЧЧ:ММ.";
  if (!isCompleteHm(v)) return `Время «${v}» введено не полностью — нужно ЧЧ:ММ, например 09:30.`;
  const [h, m] = padHm(v).split(":").map(Number);
  if (h > 23) return "Часы — от 00 до 23.";
  if (m > 59) return "Минуты — от 00 до 59.";
  return "";
}

/** Минуты от начала суток для корректного «ЧЧ:ММ», иначе null. */
export function hmToMinutes(v: string): number | null {
  if (hmProblem(v)) return null;
  const [h, m] = padHm(v).split(":").map(Number);
  return h * 60 + m;
}
