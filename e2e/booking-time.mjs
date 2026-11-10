// Запись на урок с произвольным временем начала (ЧЧ:ММ): свободные интервалы, причины отказа,
// без автосдвига времени, часовой пояс, конкурентная запись.
//   BASE=http://localhost:13000 OUT=./out node booking-time.mjs
//
// Тест не зависит от даты запуска и демо-данных: при каждом запуске он регистрирует собственных
// преподавателей и учеников (уникальные email ...@e2e.ortalyq.test, имена с пометкой «E2E») и задаёт им
// окна доступности на фиксированное время в днях относительно «сегодня» по Алматы. У новых аккаунтов
// нет занятий, поэтому нужное время гарантированно свободно. Демо-данные не изменяются, но созданные
// аккаунты остаются в базе — запускайте на тестовом или локальном стенде, не на рабочем.
// Регистрация ограничена 20 попытками в час с одного IP; один запуск использует 4.
import { chromium } from "playwright-core";
import { mkdirSync } from "node:fs";
import { randomBytes } from "node:crypto";

const BASE = process.env.BASE ?? "http://localhost:13000";
const OUT = process.env.OUT ?? "./e2e-out";
mkdirSync(OUT, { recursive: true });

const TZ = "Asia/Almaty";
const RUN = `${Date.now().toString(36)}${randomBytes(3).toString("hex")}`;
const PASSWORD = `e2e-${randomBytes(9).toString("hex")}`;
const SUBJECT = "Математика";

let failed = 0;
async function step(name, fn) {
  try {
    await fn();
    console.log("  ✔", name);
  } catch (e) {
    failed++;
    console.log("  ✘", name, "—", (e?.message ?? String(e)).split("\n")[0]);
  }
}
const toMin = (hm) => Number(hm.slice(0, 2)) * 60 + Number(hm.slice(3));
const fromMin = (m) => `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;

// ---- Время в часовом поясе (без зависимостей; учитывает текущее смещение пояса) ----
function partsIn(date, tz) {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat("en-CA", { timeZone: tz, hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" })
      .formatToParts(date)
      .map((x) => [x.type, x.value]),
  );
  return { day: `${p.year}-${p.month}-${p.day}`, hm: `${p.hour}:${p.minute}`, sec: Number(p.second) };
}
/** Момент UTC для «день ЧЧ:ММ» в поясе tz. */
function zoned(day, hm, tz) {
  const guess = new Date(`${day}T${hm}:00Z`);
  const { day: d2, hm: hm2 } = partsIn(guess, tz);
  const diff = Date.parse(`${d2}T${hm2}:00Z`) - guess.getTime(); // смещение пояса
  return new Date(guess.getTime() - diff);
}
function addDays(day, n) {
  const d = new Date(`${day}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

// В последние минуты суток «сегодня» по Алматы может не остаться свободного времени — дождёмся новых суток.
{
  const { hm, sec } = partsIn(new Date(), TZ);
  if (toMin(hm) >= 23 * 60 + 57) {
    const waitMs = ((24 * 60 - toMin(hm)) * 60 - sec + 5) * 1000;
    console.log(`  (ждём начала суток по Алматы: ${Math.ceil(waitMs / 1000)} с)`);
    await new Promise((r) => setTimeout(r, waitMs));
  }
}
const TODAY = partsIn(new Date(), TZ).day;
const D1 = addDays(TODAY, 2); // запись, границы, пересечения
const D2 = addDays(TODAY, 3); // одновременная запись
const D3 = addDays(TODAY, 4); // другой часовой пояс

const browser = await chromium.launch({ executablePath: process.env.CHROME ?? "/usr/bin/google-chrome" });

/** Регистрирует нового пользователя в отдельном контексте браузера (сессия — в cookie контекста). */
async function register(key, role) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, ignoreHTTPSErrors: true, locale: "ru-RU" });
  const email = `${key}-${RUN}@e2e.ortalyq.test`;
  const res = await ctx.request.post(`${BASE}/api/auth/register`, {
    data: { email, password: PASSWORD, name: `E2E ${key} ${RUN}`, role, timezone: TZ },
    headers: { Origin: BASE },
  });
  if (res.status() !== 201 && res.status() !== 200) throw new Error(`регистрация ${key}: ${res.status()} ${await res.text()}`);
  const me = await (await ctx.request.get(`${BASE}/api/auth/me`)).json();
  if (!me?.user?.id) throw new Error(`регистрация ${key}: нет сессии`);
  return { ctx, id: me.user.id, email };
}
async function api(ctx, method, path, data) {
  const res = await ctx.request.fetch(`${BASE}${path}`, { method, data, headers: { Origin: BASE } });
  if (res.status() >= 300) throw new Error(`${method} ${path}: ${res.status()} ${await res.text()}`);
  return res;
}
async function makeTeacher(key, windows) {
  const t = await register(key, "teacher");
  await api(t.ctx, "PUT", "/api/me/profile", { headline: "E2E-преподаватель", bio: "Создан автотестом записи.", subjects: [SUBJECT], price: 6000, experienceYears: 1 });
  for (const [from, to] of windows) await api(t.ctx, "POST", "/api/my/availability", { startsAt: from.toISOString(), endsAt: to.toISOString() });
  await t.ctx.close();
  return t;
}

