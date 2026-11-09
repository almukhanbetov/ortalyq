"use client";

// Вкладка «Код»: совместный редактор (Monaco + Yjs), входные данные и запуск через бэкенд.
// Модуль загружается только при первом открытии вкладки (см. code/index.tsx); Monaco грузится
// со своего домена (/monaco/vs, копируется из node_modules при сборке), без внешнего CDN.
import Editor, { loader, type OnMount } from "@monaco-editor/react";
import type * as MonacoNS from "monaco-editor";
import * as Y from "yjs";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError, CodeInfo, CodeLanguage, CodeRun, CodeRunStatus, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { formatTime } from "@/lib/format";
import type { LessonSocket, SocketStatus } from "@/lib/lessonSocket";
import { Spinner } from "../ui";
import { CodeSync, REMOTE, SyncState, transformIndex } from "./codeSync";

loader.config({ paths: { vs: "/monaco/vs" } });

type Monaco = typeof MonacoNS;
type IEditor = MonacoNS.editor.IStandaloneCodeEditor;

const statusText: Record<CodeRunStatus, string> = {
  running: "Выполняется…",
  completed: "Завершено",
  runtime_error: "Ошибка выполнения",
  compile_error: "Ошибка компиляции",
  time_limit: "Превышен лимит времени",
  memory_limit: "Превышен лимит памяти",
  output_limit: "Превышен лимит вывода",
  unavailable: "Сервис выполнения недоступен",
  not_configured: "Сервис выполнения не настроен",
  cancelled: "Отменено",
  internal_error: "Ошибка сервиса выполнения",
};

function statusTone(s: CodeRunStatus) {
  if (s === "running") return "bg-brand-100 text-brand-800";
  if (s === "completed") return "bg-emerald-100 text-emerald-800";
  if (s === "time_limit" || s === "memory_limit" || s === "output_limit" || s === "cancelled") return "bg-amber-100 text-amber-900";
  return "bg-red-100 text-red-800";
}

/** Новее ли запуск b, чем a (тот же запуск — завершённое состояние важнее «выполняется»). */
function newerRun(a: CodeRun | null, b: CodeRun | null): CodeRun | null {
  if (!b) return a;
  if (!a) return b;
  if (a.id === b.id) return a.status !== "running" && b.status === "running" ? a : b;
  return new Date(b.createdAt) >= new Date(a.createdAt) ? b : a;
}

// ---------- Привязка Monaco ↔ Y.Text ----------

