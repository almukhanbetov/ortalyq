// «Пробный урок»: вход без регистрации, двое по приглашению в разных браузерных сессиях, отказ третьему,
// синхронизация доски, текста и кода, изоляция комнат, завершение, мобильный интерфейс и понятные ошибки.
//   BASE=http://localhost:13000 OUT=./e2e-out node demo-lesson.mjs
// Переменные:
//   NOVIDEO=1     — бэкенд без LiveKit (проверяется вход без видео с причиной);
//   JUDGE0_STATS  — адрес /stats заглушки fake-judge0.mjs (проверяется, что запуск дошёл до исполнителя);
//   OPENAI_STATS  — адрес /stats fake-openai.mjs (проверяется, что демо не отправляет запросы к ИИ);
//   PSQL          — команда psql к базе бэкенда (снимок уроков/расписания/цен до и после);
//   LESSON_ID     — ID настоящего урока (демо-сессия не должна получить к нему доступ);
//   EXPIRY=1      — только проверка окончания по времени (бэкенд с DEMO_MINUTES=1, ждёт ~70 с).
import { chromium, devices } from "playwright-core";
import { execSync } from "node:child_process";
import { mkdirSync } from "node:fs";

const BASE = process.env.BASE ?? "http://localhost:13000";
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
    console.log("  ✘", name, "—", (e?.message ?? String(e)).split("\n")[0]);
  }
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, what, timeout = 15000) {
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
  throw new Error(`не дождались: ${what} (последнее: ${String(last?.message ?? last).slice(0, 160)})`);
}
const stats = async (url) => (url ? (await (await fetch(url)).json()) : null);
const snapshot = () =>
  process.env.PSQL
    ? execSync(
        `${process.env.PSQL} -tAc "SELECT concat_ws('|', (SELECT count(*) FROM users), (SELECT md5(coalesce(string_agg(id::text||status||starts_at::text||ends_at::text||price::text, ',' ORDER BY id),'')) FROM lessons), (SELECT md5(coalesce(string_agg(id::text||starts_at::text||ends_at::text, ',' ORDER BY id),'')) FROM availability), (SELECT md5(coalesce(string_agg(user_id::text||price::text, ',' ORDER BY user_id),'')) FROM teacher_profiles), (SELECT count(*) FROM boards), (SELECT count(*) FROM chat_messages), (SELECT count(*) FROM code_runs), (SELECT count(*) FROM recordings))"`,
      )
        .toString()
        .trim()
    : "";

const browser = await chromium.launch({
  executablePath: process.env.CHROME ?? "/usr/bin/google-chrome",
  args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream", "--autoplay-policy=no-user-gesture-required"],
});

/** Новая браузерная сессия (свои cookie). frames — сообщения WebSocket класса. */
async function session(label, opts = {}, { routeWs = false } = {}) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, permissions: ["camera", "microphone", "clipboard-read", "clipboard-write"], ...opts });
  const page = await ctx.newPage();
  const frames = { sent: [], received: [] };
  const net = { blocked: false, routes: [] };
  // Управляемый обрыв WebSocket класса (имитация недоступного сервера): setOffline не закрывает открытые сокеты.
  if (routeWs)
    await page.routeWebSocket(/\/api\/demo\/rooms\/.*\/ws/, (ws) => {
      if (net.blocked) return ws.close({ code: 1011, reason: "test outage" });
      const server = ws.connectToServer();
      net.routes.push({ ws, server });
    });
  page.on("pageerror", (e) => console.log(`   [${label}] pageerror:`, e.message));
  page.on("websocket", (ws) => {
    if (!ws.url().includes("/api/demo/")) return;
    ws.on("framesent", (f) => frames.sent.push(String(f.payload)));
    ws.on("framereceived", (f) => frames.received.push(String(f.payload)));
  });
  return { ctx, page, frames, label, net };
}

async function startDemo(s, mode) {
  await s.page.goto(`${BASE}/demo`);
  await s.page.getByTestId(mode === "solo" ? "demo-solo" : "demo-pair").click();
  await s.page.waitForURL("**/demo/room/**");
  return s.page.url().split("/demo/room/")[1];
}

async function enter(s, { skip = false } = {}) {
  await s.page.getByTestId(skip ? "prejoin-skip" : "prejoin-join").click({ timeout: 20000 });
  await s.page.getByTestId("demo-end").or(s.page.getByRole("link", { name: "Выйти" })).first().waitFor({ timeout: 20000 });
}

async function typeInMonaco(page, text) {
  await page.locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Control+End");
  await page.keyboard.type(text);
}

