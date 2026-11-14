// Бесплатный ознакомительный урок: преподаватель начинает урок без расписания, ученик входит по приглашению
// в другой браузерной сессии (с возвратом к приглашению после входа), тот же класс, защита от посторонних,
// возврат после перезагрузки, завершение кнопкой и сохранённые материалы.
//   BASE=https://localhost:8443 OUT=./out node trial-lesson.mjs
//   NOVIDEO=1 — бэкенд без LiveKit (класс открывается без видео).
//   PSQL="psql <url>" — дополнительно сверить, что обычные уроки, расписание и цены не изменились.
// Учётные записи создаются заново (регистрация), seed-данные не нужны.
import { chromium, devices } from "playwright-core";
import { execSync } from "node:child_process";
import { mkdirSync } from "node:fs";

const BASE = process.env.BASE ?? "https://localhost:8443";
const OUT = process.env.OUT ?? "./e2e-out";
const NOVIDEO = process.env.NOVIDEO === "1";
const PW = "Trial-" + Math.random().toString(36).slice(2, 10) + "!9";
const SFX = Date.now().toString(36);
mkdirSync(OUT, { recursive: true });

let failed = 0;
async function step(name, fn) {
  try {
    await fn();
    console.log("  ✔", name);
  } catch (e) {
    failed++;
    console.log("  ✘", name, "—", e.message.split("\n")[0]);
  }
}
const until = async (fn, what, ms = 15000) => {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return;
    await new Promise((r) => setTimeout(r, 300));
  }
  throw new Error(`не дождались: ${what}`);
};
const snapshot = () =>
  process.env.PSQL
    ? execSync(
        `${process.env.PSQL} -Atc "SELECT md5(coalesce(string_agg(x, '|' ORDER BY x), '')) FROM (SELECT 'l'||id||status||starts_at||ends_at||price AS x FROM lessons WHERE NOT is_trial UNION ALL SELECT 'a'||id||starts_at||ends_at FROM availability UNION ALL SELECT 'p'||user_id||price FROM teacher_profiles) q"`,
      )
        .toString()
        .trim()
    : "";

const browser = await chromium.launch({
  executablePath: process.env.CHROME ?? "/usr/bin/google-chrome",
  args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream", "--autoplay-policy=no-user-gesture-required"],
});

async function session(who, opts = {}) {
  const ctx = await browser.newContext({ viewport: { width: 1366, height: 860 }, permissions: ["camera", "microphone"], ignoreHTTPSErrors: true, ...opts });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.log(`   [${who}] pageerror:`, e.message));
  return { ctx, page, who };
}
async function register(s, role) {
  const email = `${s.who}-${SFX}@trial.test`;
  const r = await s.page.request.post(`${BASE}/api/auth/register`, {
    headers: { Origin: BASE },
    data: { name: `${role === "teacher" ? "Преподаватель" : "Ученик"} ${s.who}`, email, password: PW, role, timezone: "Asia/Almaty" },
  });
  if (r.status() !== 201 && r.status() !== 200) throw new Error(`регистрация ${s.who}: ${r.status()} ${await r.text()}`);
  return email;
}
async function enterClass(page) {
  if (!NOVIDEO) await page.getByRole("button", { name: "Войти в класс" }).click({ timeout: 20000 });
  await page.waitForSelector("text=Чат занятия", { state: "attached", timeout: 20000 });
}
const api = async (s, path) => {
  const r = await s.page.request.get(`${BASE}${path}`);
  return { status: r.status(), body: await r.json().catch(() => ({})) };
};

let before = "";
const teacher = await session("teacher");
const student = await session("student");
const outsider = await session("outsider");
let lessonId = "";
let inviteUrl = "";
let studentEmail = "";

await step("регистрация преподавателя и двух учеников (отдельные сессии)", async () => {
  await register(teacher, "teacher");
  // Ученик регистрируется в отдельном контексте и выходит — к приглашению он придёт без входа
  const tmp = await session("student");
  studentEmail = await register(tmp, "student");
  await tmp.ctx.close();
  await register(outsider, "student");
  before = snapshot(); // после регистрации: дальше меняться могут только ознакомительные уроки
});

await step("преподаватель: «Начать ознакомительный урок» в любое время → класс открывается сразу", async () => {
  await teacher.page.goto(`${BASE}/dashboard`);
  await teacher.page.getByTestId("start-trial").click();
  await teacher.page.waitForURL(/\/lessons\/[0-9a-f-]{36}\/room/);
  lessonId = teacher.page.url().match(/lessons\/([0-9a-f-]{36})/)[1];
  await teacher.page.getByText("Бесплатный ознакомительный урок").first().waitFor();
  await enterClass(teacher.page);
  await teacher.page.getByText("бесплатный ознакомительный урок · начат").waitFor();
  inviteUrl = await teacher.page.getByTestId("trial-invite-url").inputValue();
  if (!inviteUrl.includes(`/trial/${lessonId}#`)) throw new Error(`ссылка: ${inviteUrl}`);
  await teacher.page.screenshot({ path: `${OUT}/trial-teacher-class.png` });
});

