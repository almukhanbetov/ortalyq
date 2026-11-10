// Один урок — один статус у обоих участников, даже если часы или часовой пояс устройства неверны.
//   BASE=http://localhost:13000 OUT=./out node clock-skew.mjs
//
// Регрессия: статус («Запланировано» / «Идёт занятие»), остаток времени и кнопка входа считались по
// часам устройства. У ученика с часовым поясом устройства UTC+3 и выставленным вручную алматинским
// временем Date.now() опережал реальное на 2 часа: урок 22:33–23:33 в 20:36 «шёл, осталось 57 минут»,
// а у преподавателя в то же время был «Запланировано, через 2 часа».
//
// Сценарии: часы устройства ученика +2 ч и −2 ч, часы преподавателя верны или сдвинуты в другую сторону;
// часовые пояса браузеров различаются, в профилях — Asia/Almaty. Урок начинается через 1 ч 57 мин.
// Тест регистрирует собственных пользователей (…@e2e.ortalyq.test) — запускайте на тестовом стенде.
// Один запуск использует 4 регистрации и длится ~3 минуты (лимит — 20 в час с одного IP).
import { chromium } from "playwright-core";
import { mkdirSync } from "node:fs";
import { randomBytes } from "node:crypto";

const BASE = process.env.BASE ?? "http://localhost:13000";
const OUT = process.env.OUT ?? "./e2e-out";
mkdirSync(OUT, { recursive: true });
const TZ = "Asia/Almaty";
const RUN = `${Date.now().toString(36)}${randomBytes(3).toString("hex")}`;
const PASSWORD = `e2e-${randomBytes(9).toString("hex")}`;
const MIN = 60_000;
const HOUR = 60 * MIN;

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
function expect(cond, msg) {
  if (!cond) throw new Error(msg);
}
function partsIn(date, tz) {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat("en-CA", { timeZone: tz, hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })
      .formatToParts(date)
      .map((x) => [x.type, x.value]),
  );
  return { day: `${p.year}-${p.month}-${p.day}`, hm: `${p.hour}:${p.minute}` };
}

const browser = await chromium.launch({ executablePath: process.env.CHROME ?? "/usr/bin/google-chrome" });

/** Пользователь в своём браузере: часовой пояс устройства browserTz, часы устройства сдвинуты на skew мс. */
async function register(key, role, browserTz, skew) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "ru-RU", timezoneId: browserTz });
  // Часы идут с обычной скоростью, но со сдвигом — как неверно выставленные часы устройства.
  await ctx.clock.install({ time: Date.now() + skew });
  const res = await ctx.request.post(`${BASE}/api/auth/register`, {
    data: { email: `${key}-${RUN}@e2e.ortalyq.test`, password: PASSWORD, name: `E2E ${key} ${RUN}`, role, timezone: TZ },
    headers: { Origin: BASE },
  });
  if (res.status() >= 300) throw new Error(`регистрация ${key}: ${res.status()} ${await res.text()}`);
  const me = await (await ctx.request.get(`${BASE}/api/auth/me`)).json();
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.log(`   [${key}] pageerror:`, e.message));
  return { ctx, page, id: me.user.id, key, skew, browserTz };
}
async function api(ctx, method, path, data) {
  return ctx.request.fetch(`${BASE}${path}`, { method, data, headers: { Origin: BASE } });
}

/** Статус, остаток и наличие кнопки входа на странице урока и в «Моих занятиях». */
async function view(u, lessonId) {
  await u.page.goto(`${BASE}/lessons/${lessonId}`);
  const main = u.page.locator("main");
  await main.getByText(/Запланировано|Идёт занятие|Завершено/).first().waitFor({ timeout: 15000 });
  await u.page.waitForTimeout(300); // пересчёт после замера серверного времени
  const status = (await main.getByText(/^(Запланировано|Идёт занятие|Завершено)$/).first().innerText()).trim();
  const timing = ((await u.page.getByTestId("lesson-timing").innerText().catch(() => "")) || "").replace(/^\s*·\s*/, "").trim();
  const enter = (await main.getByRole("link", { name: /Войти в класс/ }).count()) > 0;
  await u.page.goto(`${BASE}/lessons`);
  const card = u.page.locator(".card", { has: u.page.locator(`a[href='/lessons/${lessonId}']`) }).first();
  await card.waitFor({ timeout: 15000 });
  await u.page.waitForTimeout(300);
  const cardText = (await card.innerText()).replace(/\s+/g, " ");
  const deviceNow = await u.page.evaluate(() => Date.now());
  return { status, timing, enter, cardText, deviceShift: Math.round((deviceNow - Date.now()) / MIN) };
}