function bindMonaco(sync: CodeSync, editor: IEditor, monaco: Monaco) {
  const ytext = sync.code;
  const doc = sync.doc;
  const model = editor.getModel()!;
  const LOCAL = { local: true };
  let muted = false;
  muted = true;
  model.setEOL(monaco.editor.EndOfLineSequence.LF);
  if (model.getValue() !== ytext.toString()) model.setValue(ytext.toString());
  muted = false;

  // Отмена/повтор только своих правок (чужие изменения не отменяются).
  const undo = new Y.UndoManager(ytext, { trackedOrigins: new Set([LOCAL]), captureTimeout: 400 });

  // Перед чужой транзакцией запоминаем выделения как относительные позиции Yjs.
  let saved: { start: Y.RelativePosition; end: Y.RelativePosition; dir: MonacoNS.SelectionDirection }[] | null = null;
  const before = () => {
    if (muted) return;
    saved = (editor.getSelections() ?? []).map((s) => ({
      // assoc = -1: курсор привязан к символу слева — чужой ввод в точке курсора не «толкает» курсор,
      // и серии символов разных участников не перемешиваются.
      start: Y.createRelativePositionFromTypeIndex(ytext, model.getOffsetAt(s.getStartPosition()), -1),
      end: Y.createRelativePositionFromTypeIndex(ytext, model.getOffsetAt(s.getEndPosition()), -1),
      dir: s.getDirection(),
    }));
  };
  doc.on("beforeAllTransactions", before);

  const observer = (ev: Y.YTextEvent, tr: Y.Transaction) => {
    if (tr.origin === LOCAL) return;
    muted = true;
    try {
      let index = 0;
      for (const d of ev.delta) {
        if (d.retain) index += d.retain;
        else if (typeof d.insert === "string") {
          const p = model.getPositionAt(index);
          model.applyEdits([{ range: new monaco.Range(p.lineNumber, p.column, p.lineNumber, p.column), text: d.insert }]);
          index += d.insert.length;
        } else if (d.delete) {
          const a = model.getPositionAt(index);
          const b = model.getPositionAt(index + d.delete);
          model.applyEdits([{ range: new monaco.Range(a.lineNumber, a.column, b.lineNumber, b.column), text: "" }]);
        }
      }
      if (saved && tr.origin === REMOTE) {
        const sels = saved.map((s) => {
          const a = Y.createAbsolutePositionFromRelativePosition(s.start, doc);
          const b = Y.createAbsolutePositionFromRelativePosition(s.end, doc);
          const pa = model.getPositionAt(a?.index ?? 0);
          const pb = model.getPositionAt(b?.index ?? 0);
          return monaco.Selection.createWithDirection(pa.lineNumber, pa.column, pb.lineNumber, pb.column, s.dir);
        });
        if (sels.length) editor.setSelections(sels);
      }
      // Защита от расхождения (не должно происходить): модель приводится к тексту Yjs.
      if (model.getValue() !== ytext.toString()) model.setValue(ytext.toString());
    } finally {
      saved = null;
      muted = false;
    }
  };
  ytext.observe(observer);

  const sub = model.onDidChangeContent((e) => {
    if (muted) return;
    muted = true;
    try {
      doc.transact(() => {
        [...e.changes]
          .sort((a, b) => b.rangeOffset - a.rangeOffset)
          .forEach((c) => {
            if (c.rangeLength) ytext.delete(c.rangeOffset, c.rangeLength);
            if (c.text) ytext.insert(c.rangeOffset, c.text);
          });
      }, LOCAL);
    } finally {
      muted = false;
    }
  });

  editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyZ, () => undo.undo());
  editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyZ, () => undo.redo());
  editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyY, () => undo.redo());

  return () => {
    sub.dispose();
    ytext.unobserve(observer);
    doc.off("beforeAllTransactions", before);
    undo.destroy();
  };
}

// ---------- Поле «Входные данные» ↔ Y.Text ----------

function StdinField({ sync, maxBytes }: { sync: CodeSync; maxBytes: number }) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const [len, setLen] = useState(0);

  useEffect(() => {
    const ta = ref.current!;
    const yt = sync.stdin;
    ta.value = yt.toString();
    setLen(ta.value.length);
    const LOCAL = { stdin: true };
    let prev = ta.value;
    const onInput = () => {
      const next = ta.value;
      // Минимальная правка: общий префикс и суффикс
      let s = 0;
      while (s < prev.length && s < next.length && prev[s] === next[s]) s++;
      let e = 0;
      while (e < prev.length - s && e < next.length - s && prev[prev.length - 1 - e] === next[next.length - 1 - e]) e++;
      sync.doc.transact(() => {
        if (prev.length - s - e > 0) yt.delete(s, prev.length - s - e);
        if (next.length - s - e > 0) yt.insert(s, next.slice(s, next.length - e));
      }, LOCAL);
      prev = next;
      setLen(next.length);
    };
    const observer = (ev: Y.YTextEvent, tr: Y.Transaction) => {
      if (tr.origin === LOCAL) return;
      const focused = document.activeElement === ta;
      const a = transformIndex(ev.delta as never, ta.selectionStart);
      const b = transformIndex(ev.delta as never, ta.selectionEnd);
      ta.value = prev = yt.toString();
      setLen(prev.length);
      if (focused) ta.setSelectionRange(a, b);
    };
    ta.addEventListener("input", onInput);
    yt.observe(observer);
    return () => {
      ta.removeEventListener("input", onInput);
      yt.unobserve(observer);
    };
  }, [sync]);

  const tooBig = new TextEncoder().encode(ref.current?.value ?? "").length > maxBytes;
  return (
    <div className="flex flex-col gap-1 p-3">
      <label htmlFor="code-stdin" className="flex items-center justify-between text-xs font-semibold text-muted">
        Входные данные (для input())
        <span className={tooBig ? "text-red-600" : "font-normal"}>{len ? `${len} симв.` : ""}</span>
      </label>
      <textarea
        id="code-stdin"
        ref={ref}
        rows={4}
        spellCheck={false}
        placeholder={"Каждая строка — один вызов input()\nнапример:\n5\n7"}
        className="input min-h-20 resize-y font-mono text-sm"
      />
    </div>
  );
}