await step("повторное нажатие: предупреждение и «Открыть идущий урок», второй урок не создан", async () => {
  const tab = await teacher.ctx.newPage();
  await tab.goto(`${BASE}/dashboard`);
  await tab.getByText("Бесплатный ознакомительный урок — идёт").waitFor(); // блок «Виртуальный класс открыт»
  await tab.getByTestId("start-trial").click();
  await tab.getByTestId("trial-conflict").waitFor();
  const href = await tab.getByTestId("trial-conflict-open").getAttribute("href");
  if (href !== `/lessons/${lessonId}/room`) throw new Error(href);
  const r = await api(teacher, "/api/lessons?scope=upcoming");
  const trials = r.body.lessons.filter((l) => l.isTrial);
  if (trials.length !== 1 || trials[0].price !== 0) throw new Error(JSON.stringify(trials));
  await tab.close();
});

await step("ученик без входа: приглашение → «Войти» → после входа возвращается к приглашению → присоединяется", async () => {
  await student.page.goto(inviteUrl);
  await student.page.getByTestId("trial-login").waitFor();
  await student.page.getByTestId("trial-login").getByRole("link", { name: "Войти" }).click();
  await student.page.waitForURL("**/login**");
  await student.page.fill("#email", studentEmail);
  await student.page.fill("#password", PW);
  await student.page.click("button:has-text('Войти')");
  await student.page.waitForURL(`**/trial/${lessonId}**`);
  await student.page.getByTestId("trial-accept").click();
  await student.page.waitForURL(`**/lessons/${lessonId}/room`);
  await enterClass(student.page);
});

await step("у преподавателя появилось имя ученика, приглашение скрыто", async () => {
  await teacher.page.getByText(/Виртуальный класс · .* · Ученик student/).waitFor({ timeout: 10000 });
  await until(async () => (await teacher.page.getByTestId("trial-invite").count()) === 0, "приглашение скрыто");
});

await step("тот же класс: чат между двумя сессиями", async () => {
  await student.page.getByLabel("Сообщение").fill("Здравствуйте, я на ознакомительном уроке");
  await student.page.getByLabel("Отправить").click();
  await teacher.page.getByText("Здравствуйте, я на ознакомительном уроке").waitFor({ timeout: 10000 });
});

await step("посторонний: приглашение занято, урок и класс недоступны", async () => {
  await outsider.page.goto(inviteUrl);
  await outsider.page.getByText("К уроку уже присоединился другой ученик").waitFor();
  const r = await api(outsider, `/api/lessons/${lessonId}`);
  if (r.status !== 404) throw new Error(`GET урока: ${r.status}`);
  await outsider.page.goto(`${BASE}/lessons/${lessonId}/room`);
  await outsider.page.getByText("Не удалось открыть класс").waitFor();
});

await step("перезагрузка страницы ученика — снова в том же классе", async () => {
  await student.page.reload();
  await enterClass(student.page);
  await student.page.getByText("Здравствуйте, я на ознакомительном уроке").waitFor({ timeout: 10000 });
});

await step("телефон: страница приглашения без горизонтальной прокрутки", async () => {
  const phone = await session("phone", { ...devices["iPhone 13"], permissions: ["camera", "microphone"] });
  await phone.page.goto(inviteUrl);
  await phone.page.getByText("К уроку уже присоединился другой ученик").waitFor();
  const over = await phone.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  if (over > 1) throw new Error(`прокрутка ${over}px`);
  await phone.page.screenshot({ path: `${OUT}/trial-phone-invite.png` });
  await phone.ctx.close();
});

await step("завершение кнопкой: у ученика класс закрывается, вход закрыт", async () => {
  await teacher.page.getByRole("button", { name: "Завершить занятие" }).click();
  await teacher.page.getByRole("dialog").getByRole("button", { name: "Завершить" }).click();
  await teacher.page.waitForURL(`**/lessons/${lessonId}`);
  await student.page.getByText(/Занятие завершено/).first().waitFor({ timeout: 15000 });
  await student.page.goto(`${BASE}/lessons/${lessonId}/room`);
  await student.page.getByText("Занятие завершено").first().waitFor();
});

await step("после завершения: материалы на странице урока, «Бесплатно», фактическое время, история", async () => {
  await teacher.page.getByTestId("lesson-price").getByText("Бесплатно").waitFor();
  await teacher.page.getByText("(фактически)").waitFor();
  await teacher.page.screenshot({ path: `${OUT}/trial-lesson-page.png`, fullPage: true });
  const r = await api(student, `/api/lessons/${lessonId}/chat`);
  if (r.status !== 200 || r.body.messages.length !== 1) throw new Error(`чат: ${JSON.stringify(r)}`);
  const h = await api(student, "/api/lessons?scope=history");
  const l = h.body.lessons.find((x) => x.id === lessonId);
  if (!l || l.status !== "completed" || !l.startedAt || !l.endedAt || l.price !== 0) throw new Error(JSON.stringify(l));
  await outsider.page.goto(inviteUrl);
  await outsider.page.getByText("Урок уже завершён").first().waitFor();
});

await step("расписание, цены и обычные уроки не изменились", async () => {
  const slots = await api(teacher, "/api/my/availability");
  if (slots.status !== 200 || slots.body.lessons?.some((x) => x.id === lessonId)) throw new Error("ознакомительный урок попал в расписание");
  if (process.env.PSQL && snapshot() !== before) throw new Error("снимок таблиц изменился");
});

await browser.close();
console.log(failed ? `\nПровалено шагов: ${failed}` : "\nВсе шаги пройдены");
process.exit(failed ? 1 : 0);