async function scenario(title, teacherOpt, studentOpt) {
  console.log(`\n${title}`);
  const t = await register(`t-${teacherOpt.key}`, "teacher", teacherOpt.tz, teacherOpt.skew);
  const s = await register(`s-${studentOpt.key}`, "student", studentOpt.tz, studentOpt.skew);
  try {
    await api(t.ctx, "PUT", "/api/me/profile", { headline: "E2E", bio: "Создан автотестом часов.", subjects: ["Математика"], price: 6000, experienceYears: 1 });
    // Как на скриншотах: доступность «сейчас + 0:01 … + 3:10», урок 60 мин через 1 ч 57 мин.
    const now = Math.floor(Date.now() / MIN) * MIN;
    const start = new Date(now + 117 * MIN);
    const end = new Date(start.getTime() + HOUR);
    const r = await api(t.ctx, "POST", "/api/my/availability", { startsAt: new Date(now + MIN).toISOString(), endsAt: new Date(now + 190 * MIN).toISOString() });
    expect(r.status() === 201, `доступность: ${r.status()} ${await r.text()}`);
    const { day, hm } = partsIn(start, TZ);
    let lessonId = "";

    await step(`запись через форму при часах устройства ${studentOpt.skew > 0 ? "+" : ""}${studentOpt.skew / HOUR} ч: ${day} ${hm} ${TZ} отправляется как ${start.toISOString()}`, async () => {
      await s.page.goto(`${BASE}/teachers/${t.id}`);
      await s.page.waitForSelector("[data-testid='free-intervals']", { timeout: 20000 });
      await s.page.locator(`[role='option'][data-day='${day}']`).click({ timeout: 5000 });
      await s.page.fill("#start-time", hm);
      await s.page.waitForTimeout(200);
      const st = await s.page.getByTestId("start-status").innerText();
      expect(st.includes("время свободно"), `форма: ${st}`);
      await s.page.getByTestId("book-open").click();
      const [resp] = await Promise.all([
        s.page.waitForResponse((x) => x.url().endsWith("/api/lessons") && x.request().method() === "POST"),
        s.page.getByRole("dialog").getByRole("button", { name: "Записаться" }).click(),
      ]);
      const sent = resp.request().postDataJSON();
      expect(resp.status() === 201, `запись: ${resp.status()} ${await resp.text()}`);
      expect(sent.startsAt === start.toISOString() && sent.duration === 60, `отправлено ${JSON.stringify(sent)}`);
      lessonId = (await resp.json()).id;
    });
    if (!lessonId) {
      // Чтобы проверить отображение и при неудаче формы — запись через API
      const b = await api(s.ctx, "POST", "/api/lessons", { teacherId: t.id, startsAt: start.toISOString(), duration: 60, subject: "Математика" });
      expect(b.status() === 201, `запись через API: ${b.status()} ${await b.text()}`);
      lessonId = (await b.json()).id;
    }

    await step("ответ API: абсолютные моменты с явным часовым поясом, окончание = начало + 60 мин, цена 6 000 ₸", async () => {
      const { lesson } = await (await api(s.ctx, "GET", `/api/lessons/${lessonId}`)).json();
      expect(/(Z|[+-]\d\d:\d\d)$/.test(lesson.startsAt) && /(Z|[+-]\d\d:\d\d)$/.test(lesson.endsAt), `без смещения: ${lesson.startsAt} ${lesson.endsAt}`);
      expect(Date.parse(lesson.startsAt) === start.getTime() && Date.parse(lesson.endsAt) === end.getTime(), `${lesson.startsAt}–${lesson.endsAt}`);
      expect(lesson.price === 6000, `цена ${lesson.price}`);
    });

    const vt = await view(t, lessonId);
    const vs = await view(s, lessonId);
    console.log(`   преподаватель (${t.browserTz}, часы ${vt.deviceShift >= 0 ? "+" : ""}${vt.deviceShift} мин): ${vt.status}, ${vt.timing}, вход: ${vt.enter}`);
    console.log(`   ученик        (${s.browserTz}, часы ${vs.deviceShift >= 0 ? "+" : ""}${vs.deviceShift} мин): ${vs.status}, ${vs.timing}, вход: ${vs.enter}`);

    await step("одинаковые статус и остаток у обоих участников: «Запланировано», «через 2 часа», входа нет", async () => {
      for (const [who, v] of [["преподаватель", vt], ["ученик", vs]]) {
        expect(v.status === "Запланировано" && v.timing === "через 2 часа" && !v.enter, `${who}: ${v.status}, ${v.timing}, вход ${v.enter}`);
      }
    });
    await step(`время урока в поясе профиля (${hm}) одинаково в «Моих занятиях» у обоих`, async () => {
      const re = new RegExp(`${hm}\\s*–\\s*${partsIn(end, TZ).hm}.*через 2 часа`);
      expect(re.test(vt.cardText) && re.test(vs.cardText) && vt.cardText.includes("Запланировано") && vs.cardText.includes("Запланировано"), `преподаватель: ${vt.cardText} | ученик: ${vs.cardText}`);
    });
    await step("право входа решает сервер: до открытия окна вход отклоняется, класс закрыт", async () => {
      const j = await api(s.ctx, "POST", `/api/lessons/${lessonId}/join`);
      expect(j.status() >= 400 && (await j.text()).includes("откроется"), `join: ${j.status()}`);
      await s.page.goto(`${BASE}/lessons/${lessonId}/room`);
      await s.page.getByText("Класс ещё закрыт").waitFor({ timeout: 15000 });
    });
    await s.page.goto(`${BASE}/lessons/${lessonId}`);
    await s.page.screenshot({ path: `${OUT}/clock-skew-${studentOpt.key}.png`, fullPage: true });

    if (studentOpt.live) {
      // Идущий урок: начало через ~2 минуты; ждём начала по реальному времени.
      const ls = new Date(Math.floor(Date.now() / MIN) * MIN + 2 * MIN);
      const b = await api(s.ctx, "POST", "/api/lessons", { teacherId: t.id, startsAt: ls.toISOString(), duration: 30, subject: "Математика" });
      expect(b.status() === 201, `запись идущего урока: ${b.status()} ${await b.text()}`);
      const liveId = (await b.json()).id;
      console.log("   (ждём начала второго урока ~2 мин)");
      await new Promise((r) => setTimeout(r, ls.getTime() - Date.now() + 5000));
      const [lt, lsv] = [await view(t, liveId), await view(s, liveId)];
      console.log(`   идущий урок: преподаватель — ${lt.status}, ${lt.timing}; ученик — ${lsv.status}, ${lsv.timing}`);
      await step("идущий урок: у обоих «Идёт занятие», одинаковый остаток, вход открыт (и по серверу)", async () => {
        for (const [who, v] of [["преподаватель", lt], ["ученик", lsv]]) {
          expect(v.status === "Идёт занятие" && /^осталось (2\d|30) минут/.test(v.timing) && v.enter, `${who}: ${v.status}, ${v.timing}, вход ${v.enter}`);
        }
        expect(lt.timing === lsv.timing, `остаток различается: ${lt.timing} / ${lsv.timing}`);
        for (const u of [t, s]) {
          const d = await (await api(u.ctx, "GET", `/api/lessons/${liveId}`)).json();
          expect(d.canJoin === true && Date.parse(d.joinClosesAt) === ls.getTime() + 30 * MIN, `${u.key}: canJoin ${d.canJoin}, закрытие ${d.joinClosesAt}`);
        }
      });
    }
  } finally {
    await t.ctx.close();
    await s.ctx.close();
  }
}

console.log("Статус урока при неверных часах устройства");
await scenario(
  "Ученик: пояс устройства Europe/Moscow, часы +2 ч (как на скриншотах); преподаватель: Asia/Almaty, часы верны",
  { key: "ok", tz: "Asia/Almaty", skew: 0 },
  { key: "plus2", tz: "Europe/Moscow", skew: 2 * HOUR, live: true },
);
await scenario(
  "Ученик: America/New_York, часы −2 ч; преподаватель: Asia/Tokyo, часы +2 ч",
  { key: "plus2", tz: "Asia/Tokyo", skew: 2 * HOUR },
  { key: "minus2", tz: "America/New_York", skew: -2 * HOUR },
);

await browser.close();
console.log(failed ? `\nОшибок: ${failed}` : "\nВсе проверки пройдены");
process.exit(failed ? 1 : 0);
