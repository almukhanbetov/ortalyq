// Удобство интерфейса: показ/скрытие пароля и курсор-«ладонь» на интерактивных элементах.
//   BASE=http://localhost:13000 DEMO_PASSWORD=... OUT=./out node ui-usability.mjs
// Данные не изменяет: только вход демо-аккаунтом и просмотр страниц (регистрация не отправляется).
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
function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}
const cursor = (loc) => loc.evaluate((el) => getComputedStyle(el).cursor);

const browser = await chromium.launch({ executablePath: process.env.CHROME ?? "/usr/bin/google-chrome" });
const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
const page = await ctx.newPage();
page.on("pageerror", (e) => console.log("   pageerror:", e.message));
let loginPosts = 0;
page.on("request", (r) => {
  if (r.method() === "POST" && r.url().includes("/api/auth/login")) loginPosts++;
});

console.log("Пароль: показ и скрытие");
await page.goto(`${BASE}/login`);
const pass = page.locator("#password");
const toggle = page.getByTestId("password-toggle");

await step("по умолчанию пароль скрыт; кнопка type=button с aria-label и aria-pressed", async () => {
  await pass.waitFor();
  assert((await pass.getAttribute("type")) === "password", "type=password");
  assert((await toggle.getAttribute("type")) === "button", "type=button");
  assert((await toggle.getAttribute("aria-label")) === "Показать пароль", "aria-label");
  assert((await toggle.getAttribute("aria-pressed")) === "false", "aria-pressed=false");
});

await step("атрибуты для менеджеров паролей и проверки формы сохранены", async () => {
  assert((await pass.getAttribute("autocomplete")) === "current-password", "autocomplete");
  assert((await pass.getAttribute("name")) === "password", "name");
  assert((await pass.getAttribute("required")) !== null, "required");
});

await step("щелчок показывает пароль без очистки поля, потери фокуса и отправки формы", async () => {
  await pass.click();
  await page.keyboard.type("секрет123");
  await pass.evaluate((el) => el.setSelectionRange(3, 3));
  await toggle.click();
  await page.waitForTimeout(300);
  assert((await pass.getAttribute("type")) === "text", "type=text");
  assert((await pass.inputValue()) === "секрет123", "значение сохранено");
  assert(await pass.evaluate((el) => document.activeElement === el), "фокус остался в поле");
  assert(await pass.evaluate((el) => el.selectionStart === 3), "позиция курсора сохранена");
  assert((await toggle.getAttribute("aria-label")) === "Скрыть пароль" && (await toggle.getAttribute("aria-pressed")) === "true", "состояние кнопки");
  await toggle.click();
  assert((await pass.getAttribute("type")) === "password", "снова скрыт");
  assert(loginPosts === 0 && page.url().includes("/login"), "форма не отправлена");
});

await step("клавиатура: Tab до кнопки, Пробел и Enter переключают и не отправляют форму", async () => {
  await pass.focus();
  await page.keyboard.press("Tab");
  assert(await toggle.evaluate((el) => document.activeElement === el), "фокус на кнопке");
  const outline = await toggle.evaluate((el) => getComputedStyle(el).outlineStyle);
  assert(outline !== "none", `видимый фокус (outline: ${outline})`);
  await page.keyboard.press("Space");
  assert((await pass.getAttribute("type")) === "text", "Пробел показал пароль");
  await page.keyboard.press("Enter");
  assert((await pass.getAttribute("type")) === "password", "Enter скрыл пароль");
  await page.waitForTimeout(500);
  assert(loginPosts === 0, "форма не отправлена");
});

await step("Enter в поле пароля по-прежнему отправляет форму входа", async () => {
  await page.fill("#email", "teacher@demo.ortalyq.local");
  await pass.fill(PW);
  await toggle.click(); // вход и с видимым паролем
  await pass.press("Enter");
  await page.waitForURL("**/dashboard", { timeout: 15000 });
  assert(loginPosts === 1, `один запрос входа (${loginPosts})`);
});

await step("регистрация: поле нового пароля с кнопкой, независимое состояние", async () => {
  const p2 = await ctx.browser().newContext().then((c) => c.newPage());
  await p2.goto(`${BASE}/register`);
  const f = p2.locator("#password");
  await f.waitFor();
  assert((await f.getAttribute("autocomplete")) === "new-password" && (await f.getAttribute("minlength")) === "8", "autocomplete/minlength");
  assert((await f.getAttribute("type")) === "password", "скрыт по умолчанию (состояние не перенесено с другой формы)");
  await p2.getByTestId("password-toggle").click();
  assert((await f.getAttribute("type")) === "text", "переключается");
  await p2.context().close();
});

