"use client";

// Пробный урок: подготовленные примеры для доски, совместного кода и учебного текста, пример результата ИИ,
// подсказки по инструментам и окно приглашения. Примеры вставляет один участник по разрешению сервера
// (board.seed / code.seed / workshop.seed) — дальше это обычное общее содержимое, его можно править и удалять.
import type { ExcalidrawElementSkeleton } from "@excalidraw/excalidraw/data/transform";
import type * as Y from "yjs";
import { useState } from "react";
import type { DemoState } from "@/lib/api";
import { formatTime } from "@/lib/format";
import { useAuth } from "@/lib/auth";
import { Modal, toast } from "../ui";

const violet = "#5b21b6";

export function demoBoardSeed(): ExcalidrawElementSkeleton[] {
  return [
    { type: "text", x: 40, y: 20, text: "Пробный урок: общая доска", fontSize: 28, strokeColor: violet },
    {
      type: "text",
      x: 40,
      y: 66,
      fontSize: 16,
      strokeColor: "#6b6880",
      text: "Рисуйте, пишите и стирайте — изменения сразу видят все участники.\nИнструменты — на панели сверху (карандаш, текст, фигуры, ластик).",
    },
    {
      type: "rectangle",
      x: 40,
      y: 140,
      width: 300,
      height: 110,
      backgroundColor: "#ede9fe",
      fillStyle: "solid",
      strokeColor: violet,
      label: { text: "x² + 5x + 6 = 0\nРешите уравнение →", fontSize: 20 },
    },
    { type: "arrow", x: 350, y: 195, width: 110, height: 0, strokeColor: violet },
    {
      type: "ellipse",
      x: 470,
      y: 140,
      width: 260,
      height: 110,
      strokeColor: "#047857",
      backgroundColor: "#d1fae5",
      fillStyle: "solid",
      label: { text: "(x + 2)(x + 3) = 0\nx = −2, x = −3", fontSize: 20 },
    },
    {
      type: "text",
      x: 40,
      y: 280,
      fontSize: 16,
      strokeColor: "#6b6880",
      text: "Попробуйте: обведите ответ карандашом или добавьте свою фигуру.",
    },
  ];
}

export function demoCodeSeed(doc: Y.Doc) {
  const code = doc.getText("code");
  if (code.length > 0) return;
  code.insert(
    0,
    `# Пробный урок: совместный редактор кода (Python 3).
# Правьте код вместе — изменения сразу видны всем участникам.

def fib(n):
    a, b = 0, 1
    for _ in range(n):
        a, b = b, a + b
    return a

n = int(input())  # значение берётся из поля «Входные данные»
print(f"Число Фибоначчи №{n}: {fib(n)}")
`,
  );
  const stdin = doc.getText("stdin");
  if (stdin.length === 0) stdin.insert(0, "10");
  doc.getMap<string>("meta").set("language", "python");
}

export const demoWorkshopText = `Задача. В классе 24 ученика. Треть из них занимается спортом. Сколько учеников не занимается спортом?

Ответ ученика: 24 : 3 = 8. Ответ: спортом не занимаются 8 учеников.`;

export function demoWorkshopSeed(doc: Y.Doc) {
  const t = doc.getText("text");
  if (t.length === 0) t.insert(0, demoWorkshopText);
}

/** Заранее подготовленный разбор (ИИ в пробном уроке не вызывается). */
export function DemoAiSample() {
  // Те же подпись и цвет, что у вердикта «Есть ошибка» в мастерской (Materials не импортируем — он грузится лениво).
  const v = { text: "Есть ошибка", cls: "bg-red-100 text-red-800" };
  return (
    <div className="space-y-2 rounded-xl border border-dashed border-brand-300 bg-brand-50/50 p-3" data-testid="demo-ai-sample">
      <div className="flex flex-wrap items-center gap-2">
        <span className="chip bg-brand-600 text-white">Пример результата</span>
        <span className="text-xs text-muted">«Проверить решение» · стандартно</span>
      </div>
      <p className="rounded-lg bg-white px-2 py-1 text-xs text-muted">Фрагмент: «24 : 3 = 8. Ответ: спортом не занимаются 8 учеников.»</p>
      <span className={`chip ${v.cls}`}>{v.text}</span>
      <div>
        <div className="text-sm font-semibold">Где ошибка</div>
        <p className="text-sm text-ink/90">24 : 3 = 8 — это число учеников, которые занимаются спортом. Вопрос задачи — о тех, кто не занимается.</p>
      </div>
      <div>
        <div className="text-sm font-semibold">Как исправить</div>
        <p className="text-sm text-ink/90">24 − 8 = 16. Ответ: спортом не занимаются 16 учеников.</p>
      </div>
      <div>
        <div className="text-sm font-semibold">Совет</div>
        <p className="text-sm text-ink/90">Перед ответом перечитайте вопрос и проверьте, о какой части класса он.</p>
      </div>
      <p className="text-[11px] text-muted">
        Это заранее подготовленный пример: в пробном уроке текст не отправляется в ИИ. На настоящем уроке преподаватель выделяет фрагмент и выбирает действие; разбор сначала видит
        только он, проверяет и показывает ученику.
      </p>
    </div>
  );
}

