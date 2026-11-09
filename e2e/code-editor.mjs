// Совместный редактор кода в классе: два браузера (преподаватель и ученик), синхронизация,
// восстановление после перезагрузки, запуск и результат у обоих, права постороннего, мобильная вёрстка.
//   BASE=http://localhost:13000 DEMO_PASSWORD=... OUT=./out node code-editor.mjs
// Переменные:
//   RUNNER=none — бэкенд запущен БЕЗ CODE_RUNNER_URL: проверяется сообщение «Сервис выполнения не настроен».
//   RUNNER=stub — исполнитель — заглушка Judge0 (код не выполняется, ответы по образцам); проверяются
//                 маршрут и отображение статусов. С реальным Judge0 (RUNNER=real) — те же проверки
//                 на настоящем выполнении Python.
import { chromium } from "playwright-core";
import { mkdirSync } from "node:fs";

const BASE = process.env.BASE ?? "http://localhost:13000";
const PW = process.env.DEMO_PASSWORD;
const OUT = process.env.OUT ?? "./e2e-out";
const RUNNER = process.env.RUNNER ?? "stub";
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

const browser = await chromium.launch({
  executablePath: process.env.CHROME ?? "/usr/bin/google-chrome",
  args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream"],
});

async function login(email, viewport = { width: 1440, height: 900 }, extra = {}) {
  const ctx = await browser.newContext({ viewport, permissions: ["camera", "microphone"], ignoreHTTPSErrors: true, ...extra });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.log(`   [${email.split("@")[0]}] pageerror:`, e.message));
  if (process.env.DEBUG_WS)
    page.on("websocket", (ws) => {
      ws.on("framesent", (f) => String(f.payload).includes("code") && console.log(`   [${email.split("@")[0]} ws→]`, String(f.payload).slice(0, 110)));
      ws.on("framereceived", (f) => String(f.payload).includes('"code.') && console.log(`   [${email.split("@")[0]} ws←]`, String(f.payload).slice(0, 110)));
    });
  const monacoRequests = [];
  page.on("request", (r) => {
    if (r.url().includes("/monaco/")) monacoRequests.push(r.url());
  });
  await page.goto(`${BASE}/login`);
  await page.fill("#email", email);
  await page.fill("#password", PW);
  await page.click("button:has-text('Войти')");
  await page.waitForURL("**/dashboard");
  return { ctx, page, monacoRequests };
}

async function enterRoom(page, lessonId) {
  await page.goto(`${BASE}/lessons/${lessonId}/room`);
  const join = page.getByRole("button", { name: "Войти в класс" });
  await Promise.race([join.waitFor({ timeout: 20000 }).then(() => join.click()), page.waitForSelector("text=Чат занятия", { state: "attached", timeout: 20000 })]);
  await page.waitForSelector("text=Чат занятия", { state: "attached", timeout: 20000 });
}

async function openCode(page) {
  await page.getByTestId("tab-code").click();
  await page.waitForSelector(".monaco-editor .view-lines", { timeout: 30000 });
  await page.waitForSelector("[data-testid='code-sync']:has-text('синхронизировано')", { timeout: 15000 });
}

const code = (page) => page.evaluate(() => window.monaco?.editor.getModels()[0]?.getValue() ?? null);
const stdin = (page) => page.locator("#code-stdin").inputValue();

/** Набор текста в редакторе (без автозакрытия скобок — чтобы текст был предсказуем). */
async function typeCode(page, text, { replace = false, at = "end" } = {}) {
  await page.evaluate(() => window.monaco.editor.getEditors()[0].updateOptions({ autoClosingBrackets: "never", autoClosingQuotes: "never", autoIndent: "none" }));
  await page.locator("[data-testid='code-editor'] .view-lines").click();
  if (replace) await page.keyboard.press("ControlOrMeta+A");
  else await page.keyboard.press(at === "start" ? "ControlOrMeta+Home" : "ControlOrMeta+End");
  await page.keyboard.type(text, { delay: 8 });
}

async function runAndWait(page, expected) {
  await page.getByTestId("code-run").click();
  const sel = `[data-testid='run-status']:has-text('${expected}')`;
  await page.waitForSelector(sel, { timeout: 30000 });
}

console.log(`Редактор кода (исполнитель: ${RUNNER})`);
const teacher = await login("teacher@demo.ortalyq.local");
const student = await login("student@demo.ortalyq.local");
const lessons = await (await student.ctx.request.get(`${BASE}/api/lessons?scope=upcoming`)).json();
const lesson = lessons.lessons.find((l) => l.alwaysOpen) ?? lessons.lessons[0];
if (!lesson) throw new Error("Демо-занятие не найдено — выполните seed");
console.log("  занятие:", lesson.id);

await step("оба вошли в класс; Monaco не загружается до открытия вкладки «Код»", async () => {
  await Promise.all([enterRoom(teacher.page, lesson.id), enterRoom(student.page, lesson.id)]);
  await sleep(1000);
  if (teacher.monacoRequests.length) throw new Error(`загружено до открытия: ${teacher.monacoRequests[0]}`);
});

