// «Учебная мастерская» в классе: два браузера (преподаватель и ученик) на демо-классе.
//   BASE=http://localhost:13000 DEMO_PASSWORD=... OUT=./out node workshop.mjs
// ИИ — НЕ реальный: бэкенд запускается с OPENAI_BASE_URL на e2e/fake-openai.mjs (образцы ответов [ТЕСТ]),
// ключ OPENAI_API_KEY=sk-fake-e2e-key, OPENAI_TIMEOUT_SECONDS=5, WORKSHOP_AI_PER_MINUTE=20 (сценарий делает 7 запросов). Проверяются интерфейс, права, публикация,
// скрытие ответов и обработка ошибок; качество ответов настоящего ИИ здесь не проверяется.
// Сценарий завершает демо-урок: после него выполните `server demo-room` (или seed).
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
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, what, timeout = 10000) {
  const end = Date.now() + timeout;
  let last;
  while (Date.now() < end) {
    try {
      last = await fn();
      if (last) return last;
    } catch (e) {
      last = e;
    }
    await sleep(150);
  }
  throw new Error(`не дождались: ${what} (последнее: ${typeof last === "string" ? JSON.stringify(last).slice(0, 200) : last})`);
}
function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

const browser = await chromium.launch({
  executablePath: process.env.CHROME ?? "/usr/bin/google-chrome",
  args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream"],
});

async function login(email, viewport = { width: 1440, height: 900 }, extra = {}) {
  const ctx = await browser.newContext({ viewport, permissions: ["camera", "microphone"], ignoreHTTPSErrors: true, ...extra });
  const page = await ctx.newPage();
  const frames = [];
  page.on("pageerror", (e) => console.log(`   [${email.split("@")[0]}] pageerror:`, e.message));
  page.on("websocket", (ws) => ws.on("framereceived", (f) => frames.push(String(f.payload))));
  await page.goto(`${BASE}/login`);
  await page.fill("#email", email);
  await page.fill("#password", PW);
  await page.click("button:has-text('Войти')");
  await page.waitForURL("**/dashboard");
  return { ctx, page, frames };
}

async function enterRoom(page, lessonId) {
  await page.goto(`${BASE}/lessons/${lessonId}/room`);
  const join = page.getByRole("button", { name: "Войти в класс" });
  await Promise.race([join.waitFor({ timeout: 20000 }).then(() => join.click()), page.waitForSelector("text=Чат занятия", { state: "attached", timeout: 20000 })]);
  await page.waitForSelector("text=Чат занятия", { state: "attached", timeout: 20000 });
}

async function openWorkshop(page) {
  await page.getByTestId("tab-workshop").click();
  await page.getByTestId("workshop-text").waitFor({ timeout: 30000 });
  await until(async () => (await page.getByTestId("workshop-sync").textContent())?.includes("синхронизировано"), "синхронизация учебного текста", 15000);
}

