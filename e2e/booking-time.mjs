// Запись на урок с произвольным временем начала (ЧЧ:ММ): свободные интервалы, причины отказа,
// без автосдвига времени, часовой пояс, конкурентная запись.
//   BASE=http://localhost:13000 DEMO_PASSWORD=... OUT=./out node booking-time.mjs
// Изменяет демо-данные (создаёт занятия): после проверки выполните seed.
import { chromium } from "playwright-core";
import { mkdirSync } from "node:fs";

const BASE = process.env.BASE ?? "http://localhost:13000";
const PW = process.env.DEMO_PASSWORD;
const OUT = process.env.OUT ?? "./e2e-out";
if (!PW) throw new Error("DEMO_PASSWORD не задан");
mkdirSync(OUT, { recursive: true });

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

const browser = await chromium.launch({ executablePath: process.env.CHROME ?? "/usr/bin/google-chrome" });
async function login(email) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, ignoreHTTPSErrors: true, locale: "ru-RU" });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.log(`   [${email.split("@")[0]}] pageerror:`, e.message));
  await page.goto(`${BASE}/login`);
  await page.fill("#email", email);
  await page.fill("#password", PW);
  await page.click("button:has-text('Войти')");
  await page.waitForURL("**/dashboard");
  return { ctx, page };
}

const s = await login("student@demo.ortalyq.local");
const o = await login("student2@demo.ortalyq.local");
const teachers = (await (await s.ctx.request.get(`${BASE}/api/teachers`)).json()).teachers;
const [t1, t2] = teachers;
const status = () => s.page.getByTestId("start-status").innerText();
const bookBtn = () => s.page.getByTestId("book-open");

async function openTeacher(t) {
  await s.page.goto(`${BASE}/teachers/${t.id}`);
  await s.page.waitForSelector("[data-testid='free-intervals']", { timeout: 20000 });
}
/** Выбрать день (второй в списке — не сегодня) и вернуть первый диапазон допустимого начала. */
async function pickDay(index = 1) {
  const chips = s.page.locator("[role='listbox'][aria-label='Дата занятия'] [role='option']");
  const n = await chips.count();
  await chips.nth(Math.min(index, n - 1)).click();
  const text = await s.page.getByTestId("free-intervals").locator("li").first().innerText();
  const m = text.match(/(\d\d:\d\d)–(\d\d:\d\d).*начало с (\d\d:\d\d) до (\d\d:\d\d)/);
  if (!m) throw new Error(`интервал не распознан: ${text}`);
  return { day: await chips.nth(Math.min(index, n - 1)).getAttribute("data-day"), freeFrom: m[1], freeTo: m[2], from: m[3], to: m[4] };
}
async function setTime(hm) {
  await s.page.fill("#start-time", hm);
  await s.page.waitForTimeout(150);
}

console.log("Запись на урок: произвольное время начала");
let r, booked;

await step("форма показывает часовой пояс и свободные интервалы с диапазоном начала", async () => {
  await openTeacher(t1);
  const tz = await s.page.getByTestId("booking-tz").innerText();
  if (!/Asia\/Almaty \(GMT\+5\)/.test(tz)) throw new Error(tz);
  r = await pickDay(1);
  // Урок 60 мин: последнее начало = конец свободного времени − 60 мин
  if (toMin(r.to) !== toMin(r.freeTo) - 60 && r.to !== "23:59") throw new Error(JSON.stringify(r));
});

await step("поле «Время начала» в формате ЧЧ:ММ (24 ч): набор 1942 → 19:42", async () => {
  await s.page.fill("#start-time", "");
  await s.page.locator("#start-time").pressSequentially("1942");
  const v = await s.page.inputValue("#start-time");
  if (v !== "19:42") throw new Error(v);
});

await step("запись на нестандартное время (начало + 7 мин) — успешно, время на странице урока совпадает", async () => {
  booked = fromMin(toMin(r.from) + 7);
  await setTime(booked);
  const st = await status();
  if (!st.includes("время свободно")) throw new Error(st);
  await bookBtn().click();
  await s.page.getByRole("dialog").getByRole("button", { name: "Записаться" }).click();
  await s.page.waitForURL(/\/lessons\/[0-9a-f-]{36}$/, { timeout: 15000 });
  await s.page.locator("main").getByText(new RegExp(`${booked}\\s*–\\s*${fromMin(toMin(booked) + 60)}`)).first().waitFor({ timeout: 10000 });
});