const monacoText = (page) => page.locator(".monaco-editor .view-lines").innerText().then((t) => t.replace(/ /g, " "));

if (process.env.EXPIRY === "1") {
  console.log("Пробный урок: окончание по серверному времени (DEMO_MINUTES=1)");
  const a = await session("expiry");
  const id = await startDemo(a, "solo");
  await enter(a, { skip: true });
  await step("предупреждение перед окончанием видно в классе", () => a.page.getByTestId("demo-warning").waitFor({ timeout: 10000 }));
  await step("через минуту сервер закрывает доступ — экран «Демонстрация завершена»", () => a.page.getByTestId("demo-ended").waitFor({ timeout: 80000 }));
  await step("после окончания API демонстрации закрыто", async () => {
    const r = await a.page.evaluate(async (id) => {
      const st = await (await fetch(`/api/demo/rooms/${id}`)).json();
      const join = await fetch(`/api/demo/rooms/${id}/code/runs`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ language: "python", source: "print(1)" }) });
      return { status: st.status, reason: st.endReason, run: join.status };
    }, id);
    if (r.status !== "ended" || r.reason !== "expired" || r.run !== 409) throw new Error(JSON.stringify(r));
  });
  await step("WebSocket после окончания отклоняется", async () => {
    const ok = await a.page.evaluate(
      (id) =>
        new Promise((res) => {
          const ws = new WebSocket(`${location.origin.replace("http", "ws")}/api/demo/rooms/${id}/ws`);
          ws.onopen = () => res(false);
          ws.onerror = () => res(true);
        }),
      id,
    );
    if (!ok) throw new Error("соединение открылось");
  });
  await browser.close();
  console.log(failed ? `\nПровалено шагов: ${failed}` : "\nВсе шаги пройдены");
  process.exit(failed ? 1 : 0);
}

console.log(`Пробный урок (${NOVIDEO ? "без LiveKit" : "с видеосвязью"})`);
const before = snapshot();
const aiBefore = await stats(process.env.OPENAI_STATS);

// ---------- Главная и описание ----------
const a = await session("solo");
await step("на главной и в навигации есть «Попробовать урок»", async () => {
  await a.page.goto(BASE);
  await a.page.getByTestId("home-try-demo").waitFor();
  await a.page.getByTestId("header-try-demo").waitFor();
  const cur = await a.page.getByTestId("home-try-demo").evaluate((el) => getComputedStyle(el).cursor);
  if (cur !== "pointer") throw new Error(`cursor=${cur}`);
  await a.page.getByTestId("home-try-demo").click();
  await a.page.waitForURL("**/demo");
});
await step("страница объясняет демонстрацию и предлагает два режима", async () => {
  const intro = await a.page.getByTestId("demo-intro").innerText();
  if (!intro.includes("Демонстрация бесплатная и не создаёт платное бронирование")) throw new Error(intro);
  const notice = await a.page.getByTestId("demo-temp-notice").innerText();
  if (!notice.includes("Материалы временные и могут быть потеряны при перезапуске сервиса")) throw new Error(notice);
  await a.page.getByTestId("demo-solo").waitFor();
  await a.page.getByTestId("demo-pair").waitFor();
});

