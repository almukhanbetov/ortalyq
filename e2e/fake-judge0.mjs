// ТОЛЬКО ДЛЯ ТЕСТОВ. Заглушка API Judge0 CE (POST /submissions, GET /submissions/:token): бэкенд запускается
// с CODE_RUNNER_URL=http://127.0.0.1:<порт> и проходит весь путь реального адаптера, но КОД НЕ ВЫПОЛНЯЕТСЯ —
// ответ фиксированный: stdout «[ЗАГЛУШКА] код не выполнялся». Проверяются маршрут запуска, лимиты и показ
// результата у участников, а не выполнение Python. В приложение не входит.
//
//   PORT=12358 node fake-judge0.mjs        GET /stats → {"submissions": N}
import { createServer } from "node:http";

const PORT = Number(process.env.PORT ?? 12358);
let submissions = 0;
const b64 = (s) => Buffer.from(s).toString("base64");

createServer((req, res) => {
  res.setHeader("Content-Type", "application/json");
  if (req.method === "GET" && req.url === "/stats") return res.end(JSON.stringify({ submissions }));
  if (req.method === "POST" && req.url.startsWith("/submissions")) {
    req.resume();
    req.on("end", () => {
      submissions++;
      res.writeHead(201).end(JSON.stringify({ token: `fake-${submissions}` }));
    });
    return;
  }
  if (req.method === "GET" && req.url.startsWith("/submissions/fake-")) {
    return res.end(
      JSON.stringify({
        stdout: b64("[ЗАГЛУШКА] код не выполнялся\n"),
        stderr: null,
        compile_output: null,
        message: null,
        exit_code: 0,
        time: "0.010",
        memory: 1024,
        status: { id: 3, description: "Accepted" },
      }),
    );
  }
  res.writeHead(404).end("{}");
}).listen(PORT, "127.0.0.1", () => console.log(`fake-judge0 on :${PORT}`));