await step("вкладка «Код» открывается у обоих, документ синхронизирован", async () => {
  await Promise.all([openCode(teacher.page), openCode(student.page)]);
  if (!teacher.monacoRequests.some((u) => u.includes("/monaco/vs/loader.js"))) throw new Error("Monaco не загружен со своего домена");
});

await step("код преподавателя виден ученику", async () => {
  await typeCode(teacher.page, 'name = input()\nprint("Привет,", name)', { replace: true });
  const want = 'name = input()\nprint("Привет,", name)';
  await until(async () => (await code(teacher.page)) === want, "текст у преподавателя");
  await until(async () => (await code(student.page)) === want, "текст у ученика");
});

await step("одновременная правка двух участников — текст обоих сохраняется, копии совпадают", async () => {
  await Promise.all([
    typeCode(teacher.page, "\n# строка преподавателя", { at: "end" }),
    typeCode(student.page, "# строка ученика\n", { at: "start" }),
  ]);
  const both = async () => {
    const [a, b] = await Promise.all([code(teacher.page), code(student.page)]);
    return a === b && a.includes("# строка преподавателя") && a.includes("# строка ученика") && a.includes('print("Привет,", name)') && a;
  };
  await until(both, "сходимость копий");
});

await step("одновременный ввод в одной позиции — без потерь", async () => {
  await Promise.all([typeCode(teacher.page, "\nAAAA", { at: "end" }), typeCode(student.page, "\nBBBB", { at: "end" })]);
  await until(async () => {
    const [a, b] = await Promise.all([code(teacher.page), code(student.page)]);
    return a === b && a.includes("AAAA") && a.includes("BBBB");
  }, "обе вставки у обоих");
});

await step("Ctrl+Z отменяет только свою правку", async () => {
  await sleep(600); // правки, сделанные быстрее captureTimeout (400 мс), отменяются одной группой
  await typeCode(student.page, "\nZZZ", { at: "end" });
  await until(async () => (await code(teacher.page)).includes("ZZZ"), "ZZZ у преподавателя");
  await student.page.keyboard.press("ControlOrMeta+Z");
  await until(async () => {
    const [a, b] = await Promise.all([code(teacher.page), code(student.page)]);
    const nativeRedo = await student.page.evaluate(() => window.monaco.editor.getModels()[0].canRedo());
    return (a === b && !a.includes("ZZZ") && a.includes("AAAA") && a.includes("BBBB")) || `nativeRedo=${nativeRedo} ${a} | ${b}`.replace(/\n/g, "⏎");
  }, "отмена у обоих без потери чужого текста").then((r) => {
    if (typeof r === "string") throw new Error(r);
  });
});

await step("входные данные общие", async () => {
  await student.page.locator("#code-stdin").fill("Айгерим\nБекзат");
  await until(async () => (await stdin(teacher.page)) === "Айгерим\nБекзат", "stdin у преподавателя");
});

await step("после перезагрузки страницы код и входные данные восстанавливаются", async () => {
  const before = await code(student.page);
  await sleep(2000); // сохранение в БД каждые 1,5 с
  await teacher.page.reload();
  await enterRoom(teacher.page, lesson.id);
  await openCode(teacher.page);
  await until(async () => (await code(teacher.page)) === before, "код после перезагрузки");
  await until(async () => (await stdin(teacher.page)) === "Айгерим\nБекзат", "stdin после перезагрузки");
});

if (RUNNER === "none") {
  await step("исполнитель не настроен: сообщение и неактивная кнопка, редактирование работает", async () => {
    await teacher.page.waitForSelector("text=Сервис выполнения не настроен", { timeout: 10000 });
    if (!(await teacher.page.getByTestId("code-run").isDisabled())) throw new Error("кнопка «Запустить» активна");
    const r = await teacher.ctx.request.post(`${BASE}/api/lessons/${lesson.id}/code/runs`, { data: { language: "python", source: "print(1)" }, headers: { Origin: BASE } });
    const j = await r.json();
    if (r.status() !== 503 || j.error !== "Сервис выполнения не настроен") throw new Error(`${r.status()} ${JSON.stringify(j)}`);
  });
} else {
  await step("запуск Python с stdin: результат и автор запуска у обоих", async () => {
    await typeCode(student.page, 'name = input()\nprint("Привет,", name)', { replace: true });
    await until(async () => (await code(teacher.page)) === 'name = input()\nprint("Привет,", name)', "код у преподавателя");
    await student.page.getByTestId("code-run").click();
    await teacher.page.waitForSelector("[data-testid='run-status']:has-text('Выполняется')", { timeout: 10000 });
    for (const u of [student, teacher]) {
      await u.page.waitForSelector("[data-testid='run-status']:has-text('Завершено')", { timeout: 30000 });
      const out = await u.page.getByTestId("run-stdout").innerText();
      if (!out.includes("Привет, Айгерим")) throw new Error(`stdout: ${out}`);
      const author = await u.page.getByTestId("run-author").innerText();
      if (!author.includes(lesson.studentName)) throw new Error(`автор: ${author}`);
    }
  });

  await step("результат последнего запуска виден после перезагрузки", async () => {
    await teacher.page.reload();
    await enterRoom(teacher.page, lesson.id);
    await openCode(teacher.page);
    await teacher.page.waitForSelector("[data-testid='run-status']:has-text('Завершено')", { timeout: 10000 });
  });

  await step("синтаксическая ошибка → «Ошибка выполнения», stderr с SyntaxError", async () => {
    await typeCode(teacher.page, "print(", { replace: true });
    await runAndWait(teacher.page, "Ошибка выполнения");
    const err = await teacher.page.getByTestId("run-stderr").innerText();
    if (!err.includes("SyntaxError")) throw new Error(err);
    await student.page.waitForSelector("[data-testid='run-status']:has-text('Ошибка выполнения')", { timeout: 10000 });
  });

  await step("бесконечный цикл → «Превышен лимит времени»", async () => {
    await typeCode(teacher.page, "while True:\n    pass", { replace: true });
    await runAndWait(teacher.page, "Превышен лимит времени");
  });

  await step("большой вывод → «Превышен лимит вывода»", async () => {
    await typeCode(teacher.page, 'print("x" * 10000000)', { replace: true });
    await runAndWait(teacher.page, "Превышен лимит вывода");
  });
  await teacher.page.screenshot({ path: `${OUT}/code-teacher.png` });
}