// ---------- Самостоятельно, без регистрации ----------
let soloId = "";
await step("самостоятельный вход без регистрации: отдельная комната и проверка устройств", async () => {
  await a.page.getByTestId("demo-solo").click();
  await a.page.waitForURL("**/demo/room/**");
  soloId = a.page.url().split("/demo/room/")[1];
  await a.page.getByTestId("prejoin-skip").waitFor();
  await a.page.getByText("Проверьте камеру и микрофон перед входом.").waitFor();
  const me = await a.page.evaluate(() => fetch("/api/auth/me").then((r) => r.json()));
  if (me.user !== null) throw new Error("посетитель не должен быть пользователем");
});
await step("«Продолжить без камеры и микрофона» открывает класс", async () => {
  await enter(a, { skip: true });
  await a.page.getByTestId("demo-hints").waitFor();
  const t = await a.page.getByTestId("demo-time-left").innerText();
  if (!/1[45]:\d\d/.test(t)) throw new Error(`таймер: ${t}`);
});
await step("запись отключена с понятной причиной", async () => {
  const btn = a.page.getByRole("button", { name: /Запись: Запись видео в пробном уроке отключена/ });
  if (!(await btn.isDisabled())) throw new Error("кнопка записи активна");
  const cur = await btn.evaluate((el) => getComputedStyle(el).cursor);
  if (cur !== "not-allowed") throw new Error(`cursor=${cur}`);
});
await step("доска: пример добавлен и отправлен на сервер", () =>
  until(() => a.frames.sent.some((f) => f.includes('"board.update"') && f.includes("Пробный урок: общая доска")), "пример доски"),
);
await step("код: пример в общем редакторе, запуск доходит до исполнителя и показан результат", async () => {
  const j0 = await stats(process.env.JUDGE0_STATS);
  await a.page.getByTestId("tab-code").click();
  await until(async () => (await monacoText(a.page)).includes("def fib"), "пример кода", 30000);
  await until(async () => (await a.page.locator("#code-stdin").inputValue()) === "10", "входные данные примера");
  const left = await a.page.getByTestId("code-runs-left").innerText();
  await a.page.getByTestId("code-run").click();
  await until(async () => (await a.page.getByTestId("run-stdout").innerText()).length > 0, "результат запуска", 20000);
  const j1 = await stats(process.env.JUDGE0_STATS);
  if (j0 && j1.submissions !== j0.submissions + 1) throw new Error("запуск не дошёл до исполнителя");
  await until(async () => (await a.page.getByTestId("code-runs-left").innerText()) !== left, "уменьшение остатка запусков");
});
await step("мастерская: учебный текст-пример и «Пример результата» без запроса к ИИ", async () => {
  await a.page.getByTestId("tab-workshop").click();
  await until(async () => (await a.page.getByTestId("workshop-text").inputValue()).includes("24 ученика"), "текст-пример");
  await a.page.getByTestId("demo-ai-sample").getByText("Пример результата").waitFor();
  const btn = a.page.locator('[data-testid^="workshop-action-"]').first();
  if (!(await btn.isDisabled())) throw new Error("кнопка ИИ активна");
  const title = await btn.getAttribute("title");
  if (!title?.includes("пробном уроке")) throw new Error(`причина: ${title}`);
});
await step("демо-сессия не открывает настоящие уроки и данные пользователей", async () => {
  const codes = await a.page.evaluate(
    async (lesson) => Promise.all(["/api/lessons", `/api/lessons/${lesson}`, `/api/lessons/${lesson}/board`, "/api/admin/users", "/api/my/homework"].map((p) => fetch(p).then((r) => r.status))),
    process.env.LESSON_ID ?? "00000000-0000-0000-0000-000000000000",
  );
  if (codes.some((c) => c !== 401)) throw new Error(codes.join(","));
});

// ---------- Вдвоём ----------
const host = await session("host", {}, { routeWs: true });
const guest = await session("guest");
let pairId = "";
let invite = "";
await step("«Попробовать вдвоём» создаёт новую комнату и ссылку-приглашение", async () => {
  pairId = await startDemo(host, "pair");
  if (pairId === soloId) throw new Error("одна и та же комната");
  invite = await host.page.getByTestId("demo-invite-url").inputValue();
  if (!invite.includes(`/demo/join/${pairId}#`)) throw new Error(invite);
});
await step("организатор входит (проверка устройств → класс)", () => enter(host, { skip: NOVIDEO }));
await step("второй участник входит по ссылке в другой браузерной сессии", async () => {
  await guest.page.goto(invite);
  await guest.page.getByTestId("demo-accept").click();
  await guest.page.waitForURL(`**/demo/room/${pairId}`);
  await enter(guest, { skip: NOVIDEO });
  const title = await guest.page.locator("header").first().innerText();
  if (!title.includes("вы — ученик")) throw new Error(title);
});
await step("оба видят друг друга в классе", async () => {
  await until(async () => (await host.page.locator("header").first().innerText()).includes("оба участника в классе"), "у организатора");
  await until(async () => (await guest.page.locator("header").first().innerText()).includes("оба участника в классе"), "у ученика");
});
if (!NOVIDEO)
  await step("видеосвязь: видео второго участника приходит (LiveKit, тестовая камера)", async () => {
    for (const s of [host, guest])
      await until(() => s.page.evaluate(() => [...document.querySelectorAll("video")].filter((v) => v.readyState >= 2 && v.videoWidth > 0).length >= 2), `видео у ${s.label}`, 30000);
  });
