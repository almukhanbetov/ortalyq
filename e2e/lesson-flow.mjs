// Сквозная проверка занятия в двух браузерах (преподаватель и ученик) с фейковыми камерой/микрофоном.
// Требует: запущенные инфраструктуру, бэкенд, фронтенд и свежий seed.
//   BASE=http://localhost:13000 DEMO_PASSWORD=... OUT=/tmp/e2e node lesson-flow.mjs
// Переменные: CHROME — путь к Chrome/Chromium (по умолчанию /usr/bin/google-chrome),
//             RECORD=0 — пропустить проверку записи.
import { chromium } from "playwright-core";
import { mkdirSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";

const BASE = process.env.BASE ?? "http://localhost:13000";
const PW = process.env.DEMO_PASSWORD;
const OUT = process.env.OUT ?? "./e2e-out";
const CHROME = process.env.CHROME ?? "/usr/bin/google-chrome";
const RECORD = process.env.RECORD !== "0";
if (!PW) throw new Error("DEMO_PASSWORD не задан");
mkdirSync(OUT, { recursive: true });

const results = [];
const ok = (name) => (results.push([true, name]), console.log("  ✔", name));
const bad = (name, e) => (results.push([false, name]), console.log("  ✘", name, "—", e?.message ?? e));
async function step(name, fn) {
  try {
    await fn();
    ok(name);
  } catch (e) {
    bad(name, e);
  }
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browser = await chromium.launch({
  executablePath: CHROME,
  headless: true,
  args: [
    "--use-fake-ui-for-media-stream",
    "--use-fake-device-for-media-stream",
    "--autoplay-policy=no-user-gesture-required",
    "--auto-accept-this-tab-capture",
    "--auto-select-desktop-capture-source=ORTALYQ",
  ],
});

async function userContext(email) {
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 }, permissions: ["camera", "microphone"], ignoreHTTPSErrors: BASE.startsWith("https") });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.log(`   [${email.split("@")[0]} pageerror]`, e.message));
  if (process.env.DEBUG_WS) page.on("websocket", (ws) => ws.on("framesent", (f) => String(f.payload).includes("board") && console.log(`   [${email.split("@")[0]} ws→]`, String(f.payload).slice(0, 120))));
  await page.goto(`${BASE}/login`);
  await page.fill("#email", email);
  await page.fill("#password", PW);
  await page.click("button:has-text('Войти')");
  await page.waitForURL("**/dashboard", { timeout: 15000 });
  return { ctx, page };
}

async function enterRoom(page, lessonId) {
  await page.goto(`${BASE}/lessons/${lessonId}/room`);
  await page.getByRole("button", { name: "Войти в класс" }).click({ timeout: 20000 });
  await page.waitForSelector("text=Чат занятия", { timeout: 20000 });
}

const remoteVideoPlaying = (page) =>
  page.waitForFunction(
    () => [...document.querySelectorAll("video")].some((v) => !v.className.includes("-scale-x-100") && v.videoWidth > 0 && !v.paused),
    null,
    { timeout: 30000 },
  );

const remoteAudioLive = (page) =>
  page.waitForFunction(
    () =>
      [...document.querySelectorAll("audio")].some((a) => {
        const s = a.srcObject;
        return s && s.getAudioTracks().some((t) => t.readyState === "live" && !t.muted);
      }),
    null,
    { timeout: 30000 },
  );

async function drawRect(page, x, y, w, h) {
  const canvas = page.locator("canvas.excalidraw__canvas.interactive");
  await canvas.waitFor({ timeout: 20000 });
  const box = await canvas.boundingBox();
  const tool = page.locator("[data-testid='toolbar-rectangle']");
  for (let i = 0; i < 5 && !(await tool.isChecked()); i++) {
    await page.locator("label:has([data-testid='toolbar-rectangle'])").click();
    await sleep(200);
  }
  if (!(await tool.isChecked())) throw new Error("инструмент «прямоугольник» не выбран");
  await page.mouse.move(box.x + x, box.y + y);
  await page.mouse.down();
  await page.mouse.move(box.x + x + w / 2, box.y + y + h / 2, { steps: 5 });
  await page.mouse.move(box.x + x + w, box.y + y + h, { steps: 5 });
  await page.mouse.up();
  await page.keyboard.press("Escape");
}

async function boardCount(ctx, lessonId) {
  const r = await ctx.request.get(`${BASE}/api/lessons/${lessonId}/board`);
  const j = await r.json();
  return j.elements.filter((e) => !e.isDeleted).length;
}

console.log("Сквозная проверка занятия");
const teacher = await userContext("teacher@demo.ortalyq.local");
const student = await userContext("student@demo.ortalyq.local");

const lessons = await (await student.ctx.request.get(`${BASE}/api/lessons?scope=upcoming`)).json();
const lesson = lessons.lessons.find((l) => l.note.startsWith("Демо-класс"));
if (!lesson) throw new Error("Демо-занятие не найдено — выполните seed");
console.log("  занятие:", lesson.id);

