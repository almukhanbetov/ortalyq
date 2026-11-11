// ТОЛЬКО ДЛЯ ТЕСТОВ. Локальный сервер в формате OpenAI Responses API (POST /v1/responses):
// бэкенд запускается с OPENAI_BASE_URL=http://127.0.0.1:<порт>/v1 и проходит весь путь реального
// адаптера (HTTP, заголовки, Structured Outputs, разбор ответа), но платный API не вызывается.
// Ответы — фиксированные образцы с пометкой [ТЕСТ]; это не ИИ, в приложение не входит.
//
//   PORT=18191 node fake-openai.mjs
// Управляющие слова во фрагменте: FAKE_TIMEOUT — не отвечать (таймаут бэкенда), FAKE_500 — ошибка 500,
// FAKE_BADJSON — ответ не по схеме.
import { createServer } from "node:http";

const PORT = Number(process.env.PORT ?? 18191);
let requests = 0;

function response(text, status = "completed") {
  return JSON.stringify({
    id: `resp_fake_${requests}`,
    object: "response",
    status,
    model: "fake-e2e-model",
    output: [{ type: "message", role: "assistant", content: [{ type: "output_text", text }] }],
  });
}

createServer((req, res) => {
  if (req.method === "GET" && req.url === "/stats") {
    res.end(JSON.stringify({ requests }));
    return;
  }
  if (req.method !== "POST" || req.url !== "/v1/responses") {
    res.writeHead(404).end("{}");
    return;
  }
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    requests++;
    if (req.headers.authorization !== "Bearer sk-fake-e2e-key") {
      res.writeHead(401).end(JSON.stringify({ error: { message: "bad key", type: "invalid_request_error" } }));
      return;
    }
    const r = JSON.parse(body);
    const input = String(r.input ?? "");
    const schema = r.text?.format?.name;
    if (input.includes("FAKE_TIMEOUT")) return; // не отвечаем
    if (input.includes("FAKE_500")) {
      res.writeHead(500).end(JSON.stringify({ error: { message: "internal" } }));
      return;
    }
    res.setHeader("Content-Type", "application/json");
    if (input.includes("FAKE_BADJSON")) {
      res.end(response(`{"verdict":"none","sections":"<img src=x onerror=alert(1)>"}`));
      return;
    }
    if (schema === "workshop_homework") {
      const ex = (i) => ({ title: `[ТЕСТ] Упражнение ${i}`, instruction: `Инструкция ${i}`, content: `Задание ${i}` });
      res.end(
        response(
          JSON.stringify({
            review: "[ТЕСТ] Повторение темы",
            exercises: [1, 2, 3, 4, 5].map(ex),
            answers: [1, 2, 3, 4, 5].map((i) => `[ТЕСТ] Ответ-${i}-СЕКРЕТ`),
            checking_tips: "[ТЕСТ] Совет-по-проверке-СЕКРЕТ",
          }),
        ),
      );
      return;
    }
    const english = String(r.instructions ?? "").includes("английский язык");
    const check = String(r.instructions ?? "").includes("insufficient_data — данных");
    const sections = english
      ? [
          { heading: "Исправленный вариант", body: "[ТЕСТ] She goes to school." },
          { heading: "Ошибки и объяснения", body: "[ТЕСТ] go → goes: третье лицо единственного числа." },
        ]
      : [{ heading: "[ТЕСТ] Объяснение", body: "Черновик-разбора <b>не HTML</b>" }];
    res.end(response(JSON.stringify({ verdict: check ? "insufficient_data" : "none", sections })));
  });
}).listen(PORT, "127.0.0.1", () => console.log(`fake-openai on :${PORT}`));