console.log("Телефон");
await step("иконка глаза не перекрывает вводимый текст (390 px)", async () => {
  const m = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  const mp = await m.newPage();
  await mp.goto(`${BASE}/register`);
  const f = mp.locator("#password");
  await f.fill("очень-длинный-пароль-1234567890-проверка");
  const geo = await f.evaluate((el) => {
    const r = el.getBoundingClientRect();
    const b = document.querySelector("[data-testid=password-toggle]").getBoundingClientRect();
    const textRight = r.right - parseFloat(getComputedStyle(el).paddingRight);
    return { textRight, buttonLeft: b.left, buttonW: b.width, overflow: document.documentElement.scrollWidth - innerWidth };
  });
  assert(geo.textRight <= geo.buttonLeft + 0.5, `текст до ${geo.textRight}, кнопка с ${geo.buttonLeft}`);
  assert(geo.buttonW >= 40, `область нажатия ${geo.buttonW}px`);
  assert(geo.overflow <= 1, "нет горизонтальной прокрутки");
  await mp.getByTestId("password-toggle").tap();
  assert((await f.getAttribute("type")) === "text", "касание переключает");
  await mp.screenshot({ path: `${OUT}/password-phone.png` });
  await m.close();
});

console.log("Курсор");
await step("ссылки и кнопки — pointer, вложенные иконки наследуют", async () => {
  await page.goto(`${BASE}/dashboard`);
  await page.waitForSelector("header a[href]");
  const checks = [
    ["ссылка в шапке", page.locator("header a[href]").first()],
    ["кнопка в шапке", page.locator("header button:not(:disabled)").first()],
  ];
  for (const [name, loc] of checks) assert((await cursor(loc)) === "pointer", `${name}: ${await cursor(loc)}`);
  await page.goto(`${BASE}/login`).catch(() => {});
});

await step("формы: кнопки и выбор роли — pointer; поля и текст — без «ладони»", async () => {
  const c = await browser.newContext();
  const p = await c.newPage();
  await p.goto(`${BASE}/register`);
  await p.locator("#password").waitFor();
  const radio = p.getByRole("radio").first();
  assert((await cursor(radio)) === "pointer", "выбор роли");
  assert((await cursor(p.getByTestId("password-toggle"))) === "pointer", "кнопка глаза");
  assert((await cursor(p.getByTestId("password-toggle").locator("svg"))) === "pointer", "иконка внутри кнопки");
  assert((await cursor(p.locator("button[type=submit], form button:not([type=button])").last())) === "pointer", "кнопка отправки");
  for (const sel of ["#email", "#password", "#name"]) {
    const v = await cursor(p.locator(sel));
    assert(v !== "pointer", `${sel}: ${v}`);
  }
  assert((await cursor(p.locator("h1"))) !== "pointer", "заголовок");
  assert((await cursor(p.locator("form p").first())) !== "pointer", "обычный текст");
  // Неактивная кнопка — not-allowed
  await p.locator("form button.btn-primary").evaluate((b) => (b.disabled = true));
  assert((await cursor(p.locator("form button.btn-primary"))) === "not-allowed", "неактивная кнопка");
  await c.close();
});

await step("класс: вкладки и инструменты — pointer, неактивная вкладка — not-allowed", async () => {
  const lessons = await page.evaluate(async () => (await (await fetch("/api/lessons?scope=upcoming")).json()).lessons);
  const demo = lessons.find((l) => l.alwaysOpen);
  assert(demo, "демо-класс");
  await page.goto(`${BASE}/lessons/${demo.id}/room`);
  const join = page.getByRole("button", { name: "Войти в класс" });
  await Promise.race([join.waitFor({ timeout: 20000 }).then(() => join.click()), page.waitForSelector("[data-testid=tab-workshop]", { timeout: 20000 })]);
  await page.getByTestId("tab-workshop").waitFor({ timeout: 20000 });
  assert((await cursor(page.getByTestId("tab-workshop"))) === "pointer", "вкладка мастерской");
  assert((await cursor(page.getByTestId("tab-workshop").locator("span").last())) === "pointer", "текст внутри вкладки");
  assert((await cursor(page.getByRole("toolbar", { name: "Инструменты доски" }).locator("button").first())) === "pointer", "инструмент доски");
  const screenTab = page.locator("button", { hasText: "Экран" }).first();
  if (await screenTab.isDisabled()) assert((await cursor(screenTab)) === "not-allowed", "неактивная вкладка «Экран»");
  await page.getByTestId("tab-workshop").click();
  await page.getByTestId("workshop-text").waitFor({ timeout: 20000 });
  assert((await cursor(page.getByTestId("workshop-text"))) !== "pointer", "учебный текст");
  assert((await cursor(page.getByTestId("workshop-mode"))) === "pointer", "выбор режима (select)");
  await page.screenshot({ path: `${OUT}/cursor-room.png` });
});

await browser.close();
console.log(failed ? `\n${failed} шаг(ов) не прошли` : "\nВсе шаги прошли");
process.exit(failed ? 1 : 0);
