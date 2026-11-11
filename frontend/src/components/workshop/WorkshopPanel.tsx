"use client";

// Вкладка «Учебная мастерская»: совместный учебный текст (отдельный документ Yjs «workshop»),
// разбор выделенного фрагмента и домашнее задание. Предметные особенности приходят с сервера
// как данные режима (modes[].actions) — отдельных компонентов на каждый предмет нет.
// Запросы к ИИ отправляются только по нажатию кнопки преподавателем; при наборе текста запросов нет.
import * as Y from "yjs";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError, errorMessage, WorkshopAnalysis, WorkshopHomework, WorkshopState } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import type { LessonSocket, SocketStatus } from "@/lib/lessonSocket";
import { SyncState, transformIndex, YjsSync } from "@/lib/yjsSync";
import { Modal, Spinner, toast } from "../ui";
import { WorkshopMaterials } from "./Materials";

type Pending = { kind: "analysis"; action: string } | { kind: "homework" };

export default function WorkshopPanel({ lessonId, socket, status }: { lessonId: string; socket: LessonSocket | null; status: SocketStatus }) {
  const { tz } = useAuth();
  const [state, setState] = useState<WorkshopState | null>(null);
  const [loadError, setLoadError] = useState("");
  const [sync, setSync] = useState<YjsSync | null>(null);
  const [syncState, setSyncState] = useState<SyncState>("connecting");
  const [locked, setLocked] = useState(false);
  const [level, setLevel] = useState("standard");
  const [sel, setSel] = useState({ start: 0, end: 0 });
  const [textLen, setTextLen] = useState(0);
  const [busy, setBusy] = useState<Pending | null>(null);
  const [aiError, setAiError] = useState("");
  const [notice, setNotice] = useState<Pending | null>(null);
  const ta = useRef<HTMLTextAreaElement>(null);

  const load = useCallback(() => {
    api<WorkshopState>(`/api/lessons/${lessonId}/workshop`)
      .then((s) => {
        setState(s);
        setLoadError("");
      })
      .catch((e) => setLoadError(errorMessage(e)));
  }, [lessonId]);

  // Состояние перечитывается после (пере)подключения и по уведомлению «workshop.changed» (без содержимого:
  // что именно показать, сервер решает по роли при HTTP-запросе).
  useEffect(() => {
    if (status === "open") load();
  }, [status, load]);
  useEffect(() => {
    if (!socket) return;
    return socket.on((m) => {
      if (m.type === "workshop.changed") load();
      if (m.type === "lesson.status") load();
    });
  }, [socket, load]);

  useEffect(() => {
    if (!socket) return;
    const s = new YjsSync("workshop", socket, setSyncState, (m) => toast(`Изменение не сохранено: ${m}`, "err"), setLocked);
    s.start();
    setSync(s);
    return () => {
      s.stop();
      setSync(null);
    };
  }, [socket]);

  const ready = !!state;
  // Привязка textarea ↔ Y.Text: минимальная правка по общему префиксу/суффиксу, чужие правки сдвигают курсор.
  useEffect(() => {
    const el = ta.current;
    if (!el || !sync) return;
    const yt: Y.Text = sync.doc.getText("text");
    const LOCAL = { workshop: true };
    el.value = yt.toString();
    let prev = el.value;
    setTextLen(prev.length);
    const onInput = () => {
      const next = el.value;
      let s = 0;
      while (s < prev.length && s < next.length && prev[s] === next[s]) s++;
      let e = 0;
      while (e < prev.length - s && e < next.length - s && prev[prev.length - 1 - e] === next[next.length - 1 - e]) e++;
      sync.doc.transact(() => {
        if (prev.length - s - e > 0) yt.delete(s, prev.length - s - e);
        if (next.length - s - e > 0) yt.insert(s, next.slice(s, next.length - e));
      }, LOCAL);
      prev = next;
      setTextLen(next.length);
    };
    const observer = (ev: Y.YTextEvent, tr: Y.Transaction) => {
      if (tr.origin === LOCAL) return;
      const focused = document.activeElement === el;
      const a = transformIndex(ev.delta as never, el.selectionStart);
      const b = transformIndex(ev.delta as never, el.selectionEnd);
      el.value = prev = yt.toString();
      setTextLen(prev.length);
      if (focused) el.setSelectionRange(a, b);
      setSel({ start: a, end: b });
    };
    el.addEventListener("input", onInput);
    yt.observe(observer);
    return () => {
      el.removeEventListener("input", onInput);
      yt.unobserve(observer);
    };
  }, [sync, ready]);

  // Выделение запоминается: после нажатия кнопки фокус уходит из поля, но фрагмент остаётся тем же.
  const trackSelection = () => {
    const el = ta.current;
    if (el && document.activeElement === el) setSel({ start: el.selectionStart, end: el.selectionEnd });
  };
  useEffect(() => {
    document.addEventListener("selectionchange", trackSelection);
    return () => document.removeEventListener("selectionchange", trackSelection);
  }, []);

  if (loadError && !state) return <div className="p-4 text-sm text-red-700">{loadError}</div>;
  if (!state)
    return (
      <div className="flex h-full items-center justify-center gap-2 text-sm text-muted">
        <Spinner className="h-4 w-4" /> Загружаем мастерскую…
      </div>
    );

  const teacher = state.role === "teacher";
  const mode = state.modes.find((m) => m.id === state.mode) ?? state.modes[0];
  const readOnly = !state.editable || locked;
  const aiReady = teacher && state.editable && !!state.ai?.configured;
  const selected = ta.current ? ta.current.value.slice(sel.start, sel.end) : "";
  const fragment = selected.trim();
  const tooLong = fragment.length > state.limits.fragmentChars;
  const suggested = teacher && state.suggestedMode && state.suggestedMode !== state.mode ? state.modes.find((m) => m.id === state.suggestedMode) : null;

  const setMode = async (id: string) => {
    try {
      await api(`/api/lessons/${lessonId}/workshop/mode`, { method: "PUT", json: { mode: id } });
      setState((s) => (s ? { ...s, mode: id } : s));
    } catch (e) {
      toast(errorMessage(e), "err");
    }
  };

  const perform = async (p: Pending) => {
    if (busy) return; // защита от повторных нажатий (сервер дополнительно отклоняет параллельный запрос)
    setBusy(p);
    setAiError("");
    try {
      if (p.kind === "analysis") {
        const r = await api<{ analysis: WorkshopAnalysis }>(`/api/lessons/${lessonId}/workshop/analyses`, {
          method: "POST",
          json: { action: p.action, level, fragment },
        });
        setState((s) => (s ? { ...s, analyses: [...s.analyses, r.analysis] } : s));
      } else {
        const full = ta.current?.value ?? "";
        const text = (fragment || full).trim();
        const r = await api<{ homework: WorkshopHomework }>(`/api/lessons/${lessonId}/workshop/homework`, { method: "POST", json: { text, level } });
        setState((s) => (s ? { ...s, homework: [...s.homework, r.homework] } : s));
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 428) {
        setState((s) => (s && s.ai ? { ...s, ai: { ...s.ai, noticeAccepted: false } } : s));
        setNotice(p);
      } else setAiError(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const request = (p: Pending) => {
    if (!state.ai?.noticeAccepted) setNotice(p);
    else perform(p);
  };

  const acceptNotice = async () => {
    const p = notice;
    setNotice(null);
    try {
      await api(`/api/lessons/${lessonId}/workshop/ai-notice`, { method: "POST" });
      setState((s) => (s && s.ai ? { ...s, ai: { ...s.ai, noticeAccepted: true } } : s));
      if (p) perform(p);
    } catch (e) {
      toast(errorMessage(e), "err");
    }
  };

  const hwText = (fragment || (ta.current?.value ?? "")).trim();
  const actionReason = !aiReady ? (state.editable ? `ИИ недоступен: ${state.ai?.problem ?? ""}` : "Урок завершён") : !fragment ? "Выделите фрагмент текста" : tooLong ? `Фрагмент длиннее ${state.limits.fragmentChars} символов` : "";
  const hwReason = !aiReady ? actionReason : !hwText ? "Напишите учебный текст" : hwText.length > state.limits.homeworkTextChars ? `Текст длиннее ${state.limits.homeworkTextChars} символов — выделите часть` : "";

  return (
    <div className="flex h-full flex-col bg-white" data-testid="workshop-panel">
      {/* Панель режима и сложности */}
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-2 py-1.5">
        {teacher && state.editable ? (
          <select className="input w-auto py-1 text-sm" value={state.mode} onChange={(e) => setMode(e.target.value)} aria-label="Режим мастерской" data-testid="workshop-mode">
            {state.modes.map((m) => (
              <option key={m.id} value={m.id}>
                Режим: {m.label}
              </option>
            ))}
          </select>
        ) : (
          <span className="chip bg-canvas py-1 text-ink">Режим: {mode.label}</span>
        )}
        {teacher && (
          <select className="input w-auto py-1 text-sm" value={level} onChange={(e) => setLevel(e.target.value)} aria-label="Сложность" data-testid="workshop-level">
            {state.levels.map((l) => (
              <option key={l.id} value={l.id}>
                Сложность: {l.label}
              </option>
            ))}
          </select>
        )}
        <span className="text-xs text-muted">Предмет: {state.subject}</span>
        <span className={`text-xs ${syncState === "synced" ? "text-emerald-600" : "text-amber-600"}`} data-testid="workshop-sync">
          {readOnly ? "● только чтение" : syncState === "synced" ? "● синхронизировано" : syncState === "offline" ? "● нет связи — правки отправятся позже" : "● подключение…"}
        </span>
      </div>
      {suggested && state.editable && (
        <div className="flex flex-wrap items-center gap-2 border-b border-line bg-brand-50 px-3 py-1.5 text-xs text-brand-900">
          Для этого предмета есть режим «{suggested.label}».
          <button className="btn-secondary btn-sm" onClick={() => setMode(suggested.id)}>
            Включить
          </button>
        </div>
      )}
      {!state.editable && (
        <div className="border-b border-line bg-amber-50 px-3 py-1.5 text-xs text-amber-900" role="status">
          {state.closedReason || "Урок завершён"}. Текст доступен только для чтения, опубликованные материалы — на странице урока.
        </div>
      )}
      {teacher && state.editable && !state.ai?.configured && (
        <div className="border-b border-line bg-amber-50 px-3 py-1.5 text-xs text-amber-900" role="status" data-testid="workshop-ai-off">
          Функции ИИ недоступны: {state.ai?.problem}. Совместный текст и сохранённые материалы работают.
        </div>
      )}

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto lg:flex-row lg:overflow-hidden">
        {/* Учебный текст */}
        <div className="flex min-h-[45vh] shrink-0 flex-col lg:min-h-0 lg:min-w-0 lg:flex-1">
          <textarea
            ref={ta}
            readOnly={readOnly}
            onSelect={trackSelection}
            onKeyUp={trackSelection}
            onMouseUp={trackSelection}
            onTouchEnd={trackSelection}
            spellCheck
            lang={state.mode === "english" ? "en" : "ru"}
            placeholder={readOnly ? "Текст пуст" : "Общий учебный текст урока: задание, ответ ученика, текст для разбора… Его видят и правят оба участника."}
            className="min-h-0 w-full flex-1 resize-none p-3 text-[15px] leading-relaxed outline-none read-only:bg-canvas/60"
            aria-label="Учебный текст"
            data-testid="workshop-text"
          />
          <div className="flex items-center justify-between border-t border-line px-3 py-1 text-[11px] text-muted">
            <span>{textLen} симв.</span>
            {fragment && <span data-testid="workshop-selection">Выделено: {fragment.length} симв.</span>}
          </div>
        </div>

        {/* Действия и результаты */}
        <div className="flex shrink-0 flex-col border-t border-line lg:w-[26rem] lg:shrink lg:overflow-y-auto lg:border-l lg:border-t-0">
          {teacher && (
            <div className="space-y-2 border-b border-line p-3">
              <div className="text-xs font-semibold text-muted">Разбор выделенного фрагмента</div>
              <div className="flex flex-wrap gap-1.5">
                {mode.actions.map((a) => (
                  <button
                    key={a.id}
                    className="btn-secondary btn-sm"
                    title={actionReason || a.hint}
                    disabled={!!actionReason || !!busy}
                    onClick={() => request({ kind: "analysis", action: a.id })}
                    data-testid={`workshop-action-${a.id}`}
                  >
                    {busy?.kind === "analysis" && busy.action === a.id && <Spinner className="h-3 w-3" />}
                    {a.label}
                  </button>
                ))}
              </div>
              <button className="btn-primary btn-sm w-full" title={hwReason || "По выделенному (или всему) тексту и опубликованным разборам"} disabled={!!hwReason || !!busy} onClick={() => request({ kind: "homework" })} data-testid="workshop-homework">
                {busy?.kind === "homework" && <Spinner className="h-3 w-3 text-white" />}
                Подготовить домашнее задание
              </button>
              {busy && <p className="text-xs text-muted" role="status">ИИ готовит ответ — это может занять до минуты…</p>}
              {aiError && (
                <div className="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700" role="alert" data-testid="workshop-ai-error">
                  {aiError}
                </div>
              )}
              <p className="text-[11px] text-muted">Исходный текст не меняется. Результаты сначала видны только вам — ученик увидит их после «Показать ученику» или «Опубликовать».</p>
            </div>
          )}
          <div className="p-3">
            <WorkshopMaterials
              lessonId={lessonId}
              state={state}
              setState={(fn) => setState((s) => (s ? fn(s) : s))}
              tz={tz}
              canModify
              emptyText={teacher ? "Выделите фрагмент в тексте и выберите действие." : "Разборы и задания появятся здесь, когда преподаватель их покажет."}
            />
          </div>
        </div>
      </div>

      <Modal
        open={!!notice}
        title="Передача текста внешнему ИИ"
        onClose={() => setNotice(null)}
        footer={
          <>
            <button className="btn-secondary" onClick={() => setNotice(null)}>
              Отмена
            </button>
            <button className="btn-primary" onClick={acceptNotice} data-testid="workshop-notice-accept">
              Понятно, отправить
            </button>
          </>
        }
      >
        <div className="space-y-2">
          <p>
            Выбранный учебный текст будет отправлен внешнему сервису ИИ (OpenAI) для подготовки рекомендации. Для домашнего задания также отправляются опубликованные разборы
            этого урока.
          </p>
          <p className="text-muted">Имена, email и другие данные участников не передаются. Не выделяйте личные данные в тексте.</p>
          <p className="text-muted">Ответ ИИ — рекомендация: он может быть неточным. Проверьте и при необходимости исправьте его перед тем, как показать ученику. Оценки ИИ не ставит.</p>
        </div>
      </Modal>
    </div>
  );
}