await step("доска синхронизируется: фигура организатора приходит ученику", async () => {
  await host.page.getByRole("toolbar", { name: "Инструменты доски" }).getByTitle("Прямоугольник (R)").click();
  const box = await host.page.locator(".excalidraw canvas").last().boundingBox();
  await host.page.mouse.move(box.x + 500, box.y + 450);
  await host.page.mouse.down();
  await host.page.mouse.move(box.x + 650, box.y + 560, { steps: 5 });
  await host.page.mouse.up();
  await until(() => guest.frames.received.some((f) => f.includes('"board.update"') && f.includes('"rectangle"') && !f.includes("Решите")), "board.update у ученика");
});
await step("текст мастерской синхронизируется в обе стороны", async () => {
  await host.page.getByTestId("tab-workshop").click();
  await guest.page.getByTestId("tab-workshop").click();
  await until(async () => (await guest.page.getByTestId("workshop-text").inputValue()).includes("24 ученика"), "пример у ученика");
  const ht = host.page.getByTestId("workshop-text");
  await ht.click();
  await host.page.keyboard.press("Control+End");
  await host.page.keyboard.type("\nКомментарий преподавателя");
  await until(async () => (await guest.page.getByTestId("workshop-text").inputValue()).includes("Комментарий преподавателя"), "правка преподавателя у ученика");
  const gt = guest.page.getByTestId("workshop-text");
  await gt.click();
  await guest.page.keyboard.press("Control+End");
  await guest.page.keyboard.type("\nОтвет ученика 16");
  await until(async () => (await ht.inputValue()).includes("Ответ ученика 16"), "правка ученика у преподавателя");
});
await step("код синхронизируется: правка ученика видна организатору", async () => {
  await guest.page.getByTestId("tab-code").click();
  await host.page.getByTestId("tab-code").click();
  await until(async () => (await monacoText(guest.page)).includes("def fib"), "пример кода у ученика", 30000);
  await typeInMonaco(guest.page, "\nprint('привет от ученика')");
  await until(async () => (await monacoText(host.page)).includes("привет от ученика"), "правка у организатора");
});
await step("чат: сообщение доходит до второго участника", async () => {
  await host.page.getByLabel("Сообщение").fill("Здравствуйте!");
  await host.page.getByLabel("Отправить").click();
  await until(async () => (await guest.page.locator("text=Здравствуйте!").count()) > 0, "сообщение у ученика");
});

// ---------- Отказ третьему и изоляция ----------
const third = await session("third");
await step("третьему участнику отказано с понятной причиной", async () => {
  await third.page.goto(invite);
  await third.page.getByTestId("demo-accept").click();
  const msg = await third.page.getByTestId("demo-join-error").innerText();
  if (!msg.includes("уже два участника")) throw new Error(msg);
  await third.page.getByRole("link", { name: "Начать свою демонстрацию" }).waitFor();
});
await step("разные демо-комнаты изолированы", async () => {
  await third.page.goto(`${BASE}/demo/room/${pairId}`);
  await third.page.getByTestId("demo-not-found").waitFor();
  const codes = await a.page.evaluate(
    (id) => Promise.all([fetch(`/api/demo/rooms/${id}`), fetch(`/api/demo/rooms/${id}/code`), fetch(`/api/demo/rooms/${id}/workshop`)].map((p) => p.then((r) => r.status))),
    pairId,
  );
  if (codes.some((c) => c !== 404)) throw new Error(`участник solo → pair: ${codes}`);
  const leaked = a.frames.received.some((f) => f.includes("Комментарий") || f.includes("Здравствуйте!") || f.includes("привет от ученика"));
  if (leaked) throw new Error("содержимое пары попало в другую комнату");
});
await step("ученик не может завершить демонстрацию для всех (сервер)", async () => {
  const code = await guest.page.evaluate((id) => fetch(`/api/demo/rooms/${id}/end`, { method: "POST" }).then((r) => r.status), pairId);
  if (code !== 403) throw new Error(String(code));
});