// ---------- Результат ----------

function RunResult({ run, error, tz }: { run: CodeRun | null; error: string; tz: string }) {
  const [now, setNow] = useState(Date.now());
  const running = run?.status === "running";
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(t);
  }, [running]);

  if (!run && !error)
    return <p className="px-3 pb-3 text-xs text-muted">Нажмите «Запустить» — результат увидят оба участника урока.</p>;
  return (
    <div className="flex min-h-0 flex-col gap-2 px-3 pb-3" aria-live="polite">
      {error && <div className="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700" role="alert">{error}</div>}
      {run && (
        <>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className={`chip py-1 ${statusTone(run.status)}`} data-testid="run-status">
              {running && <Spinner className="h-3 w-3" />}
              {statusText[run.status] ?? run.status}
            </span>
            {running ? (
              <span className="text-muted">{((now - new Date(run.createdAt).getTime()) / 1000).toFixed(1)} с</span>
            ) : (
              <>
                {run.timeMs !== null && <span className="text-muted">время: {(run.timeMs / 1000).toFixed(3)} с</span>}
                {run.memoryKb !== null && <span className="text-muted">память: {Math.round(run.memoryKb / 1024)} МБ</span>}
                {run.exitCode !== null && <span className="text-muted">код выхода: {run.exitCode}</span>}
              </>
            )}
          </div>
          <div className="text-xs text-muted" data-testid="run-author">
            Запустил(а): <b className="text-ink">{run.userName}</b> · {formatTime(run.createdAt, tz)}
          </div>
          {run.message && run.status !== "completed" && <div className="text-xs text-muted">{run.message}</div>}
          {!running && (
            <>
              <OutputBlock title="Вывод (stdout)" text={run.stdout} empty="— пусто —" testId="run-stdout" />
              {run.stderr && <OutputBlock title="Ошибки (stderr)" text={run.stderr} tone="err" testId="run-stderr" />}
              {run.truncated && <div className="text-xs text-amber-700">Вывод обрезан до 64 КБ.</div>}
            </>
          )}
        </>
      )}
    </div>
  );
}

function OutputBlock({ title, text, empty, tone, testId }: { title: string; text: string; empty?: string; tone?: "err"; testId: string }) {
  return (
    <div>
      <div className="mb-1 text-xs font-semibold text-muted">{title}</div>
      <pre
        data-testid={testId}
        className={`max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg px-3 py-2 font-mono text-xs ${tone === "err" ? "bg-red-50 text-red-800" : "bg-ink text-white"}`}
      >
        {text || <span className="opacity-60">{empty}</span>}
      </pre>
    </div>
  );
}

// ---------- Панель ----------