type Tool = "board" | "code" | "workshop";

/** Краткие подсказки по инструментам класса (только реально доступные здесь возможности). */
export function DemoHints({ demo, onClose, onOpen }: { demo: DemoState; onClose: () => void; onOpen: (t: Tool) => void }) {
  const f = demo.features;
  const items: { icon: string; title: string; text: string; tool?: Tool; off?: string }[] = [
    { icon: "🎥", title: "Видео", text: "Кнопки микрофона, камеры и экрана — внизу.", off: f.video ? "" : f.videoProblem },
    { icon: "✏️", title: "Доска", text: "Рисуйте и пишите вместе. Пример уже на доске.", tool: "board" },
    { icon: "💻", title: "Код", text: f.codeRun ? `Общий редактор Python. Запуск — до ${f.codeRunLimit} раз.` : "Общий редактор Python.", tool: "code", off: f.codeRun ? "" : f.codeProblem },
    { icon: "📝", title: "Мастерская", text: "Общий учебный текст и пример разбора.", tool: "workshop", off: "" },
    { icon: "💬", title: "Чат", text: demo.mode === "pair" ? "Сообщения видят оба участника." : "Справа (на телефоне — вкладка «Чат»)." },
  ];
  return (
    <div className="border-b border-line bg-brand-50/60 px-3 py-2" data-testid="demo-hints">
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <span className="text-xs font-semibold text-brand-900">Подсказки · что можно попробовать</span>
        <button className="btn-ghost btn-sm px-2 py-1 text-xs" onClick={onClose} aria-label="Скрыть подсказки">
          Скрыть ✕
        </button>
      </div>
      <div className="flex gap-2 overflow-x-auto pb-1">
        {items.map((it) => {
          const body = (
            <>
              <div className="flex items-center gap-1.5 text-xs font-semibold">
                <span aria-hidden>{it.icon}</span>
                {it.title}
              </div>
              <div className="mt-0.5 text-[11px] leading-snug text-muted">{it.text}</div>
              {it.off && <div className="mt-0.5 text-[11px] leading-snug text-amber-800">{it.off}</div>}
            </>
          );
          return it.tool ? (
            <button key={it.title} onClick={() => onOpen(it.tool!)} className="w-44 shrink-0 rounded-xl border border-line bg-white p-2 text-left hover:border-brand-300">
              {body}
            </button>
          ) : (
            <div key={it.title} className="w-44 shrink-0 rounded-xl border border-line bg-white p-2">
              {body}
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** Ссылка-приглашение второго участника (временная: действует до окончания демонстрации, один вход). */
export function DemoInvite({ open, onClose, path, expiresAt }: { open: boolean; onClose: () => void; path: string; expiresAt: string }) {
  return (
    <Modal open={open} title="Пригласить второго участника" onClose={onClose}>
      <InviteLink path={path} expiresAt={expiresAt} />
    </Modal>
  );
}

export function InviteLink({ path, expiresAt }: { path: string; expiresAt: string }) {
  const { tz } = useAuth();
  const [copied, setCopied] = useState(false);
  const url = typeof window !== "undefined" ? `${window.location.origin}${path}` : path;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      toast("Ссылка скопирована");
    } catch {
      toast("Не удалось скопировать — выделите ссылку и скопируйте вручную", "err");
    }
  };
  const share = async () => {
    try {
      await navigator.share({ title: "Пробный урок ORTALYQ", text: "Присоединяйтесь к пробному уроку", url });
    } catch {
      /* пользователь закрыл окно */
    }
  };
  const canShare = typeof navigator !== "undefined" && "share" in navigator;
  return (
    <div className="space-y-2 text-sm">
      <p>Отправьте ссылку второму участнику — он войдёт как «ученик» без регистрации. Войти по ней может только один человек.</p>
      <input className="input font-mono text-xs" readOnly value={url} onFocus={(e) => e.currentTarget.select()} aria-label="Ссылка-приглашение" data-testid="demo-invite-url" />
      <div className="flex flex-wrap gap-2">
        <button className="btn-primary btn-sm" onClick={copy}>
          {copied ? "Скопировано ✓" : "Скопировать ссылку"}
        </button>
        {canShare && (
          <button className="btn-secondary btn-sm" onClick={share}>
            Поделиться…
          </button>
        )}
      </div>
      <p className="text-xs text-muted">Ссылка временная: действует до {formatTime(expiresAt, tz)} — до конца демонстрации.</p>
    </div>
  );
}