// ---- Тестовые данные ----
const now = new Date();
const minute = 60_000;
const nowMin = new Date(Math.floor(now.getTime() / minute) * minute);
const t1 = await makeTeacher("t1", [
  // Окно, захватывающее текущий момент: у преподавателя гарантированно есть «сегодня»
  [new Date(nowMin.getTime() - 60 * minute), new Date(nowMin.getTime() + 3 * 60 * minute)],
  [zoned(D1, "10:00", TZ), zoned(D1, "14:00", TZ)],
  [zoned(D2, "10:00", TZ), zoned(D2, "14:00", TZ)],
  [zoned(D3, "10:00", TZ), zoned(D3, "14:00", TZ)],
]);
const t2 = await makeTeacher("t2", [[zoned(D1, "10:00", TZ), zoned(D1, "14:00", TZ)]]);
const s = await register("student", "student");
const o = await register("student2", "student");
s.page = await s.ctx.newPage();
s.page.on("pageerror", (e) => console.log("   [student] pageerror:", e.message));
console.log(`  тестовые данные: запуск ${RUN}, сегодня ${TODAY}, дни записи ${D1}, ${D2}, ${D3} (${TZ})`);

const status = () => s.page.getByTestId("start-status").innerText();
const bookBtn = () => s.page.getByTestId("book-open");

async function openTeacher(t) {
  await s.page.goto(`${BASE}/teachers/${t.id}`);
  await s.page.waitForSelector("[data-testid='free-intervals']", { timeout: 20000 });
}
/** Выбрать день по дате и вернуть диапазоны свободного времени этого дня. */
async function pickDay(day) {
  await s.page.locator(`[role='listbox'][aria-label='Дата занятия'] [role='option'][data-day='${day}']`).click({ timeout: 5000 });
  const items = await s.page.getByTestId("free-intervals").locator("li").allInnerTexts();
  return items.map((text) => {
    const m = text.match(/(\d\d:\d\d)–(\d\d:\d\d).*начало с (\d\d:\d\d) до (\d\d:\d\d)/);
    if (!m) throw new Error(`интервал не распознан: ${text}`);
    return { freeFrom: m[1], freeTo: m[2], from: m[3], to: m[4] };
  });
}
async function setTime(hm) {
  await s.page.fill("#start-time", hm);
  await s.page.waitForTimeout(150);
}
function expect(cond, msg) {
  if (!cond) throw new Error(msg);
}

console.log("Запись на урок: произвольное время начала");
const booked = "10:07";

await step("форма показывает часовой пояс и свободные интервалы с диапазоном начала", async () => {
  await openTeacher(t1);
  const tz = await s.page.getByTestId("booking-tz").innerText();
  expect(/Asia\/Almaty \(GMT\+5\)/.test(tz), tz);
  const r = await pickDay(D1);
  // Окно 10:00–14:00, урок 60 мин: начало с 10:00 до 13:00
  expect(JSON.stringify(r) === JSON.stringify([{ freeFrom: "10:00", freeTo: "14:00", from: "10:00", to: "13:00" }]), JSON.stringify(r));
});

await step("поле «Время начала» в формате ЧЧ:ММ (24 ч): набор 1942 → 19:42", async () => {
  await s.page.fill("#start-time", "");
  await s.page.locator("#start-time").pressSequentially("1942");
  const v = await s.page.inputValue("#start-time");
  expect(v === "19:42", v);
});

await step("запись на нестандартное время (10:07) — успешно, время на странице урока совпадает", async () => {
  await setTime(booked);
  const st = await status();
  expect(st.includes("время свободно"), st);
  await bookBtn().click();
  await s.page.getByRole("dialog").getByRole("button", { name: "Записаться" }).click();
  await s.page.waitForURL(/\/lessons\/[0-9a-f-]{36}$/, { timeout: 15000 });
  await s.page.locator("main").getByText(/10:07\s*–\s*11:07/).first().waitFor({ timeout: 10000 });
});