export default function CodePanel({ lessonId, socket, status }: { lessonId: string; socket: LessonSocket | null; status: SocketStatus }) {
  const { tz } = useAuth();
  const [sync, setSync] = useState<CodeSync | null>(null);
  const [syncState, setSyncState] = useState<SyncState>("connecting");
  const [info, setInfo] = useState<CodeInfo | null>(null);
  const [infoError, setInfoError] = useState("");
  const [run, setRun] = useState<CodeRun | null>(null);
  const [runError, setRunError] = useState("");
  const [starting, setStarting] = useState(false);
  const [language, setLanguage] = useState("python");
  const [empty, setEmpty] = useState(true);
  const [narrow, setNarrow] = useState(false);
  const unbind = useRef<(() => void) | null>(null);

  // Документ Yjs живёт, пока открыт класс (вкладка остаётся смонтированной после первого открытия).
  useEffect(() => {
    if (!socket) return;
    const s = new CodeSync(socket, setSyncState, (m) => setRunError(`Изменение не сохранено: ${m}`));
    const onMeta = () => setLanguage(s.meta.get("language") ?? "python");
    const onCode = () => setEmpty(s.code.length === 0);
    s.meta.observe(onMeta);
    s.code.observe(onCode);
    s.start();
    setSync(s);
    return () => {
      unbind.current?.();
      unbind.current = null;
      s.stop();
      setSync(null);
    };
  }, [socket]);

  // Языки, доступность исполнителя и последний запуск; перезапрашиваем после переподключения.
  const loadInfo = useCallback(() => {
    api<CodeInfo>(`/api/lessons/${lessonId}/code`)
      .then((i) => {
        setInfo(i);
        setInfoError("");
        setRun((r) => newerRun(r, i.lastRun));
      })
      .catch((e) => setInfoError(errorMessage(e)));
  }, [lessonId]);
  useEffect(() => {
    if (status === "open") loadInfo();
  }, [status, loadInfo]);

  useEffect(() => {
    if (!socket) return;
    return socket.on((m) => {
      if (m.type === "code.run") setRun((r) => newerRun(r, m.run as CodeRun));
    });
  }, [socket]);

  useEffect(() => {
    const mq = window.matchMedia("(max-width: 640px)");
    const on = () => setNarrow(mq.matches);
    on();
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);

  const languages: CodeLanguage[] = info?.languages ?? [{ id: "python", label: "Python 3", monaco: "python" }];
  const lang = languages.find((l) => l.id === language) ?? languages[0];
  const monacoRef = useRef<{ editor: IEditor; monaco: Monaco } | null>(null);
  const languageRef = useRef(lang.monaco);
  languageRef.current = lang.monaco;

  useEffect(() => {
    const m = monacoRef.current;
    const model = m?.editor.getModel();
    if (m && model) m.monaco.editor.setModelLanguage(model, lang.monaco);
  }, [lang.monaco]);

  // onMount вызывается с замыканием первого рендера — актуальный документ берём из ref.
  const syncRef = useRef<CodeSync | null>(null);
  syncRef.current = sync;
  const onMount: OnMount = (editor, monaco) => {
    monacoRef.current = { editor, monaco };
    monaco.editor.setModelLanguage(editor.getModel()!, languageRef.current);
    unbind.current?.();
    unbind.current = syncRef.current ? bindMonaco(syncRef.current, editor, monaco) : null;
  };
  // Если документ создан позже редактора (или пересоздан после смены сокета) — перепривязываем.
  useEffect(() => {
    const m = monacoRef.current;
    if (!m || !sync) return;
    unbind.current?.();
    unbind.current = bindMonaco(sync, m.editor, m.monaco);
  }, [sync]);

  const running = run?.status === "running";
  const runnerConfigured = info?.runnerConfigured ?? false;
  const disabledReason = !info
    ? "Загружаем настройки…"
    : !runnerConfigured
      ? "Сервис выполнения не настроен"
      : running
        ? "Программа выполняется"
        : empty
          ? "Напишите код"
          : "";

  const start = async () => {
    if (!sync || disabledReason) return;
    // Конкретный снимок кода и входных данных на момент нажатия.
    const source = sync.code.toString();
    const stdin = sync.stdin.toString();
    const enc = new TextEncoder();
    if (info && enc.encode(source).length > info.limits.sourceBytes) return setRunError("Код слишком большой (максимум 64 КБ)");
    if (info && enc.encode(stdin).length > info.limits.stdinBytes) return setRunError("Входные данные слишком большие (максимум 64 КБ)");
    setStarting(true);
    setRunError("");
    try {
      const res = await api<{ run: CodeRun }>(`/api/lessons/${lessonId}/code/runs`, { method: "POST", json: { language: lang.id, source, stdin } });
      setRun((r) => newerRun(r, res.run));
    } catch (e) {
      setRunError(e instanceof ApiError && e.status === 0 ? "Нет соединения с сервером" : errorMessage(e));
    } finally {
      setStarting(false);
    }
  };

  const cancel = async () => {
    if (!run || run.status !== "running") return;
    try {
      await api(`/api/lessons/${lessonId}/code/runs/${run.id}/cancel`, { method: "POST" });
    } catch (e) {
      setRunError(errorMessage(e));
    }
  };

  const setLang = (id: string) => sync?.doc.transact(() => sync.meta.set("language", id));

  const editorOptions = useMemo<MonacoNS.editor.IStandaloneEditorConstructionOptions>(
    () => ({
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: narrow ? 13 : 14,
      tabSize: 4,
      insertSpaces: true,
      wordWrap: narrow ? "on" : "off",
      lineNumbersMinChars: narrow ? 2 : 3,
      folding: !narrow,
      glyphMargin: false,
      scrollBeyondLastLine: false,
      renderLineHighlight: "line",
      fixedOverflowWidgets: true,
      ariaLabel: "Редактор кода",
    }),
    [narrow],
  );

  return (
    <div className="flex h-full flex-col bg-white" data-testid="code-panel">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-2 py-1.5">
        {languages.length > 1 ? (
          <select className="input w-auto py-1 text-sm" value={lang.id} onChange={(e) => setLang(e.target.value)} aria-label="Язык программирования">
            {languages.map((l) => (
              <option key={l.id} value={l.id}>
                {l.label}
              </option>
            ))}
          </select>
        ) : (
          <span className="chip bg-canvas py-1 text-ink">{lang.label}</span>
        )}
        <span className={`text-xs ${syncState === "synced" ? "text-emerald-600" : "text-amber-600"}`} data-testid="code-sync">
          {syncState === "synced" ? "● синхронизировано" : syncState === "offline" ? "● нет связи — правки отправятся позже" : "● подключение…"}
        </span>
        <div className="flex-1" />
        {running && (
          <button className="btn-ghost btn-sm" onClick={cancel}>
            Остановить
          </button>
        )}
        <button className="btn-primary btn-sm" onClick={start} disabled={!!disabledReason || starting} title={disabledReason} data-testid="code-run">
          {starting || running ? <Spinner className="h-4 w-4" /> : <span aria-hidden>▶</span>} Запустить
        </button>
      </div>
      {info && !runnerConfigured && (
        <div className="border-b border-line bg-amber-50 px-3 py-1.5 text-xs text-amber-900" role="status">
          Сервис выполнения не настроен — код можно писать и редактировать вместе, но запуск недоступен.
        </div>
      )}
      {infoError && <div className="border-b border-line bg-red-50 px-3 py-1.5 text-xs text-red-700">{infoError}</div>}

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto lg:flex-row lg:overflow-hidden">
        <div className="relative h-[45vh] shrink-0 lg:h-auto lg:min-w-0 lg:flex-1" data-testid="code-editor">
          <Editor
            defaultLanguage={lang.monaco}
            options={editorOptions}
            onMount={onMount}
            loading={
              <div className="flex items-center gap-2 text-sm text-muted">
                <Spinner className="h-4 w-4" /> Загружаем редактор…
              </div>
            }
          />
          {empty && (
            <div className="pointer-events-none absolute left-12 top-1 pr-3 font-mono text-sm text-muted/70">
              Напишите программу на {lang.label}. Ввод через input() берётся из поля «Входные данные».
            </div>
          )}
        </div>
        <div className="flex shrink-0 flex-col border-t border-line lg:w-96 lg:shrink lg:overflow-y-auto lg:border-l lg:border-t-0">
          {sync && <StdinField sync={sync} maxBytes={info?.limits.stdinBytes ?? 65536} />}
          <div className="px-3 pb-1 text-xs font-semibold text-muted">Результат</div>
          <RunResult run={run} error={runError} tz={tz} />
          {info && (
            <p className="mt-auto px-3 pb-3 text-[11px] text-muted">
              Лимиты: {info.limits.cpuSeconds} с CPU, {info.limits.wallSeconds} с всего, {info.limits.memoryMb} МБ памяти, вывод до{" "}
              {Math.round(info.limits.outputBytes / 1024)} КБ, без доступа к сети.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