await step("преподаватель и ученик вошли в класс", async () => {
  await Promise.all([enterRoom(teacher.page, lesson.id), enterRoom(student.page, lesson.id)]);
});

await step("видео передаётся в обе стороны", async () => {
  await Promise.all([remoteVideoPlaying(teacher.page), remoteVideoPlaying(student.page)]);
});

await step("звук передаётся в обе стороны (удалённая аудиодорожка активна)", async () => {
  await Promise.all([remoteAudioLive(teacher.page), remoteAudioLive(student.page)]);
});

await step("выключение и включение микрофона", async () => {
  await teacher.page.getByRole("button", { name: "Выключить микрофон" }).click();
  await teacher.page.getByRole("button", { name: "Включить микрофон" }).waitFor({ timeout: 5000 });
  await student.page.locator("[aria-label='микрофон выключен']:visible").first().waitFor({ timeout: 10000 });
  await teacher.page.getByRole("button", { name: "Включить микрофон" }).click();
  await teacher.page.getByRole("button", { name: "Выключить микрофон" }).waitFor({ timeout: 5000 });
});

await step("доска синхронизирована у обоих участников", async () => {
  for (const u of [teacher, student]) await u.page.waitForSelector("text=Синхронизация доски…", { state: "detached", timeout: 15000 });
});

await step("доска: рисунок преподавателя сохраняется на сервере", async () => {
  const before = await boardCount(teacher.ctx, lesson.id);
  await drawRect(teacher.page, 320, 180, 260, 160);
  await sleep(1500);
  await teacher.page.screenshot({ path: `${OUT}/teacher-first-draw.png` });
  const after = await boardCount(student.ctx, lesson.id);
  if (after <= before) throw new Error(`элементов было ${before}, стало ${after}`);
});

await step("доска: рисунок ученика виден преподавателю", async () => {
  const before = await boardCount(teacher.ctx, lesson.id);
  await drawRect(student.page, 520, 250, 180, 120);
  await sleep(1500);
  const after = await boardCount(teacher.ctx, lesson.id);
  if (after <= before) throw new Error("элемент ученика не дошёл до сервера");
  await teacher.page.screenshot({ path: `${OUT}/teacher-board.png` });
  await student.page.screenshot({ path: `${OUT}/student-board.png` });
});

await step("чат: сообщение доставлено и сохранено", async () => {
  const text = `Проверка чата ${Date.now()}`;
  await student.page.fill("input[aria-label='Сообщение']", text);
  await student.page.keyboard.press("Enter");
  await teacher.page.waitForSelector(`text=${text}`, { timeout: 10000 });
  const chat = await (await teacher.ctx.request.get(`${BASE}/api/lessons/${lesson.id}/chat`)).json();
  if (!chat.messages.some((m) => m.body === text)) throw new Error("сообщение не сохранено");
});

await step("после переподключения доска восстанавливается", async () => {
  await student.page.reload();
  await student.page.getByRole("button", { name: "Войти в класс" }).click({ timeout: 20000 });
  await student.page.waitForSelector("text=Чат занятия", { timeout: 20000 });
  await student.page.waitForSelector("text=Синхронизация доски…", { state: "detached", timeout: 15000 });
  await sleep(1500);
  await student.page.screenshot({ path: `${OUT}/student-after-reconnect.png` });
  await remoteVideoPlaying(student.page);
});