await step("выход за границу интервала: причина, кнопка неактивна, время не сдвинуто", async () => {
  await openTeacher(t1);
  r = await pickDay(1);
  const late = fromMin(toMin(r.to) + 1);
  await setTime(late);
  const st = await status();
  if (!st.includes(`Последнее возможное начало — ${r.to}`) && !st.includes("недоступен")) throw new Error(st);
  if (!(await bookBtn().isDisabled())) throw new Error("кнопка активна");
  if ((await s.page.inputValue("#start-time")) !== late) throw new Error("время изменено");
});

await step("пересечение с уже записанным уроком: время недоступно", async () => {
  const inside = fromMin(toMin(booked) + 10);
  await setTime(inside);
  const st = await status();
  if (!st.includes("недоступен") && !st.includes("Последнее возможное начало")) throw new Error(st);
});

await step("пересечение со своим уроком у другого преподавателя — причина «У вас уже есть занятие»", async () => {
  await openTeacher(t2);
  await s.page.locator(`[role='option'][data-day='${r.day}']`).click({ timeout: 5000 });
  const t = fromMin(toMin(booked) + 15);
  await setTime(t);
  const st = await status();
  if (!st.includes("У вас уже есть занятие") && !st.includes("недоступен")) throw new Error(st);
});

await step("прошедшее время сегодня отклоняется", async () => {
  await openTeacher(t1);
  const today = new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Almaty" }).format(new Date());
  const chip = s.page.locator(`[role='option'][data-day='${today}']`);
  if (!(await chip.count())) {
    console.log("     (сегодня свободного времени нет — проверено сервером в Go-тесте)");
    return;
  }
  await chip.click();
  await setTime("00:01");
  const st = await status();
  if (!st.includes("уже прошло")) throw new Error(st);
});

await step("одновременная запись: другой ученик занял время раньше — причина от сервера, время не сдвинуто", async () => {
  await openTeacher(t1);
  r = await pickDay(2);
  const t = fromMin(toMin(r.from) + 23);
  await setTime(t);
  if (!(await status()).includes("время свободно")) throw new Error(await status());
  await bookBtn().click();
  // Пока окно подтверждения открыто, второй ученик бронирует это же время
  const start = await s.page.evaluate(() => document.querySelector("#start-time").value);
  const [y, mo, d] = r.day.split("-").map(Number);
  const startUtc = new Date(Date.UTC(y, mo - 1, d, Number(start.slice(0, 2)) - 5, Number(start.slice(3)))).toISOString();
  const other = await o.ctx.request.post(`${BASE}/api/lessons`, { data: { teacherId: t1.id, startsAt: startUtc, duration: 60, subject: t1.subjects[0] }, headers: { Origin: BASE } });
  if (other.status() !== 201) throw new Error(`второй ученик: ${other.status()} ${await other.text()}`);
  await s.page.getByRole("dialog").getByRole("button", { name: "Записаться" }).click();
  await s.page.waitForFunction(() => /занятием преподавателя|занял другой ученик/.test(document.querySelector("[data-testid='start-status']")?.textContent ?? ""), null, { timeout: 10000 });
  if ((await s.page.inputValue("#start-time")) !== t) throw new Error("время изменено");
  if (!s.page.url().includes("/teachers/")) throw new Error("переход на страницу урока");
});

await step("другой часовой пояс (Europe/Moscow): интервалы сдвигаются на −2 ч, пояс указан в форме", async () => {
  await openTeacher(t1);
  const almaty = await pickDay(3);
  await s.ctx.request.patch(`${BASE}/api/me`, { data: { timezone: "Europe/Moscow" }, headers: { Origin: BASE } });
  try {
    await openTeacher(t1);
    const tz = await s.page.getByTestId("booking-tz").innerText();
    if (!tz.includes("Europe/Moscow")) throw new Error(tz);
    await s.page.locator(`[role='option'][data-day='${almaty.day}']`).click();
    const text = await s.page.getByTestId("free-intervals").innerText();
    const shifted = fromMin(toMin(almaty.freeFrom) - 120);
    if (!text.includes(shifted)) throw new Error(`ожидалось ${shifted}: ${text}`);
  } finally {
    await s.ctx.request.patch(`${BASE}/api/me`, { data: { timezone: "Asia/Almaty" }, headers: { Origin: BASE } });
  }
});

await s.page.screenshot({ path: `${OUT}/booking-time.png`, fullPage: true });
await browser.close();
console.log(failed ? `\nОшибок: ${failed}` : "\nВсе проверки пройдены");
process.exit(failed ? 1 : 0);
