// Вход в виртуальный класс из кабинетов преподавателя и ученика, синхронизация доски и чата.
//   BASE=https://localhost:8443 DEMO_PASSWORD=... OUT=./out node classroom-entry.mjs
//   NOVIDEO=1 — проверка режима без LiveKit (класс открывается с сообщением о недоступности видео).
import { chromium } from "playwright-core";
import { mkdirSync } from "node:fs";

const BASE = process.env.BASE ?? "https://localhost:8443";
const PW = process.env.DEMO_PASSWORD;
const OUT = process.env.OUT ?? "./e2e-out";
const NOVIDEO = process.env.NOVIDEO === "1";
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
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browser = await chromium.launch({
  executablePath: process.env.CHROME ?? "/usr/bin/google-chrome",
  args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream", "--autoplay-policy=no-user-gesture-required"],
});

async function login(email, viewport = { width: 1440, height: 900 }) {
  const ctx = await browser.newContext({ viewport, permissions: ["camera", "microphone"], ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.log(`   [${email.split("@")[0]}] pageerror:`, e.message));
  await page.goto(`${BASE}/login`);
  await page.fill("#email", email);
  await page.fill("#password", PW);
  await page.click("button:has-text('Войти')");
  await page.waitForURL("**/dashboard");
  return { ctx, page };
}

async function enterFromDashboard(u, who) {
  // Кабинет: блок «Виртуальный класс открыт» и раздел «Мои занятия»
  await u.page.waitForSelector("h2:has-text('Мои занятия')");
  await u.page.waitForSelector("text=Виртуальный класс открыт");
  await u.page.screenshot({ path: `${OUT}/${who}-dashboard.png` });
  const cards = u.page.locator("#my-lessons a:has-text('Войти в класс')");
  if ((await cards.count()) < 1) throw new Error("в «Моих занятиях» нет кнопки «Войти в класс»");
  await u.page.locator("section[aria-label='Открытый класс'] a:has-text('Войти в класс')").click();
  await u.page.waitForURL("**/room");
  if (!NOVIDEO) await u.page.getByRole("button", { name: "Войти в класс" }).click({ timeout: 20000 });
  await u.page.waitForSelector("text=Чат занятия", { state: "attached", timeout: 20000 });
  await u.page.waitForSelector("text=Синхронизация доски…", { state: "detached", timeout: 15000 });
}

async function draw(page, tool, x, y, w, h) {
  await page.getByRole("toolbar", { name: "Инструменты доски" }).getByTitle(new RegExp(`^${tool}`)).click();
  const box = await page.locator("canvas.excalidraw__canvas.interactive").boundingBox();
  await page.mouse.move(box.x + x, box.y + y);
  await page.mouse.down();
  for (let i = 1; i <= 8; i++) await page.mouse.move(box.x + x + (w * i) / 8, box.y + y + (h * Math.sin(i)) / 2 + (h * i) / 8);
  await page.mouse.up();
}

async function boardCount(ctx, id) {
  const j = await (await ctx.request.get(`${BASE}/api/lessons/${id}/board`)).json();
  return j.elements.filter((e) => !e.isDeleted).length;
}

console.log(`Вход в класс из кабинетов (${NOVIDEO ? "без LiveKit" : "с видеосвязью"})`);
const teacher = await login("teacher@demo.ortalyq.local");
const student = await login("student@demo.ortalyq.local", { width: 412, height: 915 });

await step("преподаватель входит в класс из кабинета", () => enterFromDashboard(teacher, "teacher"));
await step("ученик (телефон) входит в класс из кабинета", () => enterFromDashboard(student, "student"));
const lessonId = teacher.page.url().split("/lessons/")[1].split("/")[0];
await step("оба вошли в одну и ту же комнату", async () => {
  const sid = student.page.url().split("/lessons/")[1].split("/")[0];
  if (sid !== lessonId) throw new Error(`${lessonId} ≠ ${sid}`);
});

if (NOVIDEO) {
  await step("видны сообщение «Видеосвязь недоступна» и неактивные кнопки камеры", async () => {
    await teacher.page.waitForSelector("text=Видеосвязь недоступна");
    if (!(await teacher.page.getByRole("button", { name: "Камера: недоступно" }).isDisabled())) throw new Error("кнопка камеры активна");
  });
} else {
  await step("видео собеседника воспроизводится у обоих", async () => {
    for (const u of [teacher, student])
      await u.page.waitForFunction(() => [...document.querySelectorAll("video")].some((v) => !v.className.includes("-scale-x-100") && v.videoWidth > 0), null, { timeout: 30000 });
  });
}

await step("присутствие: «оба участника в классе»", async () => {
  await teacher.page.waitForSelector("text=оба участника в классе", { timeout: 20000 });
});

await step("доска: карандаш и прямоугольник преподавателя видны ученику", async () => {
  await draw(teacher.page, "Карандаш", 320, 200, 260, 120);
  await draw(teacher.page, "Прямоугольник", 650, 180, 220, 140);
  await sleep(1500);
  if ((await boardCount(student.ctx, lessonId)) < 2) throw new Error("элементы не дошли до сервера");
});

await step("доска: текст преподавателя синхронизирован", async () => {
  await teacher.page.getByRole("toolbar", { name: "Инструменты доски" }).getByTitle(/^Текст/).click();
  const box = await teacher.page.locator("canvas.excalidraw__canvas.interactive").boundingBox();
  await teacher.page.mouse.click(box.x + 330, box.y + 420);
  await teacher.page.keyboard.type("x² − 5x + 6 = 0");
  await teacher.page.keyboard.press("Escape");
  await sleep(1500);
  const j = await (await student.ctx.request.get(`${BASE}/api/lessons/${lessonId}/board`)).json();
  if (!j.elements.some((e) => e.type === "text" && !e.isDeleted && e.text.includes("5x"))) throw new Error("текст не найден");
});

await step("доска: рисунок ученика с телефона виден преподавателю", async () => {
  const before = await boardCount(teacher.ctx, lessonId);
  await draw(student.page, "Круг", 120, 380, 120, 90);
  await sleep(1500);
  if ((await boardCount(teacher.ctx, lessonId)) <= before) throw new Error("рисунок ученика не дошёл");
});

await step("чат: сообщения в обе стороны", async () => {
  await teacher.page.fill("input[aria-label='Сообщение']", "Решим уравнение на доске");
  await teacher.page.keyboard.press("Enter");
  await student.page.getByRole("button", { name: /^Чат/ }).click();
  await student.page.waitForSelector("text=Решим уравнение на доске", { timeout: 10000 });
  await student.page.fill("input[aria-label='Сообщение']", "x = 2 и x = 3");
  await student.page.keyboard.press("Enter");
  await teacher.page.waitForSelector("text=x = 2 и x = 3", { timeout: 10000 });
  await student.page.getByRole("button", { name: /^Доска/ }).click();
});

await step("ластик удаляет объект у обоих", async () => {
  const before = await boardCount(teacher.ctx, lessonId);
  await teacher.page.getByRole("toolbar", { name: "Инструменты доски" }).getByTitle(/^Ластик/).click();
  const box = await teacher.page.locator("canvas.excalidraw__canvas.interactive").boundingBox();
  await teacher.page.mouse.move(box.x + 640, box.y + 250);
  await teacher.page.mouse.down();
  await teacher.page.mouse.move(box.x + 680, box.y + 250, { steps: 6 });
  await teacher.page.mouse.up();
  await sleep(1500);
  const after = await boardCount(student.ctx, lessonId);
  if (after >= before) throw new Error(`было ${before}, стало ${after}`);
  await draw(teacher.page, "Прямоугольник", 650, 180, 220, 140); // вернём фигуру для скриншота
  await teacher.page.getByRole("toolbar", { name: "Инструменты доски" }).getByTitle(/^Выбор/).click();
  await sleep(2000);
});

await teacher.page.screenshot({ path: `${OUT}/classroom-teacher.png` });
await student.page.screenshot({ path: `${OUT}/classroom-student-mobile.png` });

await step("посторонний ученик не попадает в этот класс", async () => {
  const o = await login("student2@demo.ortalyq.local");
  await o.page.goto(`${BASE}/lessons/${lessonId}/room`);
  await o.page.waitForSelector("text=Не удалось открыть класс", { timeout: 15000 });
  await o.ctx.close();
});

await step("кнопки выхода и завершения на месте", async () => {
  await teacher.page.getByRole("button", { name: "Завершить занятие" }).waitFor();
  await teacher.page.getByRole("link", { name: "Выйти из класса" }).waitFor();
  await student.page.getByRole("link", { name: "Выйти из класса" }).waitFor();
  if (await student.page.getByRole("button", { name: "Завершить занятие" }).count()) throw new Error("у ученика есть кнопка завершения");
});

await browser.close();
console.log(failed ? `\nОшибок: ${failed}` : "\nВсе проверки пройдены");
process.exit(failed ? 1 : 0);