let recordingId = null;
if (RECORD) {
  await step("запись: согласие обоих и индикатор «Идёт запись»", async () => {
    await teacher.page.getByRole("button", { name: "⏺ Начать запись" }).click();
    await teacher.page.getByRole("button", { name: "Согласен, запросить запись" }).click();
    await student.page.getByRole("button", { name: "Согласен на запись" }).click({ timeout: 10000 });
    await Promise.all([
      teacher.page.waitForSelector("text=Идёт запись", { timeout: 90000 }),
      student.page.waitForSelector("text=Идёт запись", { timeout: 90000 }),
    ]);
    await teacher.page.screenshot({ path: `${OUT}/teacher-recording.png` });
  });

  await step("запись: идёт ~20 секунд с рисованием на доске", async () => {
    await drawRect(teacher.page, 300, 380, 200, 100);
    await sleep(5000);
    await drawRect(student.page, 620, 420, 120, 120);
    await sleep(3000);
  });

  await step("демонстрация экрана видна ученику и попадает в запись", async () => {
    await teacher.page.getByRole("button", { name: "Показать экран" }).click();
    await student.page.waitForSelector("text=Экран: Айгерим", { timeout: 15000 });
    await student.page.waitForFunction(() => [...document.querySelectorAll("video.object-contain")].some((v) => v.videoWidth > 0), null, { timeout: 15000 });
    await student.page.screenshot({ path: `${OUT}/student-screenshare.png` });
    await sleep(7000);
    await teacher.page.getByRole("button", { name: "Остановить демонстрацию" }).click();
    await sleep(3000);
  });

  await step("запись остановлена преподавателем", async () => {
    await teacher.page.getByRole("button", { name: /Остановить запись/ }).click();
  });

  await step("запись: файл готов и сохранён в S3", async () => {
    const deadline = Date.now() + 120000;
    for (;;) {
      const d = await (await teacher.ctx.request.get(`${BASE}/api/lessons/${lesson.id}`)).json();
      const r = d.recordings.at(-1);
      if (r?.status === "ready") {
        recordingId = r.id;
        console.log(`   размер ${r.sizeBytes} байт, длительность ${r.durationMs} мс`);
        break;
      }
      if (r?.status === "failed") throw new Error(`ошибка записи: ${r.error}`);
      if (Date.now() > deadline) throw new Error(`статус ${r?.status}`);
      await sleep(3000);
    }
  });

  await step("запись: ученик скачивает файл по временной ссылке, файл содержит видео и звук", async () => {
    const link = await (await student.ctx.request.get(`${BASE}/api/lessons/${lesson.id}/recordings/${recordingId}/link`)).json();
    const res = await student.ctx.request.get(`${BASE}${link.url}`);
    if (res.status() !== 200) throw new Error(`HTTP ${res.status()}`);
    const buf = await res.body();
    writeFileSync(`${OUT}/recording.mp4`, buf);
    const probe = execFileSync("docker", ["run", "--rm", "-v", `${OUT}:/w`, "--entrypoint", "/ffprobe", "mwader/static-ffmpeg:7.1",
      "-v", "error", "-show_entries", "stream=codec_type,codec_name,width,height:format=duration", "-of", "json", "/w/recording.mp4"]).toString();
    writeFileSync(`${OUT}/recording-probe.json`, probe);
    const p = JSON.parse(probe);
    const types = p.streams.map((s) => s.codec_type);
    if (!types.includes("video") || !types.includes("audio")) throw new Error(`потоки: ${types}`);
    console.log(`   потоки: ${p.streams.map((s) => `${s.codec_type}/${s.codec_name}`).join(", ")}; длительность ${p.format.duration} с`);
    execFileSync("docker", ["run", "--rm", "-v", `${OUT}:/w`, "--entrypoint", "/ffmpeg", "mwader/static-ffmpeg:7.1",
      "-v", "error", "-y", "-ss", "6", "-i", "/w/recording.mp4", "-frames:v", "1", "/w/recording-frame.png"]);
    execFileSync("docker", ["run", "--rm", "-v", `${OUT}:/w`, "--entrypoint", "/ffmpeg", "mwader/static-ffmpeg:7.1",
      "-v", "error", "-y", "-ss", "14", "-i", "/w/recording.mp4", "-frames:v", "1", "/w/recording-frame-screen.png"]);
  });

  await step("запись: посторонний не получает ссылку на запись", async () => {
    const outsider = await userContext("student2@demo.ortalyq.local");
    const r = await outsider.ctx.request.get(`${BASE}/api/lessons/${lesson.id}/recordings/${recordingId}/link`);
    if (r.status() !== 404) throw new Error(`HTTP ${r.status()}`);
    await outsider.ctx.close();
  });
}

await step("посторонний ученик не может войти в класс", async () => {
  const outsider = await userContext("student2@demo.ortalyq.local");
  await outsider.page.goto(`${BASE}/lessons/${lesson.id}/room`);
  await outsider.page.waitForSelector("text=Не удалось открыть класс", { timeout: 15000 });
  const r = await outsider.ctx.request.post(`${BASE}/api/lessons/${lesson.id}/join`, { headers: { Origin: BASE } });
  if (r.status() !== 404) throw new Error(`join вернул ${r.status()}`);
  await outsider.ctx.close();
});

await step("преподаватель завершает занятие, у ученика появляется уведомление", async () => {
  await teacher.page.getByRole("button", { name: "Завершить занятие" }).click();
  await teacher.page.getByRole("button", { name: "Завершить", exact: true }).click();
  await student.page.waitForSelector("text=Занятие завершено", { timeout: 15000 });
  await teacher.page.waitForURL(`**/lessons/${lesson.id}`, { timeout: 15000 });
});

await step("страница завершённого занятия: доска, чат и запись доступны", async () => {
  await student.page.goto(`${BASE}/lessons/${lesson.id}`);
  await student.page.waitForSelector("text=Доска занятия");
  await student.page.waitForSelector("text=Завершено");
  if (RECORD) await student.page.waitForSelector("text=Готова", { timeout: 15000 });
  await student.page.getByRole("button", { name: "Открыть" }).click();
  await sleep(2500);
  await student.page.screenshot({ path: `${OUT}/lesson-page.png`, fullPage: true });
});

await browser.close();
const failed = results.filter((r) => !r[0]).length;
console.log(`\nИтого: успешно ${results.length - failed}, ошибок ${failed}`);
process.exit(failed ? 1 : 0);