await step("выход за границу интервала: причина, кнопка неактивна, время не сдвинуто", async () => {
  await openTeacher(t1);
  const r = await pickDay(D1);
  // После урока 10:07–11:07 свободно 11:07–14:00 (окно 10:00–10:07 короче урока и не показывается)
  expect(JSON.stringify(r) === JSON.stringify([{ freeFrom: "11:07", freeTo: "14:00", from: "11:07", to: "13:00" }]), JSON.stringify(r));
  await setTime("13:01");
  const st = await status();
  expect(st.includes("Последнее возможное начало — 13:00"), st);
  expect(await bookBtn().isDisabled(), "кнопка активна");
  expect((await s.page.inputValue("#start-time")) === "13:01", "время изменено");
});

await step("пересечение с уже записанным уроком: время недоступно", async () => {
  await setTime("10:17");
  const st = await status();
  expect(st.includes("Преподаватель недоступен в 10:17"), st);
  expect(await bookBtn().isDisabled(), "кнопка активна");
});

await step("пересечение со своим уроком у другого преподавателя — причина «У вас уже есть занятие»", async () => {
  await openTeacher(t2);
  const r = await pickDay(D1);
  expect(r.length === 1 && r[0].freeFrom === "10:00" && r[0].freeTo === "14:00", `у второго преподавателя ${D1} должно быть свободно 10:00–14:00: ${JSON.stringify(r)}`);
  await setTime("10:22");
  const st = await status();
  expect(st.includes("У вас уже есть занятие 10:07–11:07"), st);
  expect(await bookBtn().isDisabled(), "кнопка активна");
});

await step("прошедшее время сегодня отклоняется", async () => {
  await openTeacher(t1);
  await pickDay(TODAY); // у первого преподавателя есть окно, захватывающее текущий момент
  await setTime("00:00"); // начало суток всегда в прошлом
  const st = await status();
  expect(st.includes("уже прошло"), st);
  expect(await bookBtn().isDisabled(), "кнопка активна");
  // Прошедшая часть окна не показывается свободной: первое начало — не раньше текущей минуты
  const first = (await pickDay(TODAY))[0];
  const cur = partsIn(new Date(), TZ).hm;
  expect(toMin(first.from) >= toMin(cur), `первое начало ${first.from} раньше текущего ${cur}`);
});

await step("одновременная запись: другой ученик занял время раньше — причина от сервера, время не сдвинуто", async () => {
  await openTeacher(t1);
  await pickDay(D2);
  const t = "10:23";
  await setTime(t);
  expect((await status()).includes("время свободно"), await status());
  await bookBtn().click();
  // Пока окно подтверждения открыто, второй ученик бронирует это же время
  const other = await o.ctx.request.post(`${BASE}/api/lessons`, {
    data: { teacherId: t1.id, startsAt: zoned(D2, t, TZ).toISOString(), duration: 60, subject: SUBJECT },
    headers: { Origin: BASE },
  });
  expect(other.status() === 201, `второй ученик: ${other.status()} ${await other.text()}`);
  await s.page.getByRole("dialog").getByRole("button", { name: "Записаться" }).click();
  await s.page.waitForFunction(() => /занятием преподавателя|занял другой ученик/.test(document.querySelector("[data-testid='start-status']")?.textContent ?? ""), null, { timeout: 10000 });
  expect((await s.page.inputValue("#start-time")) === t, "время изменено");
  expect(s.page.url().includes("/teachers/"), "переход на страницу урока");
});

await step("другой часовой пояс (Europe/Moscow): интервалы сдвигаются на −2 ч, пояс указан в форме", async () => {
  await openTeacher(t1);
  const almaty = await pickDay(D3);
  expect(almaty[0]?.freeFrom === "10:00" && almaty[0]?.freeTo === "14:00", JSON.stringify(almaty));
  await api(s.ctx, "PATCH", "/api/me", { timezone: "Europe/Moscow" });
  try {
    await openTeacher(t1);
    const tz = await s.page.getByTestId("booking-tz").innerText();
    expect(tz.includes("Europe/Moscow"), tz);
    const msk = await pickDay(D3);
    expect(msk[0]?.freeFrom === "08:00" && msk[0]?.freeTo === "12:00" && msk[0]?.to === "11:00", `ожидалось 08:00–12:00: ${JSON.stringify(msk)}`);
  } finally {
    await api(s.ctx, "PATCH", "/api/me", { timezone: TZ });
  }
});

await s.page.screenshot({ path: `${OUT}/booking-time.png`, fullPage: true });
await browser.close();
console.log(failed ? `\nОшибок: ${failed}` : "\nВсе проверки пройдены");
process.exit(failed ? 1 : 0);