const text = (page) => page.getByTestId("workshop-text").inputValue();
const apiGet = (page, path) => page.evaluate(async (p) => {
  const r = await fetch(p, { credentials: "include" });
  return { status: r.status, body: await r.text() };
}, path);
const apiPost = (page, path, json) => page.evaluate(async ([p, j]) => {
  const r = await fetch(p, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json" }, body: JSON.stringify(j) });
  return { status: r.status, body: await r.text() };
}, [path, json]);

async function selectText(page, needle) {
  await page.getByTestId("workshop-text").evaluate((el, n) => {
    const i = el.value.indexOf(n);
    el.focus();
    el.setSelectionRange(i, i + n.length);
    el.dispatchEvent(new Event("select", { bubbles: true }));
  }, needle);
  await until(async () => (await page.getByTestId("workshop-selection").textContent())?.includes(String(needle.length)), "выделение фрагмента");
}

// Видно только преподавателю всегда: ответы и советы по проверке ДЗ, поля черновиков.
const SECRET = ["СЕКРЕТ", "answerKey", "checkingTips", '"draft"'];
// До публикации первого разбора его текст тоже скрыт от ученика.
const DRAFT1 = "Черновик-разбора";
// Разбор английского режима так и не публикуется — ученик не должен его видеть никогда.
const DRAFT_EN = "She goes to school.";

const teacher = await login("teacher@demo.ortalyq.local");
const student = await login("student@demo.ortalyq.local");
const lessons = JSON.parse((await apiGet(teacher.page, "/api/lessons?scope=upcoming")).body).lessons;
const lesson = lessons.find((l) => l.alwaysOpen);
if (!lesson) throw new Error("Демо-класс не найден — выполните seed/demo-room");
const W = `/api/lessons/${lesson.id}/workshop`;
console.log(`Демо-класс ${lesson.id} (${lesson.subject})`);

await step("оба участника открывают вкладку «Учебная мастерская»", async () => {
  await Promise.all([enterRoom(teacher.page, lesson.id), enterRoom(student.page, lesson.id)]);
  await Promise.all([openWorkshop(teacher.page), openWorkshop(student.page)]);
  assert(await teacher.page.getByTestId("workshop-action-explain").isVisible(), "у преподавателя есть действия");
  assert((await student.page.getByTestId("workshop-action-explain").count()) === 0, "у ученика нет действий преподавателя");
});

await step("совместное редактирование: текст преподавателя виден ученику", async () => {
  await teacher.page.getByTestId("workshop-text").pressSequentially("Задача: найдите площадь. Ответ ученика: 42.\n");
  await until(async () => (await text(student.page)).includes("Ответ ученика: 42."), "текст у ученика");
});

await step("одновременный ввод двух участников без потери правок", async () => {
  const tEl = teacher.page.getByTestId("workshop-text");
  const sEl = student.page.getByTestId("workshop-text");
  await tEl.evaluate((el) => { el.focus(); el.setSelectionRange(0, 0); });
  await sEl.evaluate((el) => { el.focus(); el.setSelectionRange(el.value.length, el.value.length); });
  await Promise.all([teacher.page.keyboard.type("[T-начало] ", { delay: 15 }), student.page.keyboard.type("She go to school every day.", { delay: 15 })]);
  await until(async () => {
    const [a, b] = await Promise.all([text(teacher.page), text(student.page)]);
    return a === b && a.includes("[T-начало]") && a.includes("She go to school every day.") && a.includes("Ответ ученика: 42.");
  }, "одинаковый текст с правками обоих", 15000);
});

await step("текст сохраняется после перезагрузки", async () => {
  await sleep(2000); // периодическое сохранение на сервере — 1,5 с
  await student.page.reload();
  await enterRoom(student.page, lesson.id);
  await openWorkshop(student.page);
  await until(async () => (await text(student.page)).includes("She go to school every day."), "текст после перезагрузки");
});

await step("посторонний (второй ученик) не получает мастерскую чужого урока", async () => {
  const other = await login("student2@demo.ortalyq.local");
  const r1 = await apiGet(other.page, W);
  const r2 = await apiGet(other.page, `${W}/doc`);
  const r3 = await apiPost(other.page, `${W}/analyses`, { action: "explain", level: "standard", fragment: "x" });
  assert(r1.status === 404 && r2.status === 404 && r3.status === 404, `ожидали 404: ${r1.status} ${r2.status} ${r3.status}`);
  await other.ctx.close();
});

await step("ученик не может вызвать действия преподавателя через API", async () => {
  const codes = [
    (await apiPost(student.page, `${W}/analyses`, { action: "explain", level: "standard", fragment: "x" })).status,
    (await apiPost(student.page, `${W}/homework`, { text: "x", level: "standard" })).status,
    (await apiPost(student.page, `${W}/ai-notice`, {})).status,
  ];
  assert(codes.every((c) => c === 403), `ожидали 403: ${codes}`);
});

await step("перед первым запросом — предупреждение о передаче текста внешнему ИИ", async () => {
  await selectText(teacher.page, "Задача: найдите площадь.");
  await teacher.page.getByTestId("workshop-action-explain").click();
  await teacher.page.getByText("Передача текста внешнему ИИ").waitFor({ timeout: 5000 });
  await teacher.page.getByTestId("workshop-notice-accept").click();
  await teacher.page.getByTestId("analysis-item").first().waitFor({ timeout: 15000 });
  const item = teacher.page.getByTestId("analysis-item").first();
  assert((await item.textContent()).includes("Видно только вам"), "разбор сначала только у преподавателя");
  assert((await item.textContent()).includes("Рекомендация ИИ"), "пометка «рекомендация»");
  assert(!(await text(teacher.page)).includes("[ТЕСТ]"), "исходный текст не заменяется");
});

await step("ученик не видит неопубликованный разбор (интерфейс, API, WebSocket)", async () => {
  await sleep(800);
  assert((await student.page.getByTestId("analysis-item").count()) === 0, "в интерфейсе ученика пусто");
  const r = await apiGet(student.page, W);
  for (const s of [...SECRET, DRAFT1]) assert(!r.body.includes(s), `в ответе API ученику: ${s}`);
  for (const f of student.frames) for (const s of [...SECRET, DRAFT1]) assert(!f.includes(s), `в сообщении WebSocket ученику: ${s}`);
});

await step("«Показать ученику» — разбор появляется у ученика, HTML показывается как текст", async () => {
  await teacher.page.getByTestId("analysis-publish").first().click();
  await student.page.getByTestId("analysis-item").first().waitFor({ timeout: 10000 });
  const t = await student.page.getByTestId("analysis-item").first().textContent();
  assert(t.includes("<b>не HTML</b>") && t.includes("Задача: найдите площадь."), `содержимое: ${t}`);
  assert((await student.page.locator("[data-testid=analysis-item] b").count()) === 0, "HTML не исполняется");
});

await step("«Проверить ответ» показывает вердикт «Не хватает условия»", async () => {
  await selectText(teacher.page, "Ответ ученика: 42.");
  await teacher.page.getByTestId("workshop-action-check").click();
  await until(async () => (await teacher.page.getByTestId("analysis-verdict").allTextContents()).includes("Не хватает условия"), "вердикт");
});

await step("режим «Английский»: сохраняется для урока, исправление грамматики", async () => {
  await teacher.page.getByTestId("workshop-mode").selectOption("english");
  await until(async () => (await student.page.getByText("Режим: Английский").count()) > 0, "режим у ученика");
  await selectText(teacher.page, "She go to school every day.");
  await teacher.page.getByTestId("workshop-action-correct").click();
  await until(async () => (await teacher.page.getByTestId("analysis-item").first().textContent()).includes("Исправленный вариант"), "исправление");
  const st = JSON.parse((await apiGet(teacher.page, W)).body);
  assert(st.mode === "english", "режим сохранён на сервере");
});

await step("ошибки провайдера: 500, некорректный ответ, таймаут — понятные сообщения", async () => {
  const before = await teacher.page.getByTestId("analysis-item").count();
  for (const [word, expect] of [["FAKE_500", "временно недоступен"], ["FAKE_BADJSON", "неожиданном формате"], ["FAKE_TIMEOUT", "не ответил вовремя"]]) {
    await teacher.page.getByTestId("workshop-text").evaluate((el, w) => {
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }, word);
    await teacher.page.keyboard.type(` ${word}`);
    await selectText(teacher.page, word);
    await teacher.page.getByTestId("workshop-action-explain").click();
    await until(async () => (await teacher.page.getByTestId("workshop-ai-error").textContent())?.includes(expect), `сообщение «${expect}»`, 15000);
    const msg = await teacher.page.getByTestId("workshop-ai-error").textContent();
    assert(!/sk-|openai\.com|127\.0\.0\.1|status/i.test(msg), `сообщение без технических деталей: ${msg}`);
  }
  assert((await teacher.page.getByTestId("analysis-item").count()) === before, "неудачные запросы не создают разборов");
});

await step("домашнее задание: черновик с ответами у преподавателя, публикация без ответов", async () => {
  await selectText(teacher.page, "She go to school every day.");
  await teacher.page.getByTestId("workshop-homework").click();
  await teacher.page.getByTestId("hw-item").waitFor({ timeout: 15000 });
  assert((await teacher.page.getByTestId("hw-answers").textContent()).includes("Ответ-5-СЕКРЕТ"), "ответы у преподавателя");
  assert((await student.page.getByTestId("hw-item").count()) === 0, "черновик не виден ученику");
  await teacher.page.getByTestId("hw-publish").click();
  await student.page.getByTestId("hw-item").waitFor({ timeout: 10000 });
  assert((await student.page.getByTestId("hw-exercise").count()) === 5, "5 упражнений у ученика");
  const dom = await student.page.content();
  for (const s of [...SECRET, DRAFT_EN]) assert(!dom.includes(s), `в интерфейсе ученика: ${s}`);
  const r = await apiGet(student.page, W);
  for (const s of [...SECRET, DRAFT_EN]) assert(!r.body.includes(s), `в API ученика: ${s}`);
  for (const f of student.frames) for (const s of [...SECRET, DRAFT_EN]) assert(!f.includes(s), `в WebSocket ученика: ${s}`);
});

await step("телефон: вкладка мастерской без горизонтальной прокрутки", async () => {
  const phone = await login("student@demo.ortalyq.local", { width: 390, height: 844 }, { isMobile: true, hasTouch: true });
  await enterRoom(phone.page, lesson.id);
  await openWorkshop(phone.page);
  await phone.page.getByTestId("hw-item").waitFor({ timeout: 10000 });
  const overflow = await phone.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  assert(overflow <= 1, `горизонтальная прокрутка ${overflow}px`);
  await phone.page.screenshot({ path: `${OUT}/workshop-phone.png` });
  await phone.ctx.close();
});

await teacher.page.screenshot({ path: `${OUT}/workshop-teacher.png` });
await student.page.screenshot({ path: `${OUT}/workshop-student.png` });

await step("после завершения урока: редактирование и ИИ закрыты, опубликованное доступно", async () => {
  const end = await apiPost(teacher.page, `/api/lessons/${lesson.id}/end`, {});
  assert(end.status === 200, `end ${end.status}`);
  const ai = await apiPost(teacher.page, `${W}/analyses`, { action: "explain", level: "standard", fragment: "x" });
  assert(ai.status === 409, `ИИ после окончания: ${ai.status}`);
  await student.page.goto(`${BASE}/lessons/${lesson.id}`);
  await student.page.getByTestId("lesson-workshop").waitFor();
  await student.page.getByTestId("hw-item").waitFor({ timeout: 10000 });
  assert((await student.page.getByTestId("analysis-item").count()) === 1, "один опубликованный разбор");
  await student.page.getByRole("button", { name: "Показать учебный текст" }).click();
  await until(async () => (await student.page.getByTestId("lesson-workshop-text").textContent())?.includes("She go to school"), "сохранённый текст");
  const dom = await student.page.content();
  for (const s of [...SECRET, DRAFT_EN]) assert(!dom.includes(s), `на странице урока у ученика: ${s}`);
  await teacher.page.goto(`${BASE}/lessons/${lesson.id}`);
  await teacher.page.getByTestId("lesson-workshop").waitFor();
  await until(async () => (await teacher.page.getByTestId("analysis-item").count()) >= 3, "у преподавателя все разборы");
  await student.page.screenshot({ path: `${OUT}/workshop-lesson-page.png`, fullPage: true });
});

await browser.close();
console.log(failed ? `\n${failed} шаг(ов) не прошли` : "\nВсе шаги прошли");
process.exit(failed ? 1 : 0);