// ---------- Ошибки подключения ----------
await step("понятная ошибка при потере связи с сервером класса и восстановление", async () => {
  host.net.blocked = true;
  try {
    for (const r of host.net.routes.splice(0)) {
      await r.server.close().catch(() => {});
      await r.ws.close({ code: 1006 }).catch(() => {});
    }
    await host.page.getByText("Нет связи с сервером доски и чата — переподключаемся…").waitFor({ timeout: 15000 });
    await host.page.screenshot({ path: `${OUT}/demo-reconnecting.png` });
  } finally {
    host.net.blocked = false;
  }
  await until(async () => (await host.page.getByText("Нет связи с сервером доски и чата").count()) === 0, "восстановление связи", 20000);
  // После переподключения правки снова доходят
  await host.page.getByLabel("Сообщение").fill("Связь восстановлена");
  await host.page.getByLabel("Отправить").click();
  await until(async () => (await guest.page.locator("text=Связь восстановлена").count()) > 0, "сообщение после переподключения");
});
await step("понятные ошибки ссылок: несуществующая демонстрация, ссылка без кода, неверный код, нет сети", async () => {
  const p = third.page;
  await p.goto(`${BASE}/demo/room/11111111-1111-4111-8111-111111111111`);
  await p.getByTestId("demo-not-found").waitFor();
  await p.goto(`${BASE}/demo/join/${pairId}`);
  await p.getByText("В ссылке нет кода приглашения").waitFor();
  await p.goto(`${BASE}/demo/join/${pairId}#wrong`);
  await p.getByTestId("demo-accept").click();
  const msg = await p.getByTestId("demo-join-error").innerText();
  if (!msg.includes("недействительна")) throw new Error(msg);
  await p.goto(`${BASE}/demo`);
  await third.ctx.setOffline(true);
  try {
    await p.getByTestId("demo-solo").click();
    const err = await p.getByTestId("demo-error").innerText();
    if (!err.includes("Нет соединения")) throw new Error(err);
  } finally {
    await third.ctx.setOffline(false);
  }
});

// ---------- Завершение ----------
await step("«Завершить демонстрацию»: доступ закрыт у обоих, ресурсы освобождены", async () => {
  await host.page.getByTestId("demo-end").click();
  await host.page.getByRole("button", { name: "Завершить", exact: true }).click();
  await host.page.getByTestId("demo-ended").waitFor({ timeout: 10000 });
  await guest.page.getByTestId("demo-ended").waitFor({ timeout: 10000 });
  const st = await guest.page.evaluate((id) => fetch(`/api/demo/rooms/${id}`).then((r) => r.json()), pairId);
  if (st.status !== "ended") throw new Error(JSON.stringify(st));
});

// ---------- Телефон ----------
const phone = await session("phone", { ...devices["iPhone 13"], permissions: ["camera", "microphone"] });
await step("телефон: описание и класс без горизонтальной прокрутки, вкладки и управление доступны", async () => {
  const p = phone.page;
  await p.goto(BASE);
  await p.getByTestId("header-try-demo-mobile").click();
  await p.waitForURL("**/demo");
  const over = await p.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  if (over > 1) throw new Error(`горизонтальная прокрутка на /demo: ${over}px`);
  await p.screenshot({ path: `${OUT}/demo-phone-intro.png`, fullPage: true });
  await p.getByTestId("demo-solo").click();
  await p.waitForURL("**/demo/room/**");
  await p.getByTestId("prejoin-skip").click();
  await p.getByTestId("demo-end").waitFor({ timeout: 20000 });
  await p.getByRole("button", { name: "Доска / код / мастерская" }).waitFor();
  await p.getByRole("button", { name: /^Чат/ }).waitFor();
  await p.getByTestId("demo-time-left").waitFor();
  // Подсказки на телефоне свёрнуты, открываются кнопкой «Как пользоваться»
  if (await p.getByTestId("demo-hints").isVisible()) throw new Error("подсказки на телефоне должны быть свёрнуты");
  await p.getByTestId("demo-howto").click();
  await p.getByTestId("demo-hints").waitFor();
  await p.getByRole("button", { name: "Скрыть подсказки" }).click();
  await p.getByTestId("demo-howto").waitFor();
  const over2 = await p.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  if (over2 > 1) throw new Error(`горизонтальная прокрутка в классе: ${over2}px`);
  await p.screenshot({ path: `${OUT}/demo-phone-class.png` });
  await p.getByTestId("tab-workshop").click();
  await until(async () => (await p.getByTestId("workshop-text").inputValue()).includes("24 ученика"), "текст на телефоне");
  await p.screenshot({ path: `${OUT}/demo-phone-workshop.png` });
});
await a.page.screenshot({ path: `${OUT}/demo-solo-workshop.png` });

// ---------- Без влияния на уроки и ИИ ----------
await step("запросов к ИИ не было", async () => {
  const ai = await stats(process.env.OPENAI_STATS);
  if (aiBefore && ai.requests !== aiBefore.requests) throw new Error(`${aiBefore.requests} → ${ai.requests}`);
});
await step("уроки, расписание, цены, доски, чаты, записи и запуски уроков не изменились", async () => {
  const after = snapshot();
  if (before !== after) throw new Error(`${before} → ${after}`);
  if (!before) console.log("     (PSQL не задан — снимок БД не сравнивался)");
});

await browser.close();
console.log(failed ? `\nПровалено шагов: ${failed}` : "\nВсе шаги пройдены");
process.exit(failed ? 1 : 0);