await step("посторонний ученик не читает и не запускает код этого урока", async () => {
  const o = await login("student2@demo.ortalyq.local");
  const g = await o.ctx.request.get(`${BASE}/api/lessons/${lesson.id}/code`);
  const p = await o.ctx.request.post(`${BASE}/api/lessons/${lesson.id}/code/runs`, { data: { language: "python", source: "print(1)" }, headers: { Origin: BASE } });
  if (g.status() !== 404 || p.status() !== 404) throw new Error(`GET ${g.status()}, POST ${p.status()}`);
  await o.page.goto(`${BASE}/lessons/${lesson.id}/room`);
  await o.page.waitForSelector("text=Не удалось открыть класс", { timeout: 15000 });
  await o.ctx.close();
});

await step("доска и чат работают после редактора кода", async () => {
  await teacher.page.getByRole("button", { name: "Доска", exact: true }).click();
  await teacher.page.locator("canvas.excalidraw__canvas.interactive").waitFor({ timeout: 15000 });
  const box = await teacher.page.locator("canvas.excalidraw__canvas.interactive").boundingBox();
  const count = async () => (await (await student.ctx.request.get(`${BASE}/api/lessons/${lesson.id}/board`)).json()).elements.filter((e) => !e.isDeleted).length;
  const before = await count();
  // Первый жест сразу после показа доски иногда не доходит до Excalidraw (как и в lesson-flow.mjs) — повторяем.
  for (let i = 0; i < 3 && (await count()) <= before; i++) {
    await teacher.page.getByRole("toolbar", { name: "Инструменты доски" }).getByTitle(/^Прямоугольник/).click();
    await teacher.page.mouse.move(box.x + 200 + i * 40, box.y + 150);
    await teacher.page.mouse.down();
    await teacher.page.mouse.move(box.x + 380 + i * 40, box.y + 260, { steps: 6 });
    await teacher.page.mouse.up();
    await sleep(2500);
  }
  if ((await count()) <= before) throw new Error("фигура не дошла до сервера");
  const msg = `проверка чата ${Date.now()}`;
  await student.page.locator("aside input, aside textarea").last().fill(msg);
  await student.page.keyboard.press("Enter");
  await teacher.page.waitForSelector(`text=${msg}`, { timeout: 10000 });
  // Код не потерялся при переключении вкладок
  await teacher.page.getByTestId("tab-code").click();
  await until(async () => (await code(teacher.page)) === (await code(student.page)), "код после переключения");
});

await step("телефон: редактор, ввод и «Запустить» доступны без горизонтальной прокрутки", async () => {
  const m = await login("student@demo.ortalyq.local", { width: 390, height: 844 }, { isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await enterRoom(m.page, lesson.id);
  await openCode(m.page);
  const edBox = await m.page.getByTestId("code-editor").boundingBox();
  if (!edBox || edBox.height < 200 || edBox.width < 300) throw new Error(`редактор ${JSON.stringify(edBox)}`);
  const run = await m.page.getByTestId("code-run").boundingBox();
  if (!run || run.x + run.width > 390) throw new Error("кнопка «Запустить» за пределами экрана");
  await m.page.locator("#code-stdin").scrollIntoViewIfNeeded();
  if (!(await m.page.locator("#code-stdin").isVisible())) throw new Error("поле ввода не видно");
  const overflow = await m.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  if (overflow > 1) throw new Error(`горизонтальная прокрутка ${overflow}px`);
  await m.page.screenshot({ path: `${OUT}/code-mobile.png` });
  await m.ctx.close();
});

await student.page.screenshot({ path: `${OUT}/code-student.png` });
await browser.close();
console.log(failed ? `\nОшибок: ${failed}` : "\nВсе проверки пройдены");
process.exit(failed ? 1 : 0);
