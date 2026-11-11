"use client";

// Разборы и домашние задания мастерской: просмотр (ученик — только опубликованное) и
// правка/публикация (преподаватель). Используется во вкладке класса и на странице урока.
// Содержимое от ИИ выводится только как текст (React экранирует его), HTML не исполняется.
import { useState } from "react";
import {
  api,
  errorMessage,
  Verdict,
  WorkshopAnalysis,
  WorkshopAnalysisContent,
  WorkshopAnswerKey,
  WorkshopHomework,
  WorkshopHomeworkContent,
  WorkshopState,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { ConfirmModal, Spinner, toast } from "../ui";

export const verdictLabels: Record<Verdict, { text: string; cls: string } | null> = {
  none: null,
  correct: { text: "Верно", cls: "bg-emerald-100 text-emerald-800" },
  acceptable: { text: "Допустимый вариант", cls: "bg-sky-100 text-sky-800" },
  error: { text: "Есть ошибка", cls: "bg-red-100 text-red-800" },
  insufficient_data: { text: "Не хватает условия", cls: "bg-amber-100 text-amber-900" },
};

export const levelLabels: Record<string, string> = { easier: "проще", standard: "стандартно", harder: "сложнее" };

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

function Text({ children, className = "" }: { children: string; className?: string }) {
  return <p className={`whitespace-pre-wrap break-words ${className}`}>{children}</p>;
}

export function AiNote() {
  return <p className="text-[11px] text-muted">Рекомендация ИИ — может содержать неточности. Проверьте и при необходимости исправьте перед показом ученику.</p>;
}

function AnalysisContentView({ content }: { content: WorkshopAnalysisContent }) {
  const v = verdictLabels[content.verdict];
  return (
    <div className="space-y-2">
      {v && <span className={`chip ${v.cls}`} data-testid="analysis-verdict">{v.text}</span>}
      {content.sections.map((s, i) => (
        <div key={i}>
          {s.heading && <div className="text-sm font-semibold">{s.heading}</div>}
          <Text className="text-sm text-ink/90">{s.body}</Text>
        </div>
      ))}
    </div>
  );
}

function AnalysisEditor({
  value,
  verdictAction,
  onCancel,
  onSave,
}: {
  value: WorkshopAnalysisContent;
  verdictAction: boolean;
  onCancel: () => void;
  onSave: (v: WorkshopAnalysisContent) => Promise<void>;
}) {
  const [v, setV] = useState<WorkshopAnalysisContent>(() => structuredClone(value));
  const [busy, setBusy] = useState(false);
  const setSection = (i: number, k: "heading" | "body", text: string) =>
    setV((x) => ({ ...x, sections: x.sections.map((s, j) => (j === i ? { ...s, [k]: text } : s)) }));
  return (
    <div className="space-y-2">
      {verdictAction && (
        <select className="input py-1.5 text-sm" value={v.verdict} onChange={(e) => setV({ ...v, verdict: e.target.value as Verdict })} aria-label="Вердикт">
          {(["correct", "acceptable", "error", "insufficient_data"] as Verdict[]).map((k) => (
            <option key={k} value={k}>
              {verdictLabels[k]!.text}
            </option>
          ))}
        </select>
      )}
      {v.sections.map((s, i) => (
        <div key={i} className="space-y-1 rounded-xl border border-line p-2">
          <div className="flex gap-2">
            <input className="input py-1.5 text-sm font-semibold" value={s.heading} maxLength={160} onChange={(e) => setSection(i, "heading", e.target.value)} aria-label="Заголовок раздела" />
            <button className="btn-ghost btn-sm text-red-600" onClick={() => setV((x) => ({ ...x, sections: x.sections.filter((_, j) => j !== i) }))} aria-label="Удалить раздел" title="Удалить раздел">
              ✕
            </button>
          </div>
          <textarea className="input min-h-24 text-sm" value={s.body} maxLength={6000} onChange={(e) => setSection(i, "body", e.target.value)} aria-label="Текст раздела" />
        </div>
      ))}
      <div className="flex flex-wrap gap-2">
        {v.sections.length < 10 && (
          <button className="btn-ghost btn-sm" onClick={() => setV((x) => ({ ...x, sections: [...x.sections, { heading: "", body: "" }] }))}>
            + Раздел
          </button>
        )}
        <div className="flex-1" />
        <button className="btn-secondary btn-sm" onClick={onCancel} disabled={busy}>
          Отмена
        </button>
        <button
          className="btn-primary btn-sm"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await onSave(v);
            } finally {
              setBusy(false);
            }
          }}
        >
          {busy && <Spinner className="h-3 w-3 text-white" />} Сохранить
        </button>
      </div>
    </div>
  );
}

function StatusChip({ published, dirty }: { published: boolean; dirty: boolean }) {
  if (!published) return <span className="chip bg-slate-100 text-slate-700">Видно только вам</span>;
  if (dirty) return <span className="chip bg-amber-100 text-amber-900">Ученик видит предыдущую версию</span>;
  return <span className="chip bg-emerald-100 text-emerald-800">Показано ученику</span>;
}

type Busy = "" | "publish" | "unpublish" | "delete";

export function AnalysisItem({
  lessonId,
  a,
  teacher,
  canModify,
  verdictAction,
  tz,
  onChange,
}: {
  lessonId: string;
  a: WorkshopAnalysis;
  teacher: boolean;
  /** Правка и публикация (урок не отменён) */
  canModify: boolean;
  verdictAction: boolean;
  tz: string;
  onChange: (a: WorkshopAnalysis | null) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState<Busy>("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const content = teacher ? a.draft ?? a.published : a.published;
  const dirty = !!a.published && !same(a.draft, a.published);

  const call = async (kind: Busy, fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(kind);
    try {
      await fn();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setBusy("");
    }
  };
  const path = `/api/lessons/${lessonId}/workshop/analyses/${a.id}`;

  return (
    <article className="space-y-2 rounded-2xl border border-line bg-white p-3" data-testid="analysis-item">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold">{a.actionLabel}</span>
        <span className="text-xs text-muted">· {levelLabels[a.level] ?? a.level}</span>
        {teacher && <StatusChip published={!!a.published} dirty={dirty} />}
        {!teacher && a.publishedAt && <span className="text-xs text-muted">· {formatDateTime(a.publishedAt, tz)}</span>}
      </div>
      <blockquote className="max-h-32 overflow-y-auto rounded-xl bg-canvas px-3 py-2 text-xs text-ink/80" aria-label="Разбираемый фрагмент">
        <Text>{a.fragment}</Text>
      </blockquote>
      {teacher && <AiNote />}
      {editing && a.draft ? (
        <AnalysisEditor
          value={a.draft}
          verdictAction={verdictAction}
          onCancel={() => setEditing(false)}
          onSave={async (draft) => {
            try {
              const r = await api<{ analysis: WorkshopAnalysis }>(path, { method: "PUT", json: { draft } });
              onChange(r.analysis);
              setEditing(false);
            } catch (e) {
              toast(errorMessage(e), "err");
            }
          }}
        />
      ) : (
        content && <AnalysisContentView content={content} />
      )}
      {teacher && canModify && !editing && (
        <div className="flex flex-wrap gap-2 pt-1">
          <button className="btn-secondary btn-sm" onClick={() => setEditing(true)}>
            ✏️ Редактировать
          </button>
          {(!a.published || dirty) && (
            <button
              className="btn-primary btn-sm"
              disabled={!!busy}
              data-testid="analysis-publish"
              onClick={() =>
                call("publish", async () => {
                  onChange((await api<{ analysis: WorkshopAnalysis }>(`${path}/publish`, { method: "POST" })).analysis);
                  toast("Разбор показан ученику");
                })
              }
            >
              {busy === "publish" && <Spinner className="h-3 w-3 text-white" />}
              {dirty ? "Показать ученику новую версию" : "Показать ученику"}
            </button>
          )}
          {a.published && (
            <button
              className="btn-ghost btn-sm"
              disabled={!!busy}
              onClick={() => call("unpublish", async () => onChange((await api<{ analysis: WorkshopAnalysis }>(`${path}/unpublish`, { method: "POST" })).analysis))}
            >
              Скрыть от ученика
            </button>
          )}
          <div className="flex-1" />
          <button className="btn-ghost btn-sm text-red-600" disabled={!!busy} onClick={() => setConfirmDelete(true)}>
            Удалить
          </button>
        </div>
      )}
      <ConfirmModal
        open={confirmDelete}
        title="Удалить разбор?"
        text={a.published ? "Разбор исчезнет и у ученика." : "Разбор будет удалён."}
        confirmLabel="Удалить"
        danger
        busy={busy === "delete"}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() =>
          call("delete", async () => {
            await api(path, { method: "DELETE" });
            setConfirmDelete(false);
            onChange(null);
          })
        }
      />
    </article>
  );
}

function HomeworkContentView({ content }: { content: WorkshopHomeworkContent }) {
  return (
    <div className="space-y-3">
      <div>
        <div className="text-sm font-semibold">Повторение темы</div>
        <Text className="text-sm text-ink/90">{content.review}</Text>
      </div>
      <ol className="space-y-2">
        {content.exercises.map((e, i) => (
          <li key={i} className="rounded-xl bg-canvas px-3 py-2" data-testid="hw-exercise">
            <div className="text-sm font-semibold">
              {i + 1}. {e.title}
            </div>
            <Text className="text-sm">{e.instruction}</Text>
            {e.content && <Text className="mt-1 text-sm text-ink/85">{e.content}</Text>}
          </li>
        ))}
      </ol>
    </div>
  );
}

function AnswerKeyView({ k }: { k: WorkshopAnswerKey }) {
  return (
    <div className="space-y-1 rounded-xl border border-dashed border-amber-300 bg-amber-50 px-3 py-2" data-testid="hw-answers">
      <div className="text-xs font-semibold text-amber-900">Ответы и проверка — только для вас, ученику не показываются</div>
      <ol className="list-decimal space-y-0.5 pl-5 text-sm">
        {k.answers.map((a, i) => (
          <li key={i}>
            <Text>{a}</Text>
          </li>
        ))}
      </ol>
      {k.checkingTips && <Text className="text-sm text-ink/85">{k.checkingTips}</Text>}
    </div>
  );
}

function HomeworkEditor({
  draft,
  answerKey,
  onCancel,
  onSave,
}: {
  draft: WorkshopHomeworkContent;
  answerKey: WorkshopAnswerKey;
  onCancel: () => void;
  onSave: (d: WorkshopHomeworkContent, k: WorkshopAnswerKey) => Promise<void>;
}) {
  const [d, setD] = useState(() => structuredClone(draft));
  const [k, setK] = useState(() => structuredClone(answerKey));
  const [busy, setBusy] = useState(false);
  const setEx = (i: number, f: "title" | "instruction" | "content", v: string) =>
    setD((x) => ({ ...x, exercises: x.exercises.map((e, j) => (j === i ? { ...e, [f]: v } : e)) }));
  return (
    <div className="space-y-2">
      <label className="block text-xs font-semibold text-muted">
        Повторение темы
        <textarea className="input mt-1 min-h-24 text-sm font-normal text-ink" value={d.review} maxLength={6000} onChange={(e) => setD({ ...d, review: e.target.value })} />
      </label>
      {d.exercises.map((e, i) => (
        <fieldset key={i} className="space-y-1 rounded-xl border border-line p-2">
          <legend className="px-1 text-xs font-semibold text-muted">Упражнение {i + 1}</legend>
          <input className="input py-1.5 text-sm" value={e.title} placeholder="Название" maxLength={300} onChange={(ev) => setEx(i, "title", ev.target.value)} aria-label={`Название упражнения ${i + 1}`} />
          <textarea className="input min-h-14 text-sm" value={e.instruction} placeholder="Инструкция" maxLength={3000} onChange={(ev) => setEx(i, "instruction", ev.target.value)} aria-label={`Инструкция ${i + 1}`} />
          <textarea className="input min-h-14 text-sm" value={e.content} placeholder="Задание" maxLength={3000} onChange={(ev) => setEx(i, "content", ev.target.value)} aria-label={`Задание ${i + 1}`} />
          <textarea
            className="input min-h-10 border-amber-200 bg-amber-50 text-sm"
            value={k.answers[i] ?? ""}
            placeholder="Ответ (только для вас)"
            maxLength={2000}
            onChange={(ev) => setK((x) => ({ ...x, answers: x.answers.map((a, j) => (j === i ? ev.target.value : a)) }))}
            aria-label={`Ответ ${i + 1}`}
          />
        </fieldset>
      ))}
      <label className="block text-xs font-semibold text-muted">
        Рекомендации по проверке (только для вас)
        <textarea className="input mt-1 min-h-16 border-amber-200 bg-amber-50 text-sm font-normal text-ink" value={k.checkingTips} maxLength={4000} onChange={(e) => setK({ ...k, checkingTips: e.target.value })} />
      </label>
      <div className="flex justify-end gap-2">
        <button className="btn-secondary btn-sm" onClick={onCancel} disabled={busy}>
          Отмена
        </button>
        <button
          className="btn-primary btn-sm"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await onSave(d, k);
            } finally {
              setBusy(false);
            }
          }}
        >
          {busy && <Spinner className="h-3 w-3 text-white" />} Сохранить
        </button>
      </div>
    </div>
  );
}

export function HomeworkItem({
  lessonId,
  h,
  teacher,
  canModify,
  tz,
  onChange,
}: {
  lessonId: string;
  h: WorkshopHomework;
  teacher: boolean;
  canModify: boolean;
  tz: string;
  onChange: (h: WorkshopHomework | null) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState<Busy>("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const content = teacher ? h.draft ?? h.published : h.published;
  const dirty = !!h.published && !same(h.draft, h.published);
  const path = `/api/lessons/${lessonId}/workshop/homework/${h.id}`;
  const call = async (kind: Busy, fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(kind);
    try {
      await fn();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setBusy("");
    }
  };

  return (
    <article className="space-y-2 rounded-2xl border border-line bg-white p-3" data-testid="hw-item">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold">Домашнее задание</span>
        <span className="text-xs text-muted">· {levelLabels[h.level] ?? h.level}</span>
        {teacher ? (
          <span className={`chip ${!h.published ? "bg-slate-100 text-slate-700" : dirty ? "bg-amber-100 text-amber-900" : "bg-emerald-100 text-emerald-800"}`}>
            {!h.published ? "Черновик — видно только вам" : dirty ? "Опубликована предыдущая версия" : "Опубликовано"}
          </span>
        ) : (
          h.publishedAt && <span className="text-xs text-muted">· {formatDateTime(h.publishedAt, tz)}</span>
        )}
      </div>
      {teacher && <AiNote />}
      {editing && h.draft && h.answerKey ? (
        <HomeworkEditor
          draft={h.draft}
          answerKey={h.answerKey}
          onCancel={() => setEditing(false)}
          onSave={async (draft, answerKey) => {
            try {
              onChange((await api<{ homework: WorkshopHomework }>(path, { method: "PUT", json: { draft, answerKey } })).homework);
              setEditing(false);
            } catch (e) {
              toast(errorMessage(e), "err");
            }
          }}
        />
      ) : (
        <>
          {content && <HomeworkContentView content={content} />}
          {teacher && h.answerKey && <AnswerKeyView k={h.answerKey} />}
        </>
      )}
      {teacher && canModify && !editing && (
        <div className="flex flex-wrap gap-2 pt-1">
          <button className="btn-secondary btn-sm" onClick={() => setEditing(true)}>
            ✏️ Редактировать
          </button>
          {(!h.published || dirty) && (
            <button
              className="btn-primary btn-sm"
              disabled={!!busy}
              data-testid="hw-publish"
              onClick={() =>
                call("publish", async () => {
                  onChange((await api<{ homework: WorkshopHomework }>(`${path}/publish`, { method: "POST" })).homework);
                  toast("Задание опубликовано — ученик видит его без ответов");
                })
              }
            >
              {busy === "publish" && <Spinner className="h-3 w-3 text-white" />}
              {dirty ? "Опубликовать новую версию" : "Опубликовать"}
            </button>
          )}
          {h.published && (
            <button className="btn-ghost btn-sm" disabled={!!busy} onClick={() => call("unpublish", async () => onChange((await api<{ homework: WorkshopHomework }>(`${path}/unpublish`, { method: "POST" })).homework))}>
              Снять с публикации
            </button>
          )}
          <div className="flex-1" />
          <button className="btn-ghost btn-sm text-red-600" disabled={!!busy} onClick={() => setConfirmDelete(true)}>
            Удалить
          </button>
        </div>
      )}
      <ConfirmModal
        open={confirmDelete}
        title="Удалить задание?"
        text={h.published ? "Задание исчезнет и у ученика." : "Черновик будет удалён."}
        confirmLabel="Удалить"
        danger
        busy={busy === "delete"}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() =>
          call("delete", async () => {
            await api(path, { method: "DELETE" });
            setConfirmDelete(false);
            onChange(null);
          })
        }
      />
    </article>
  );
}

/** Списки разборов и заданий с обновлением состояния после действий преподавателя. */
export function WorkshopMaterials({
  lessonId,
  state,
  setState,
  tz,
  canModify,
  emptyText,
}: {
  lessonId: string;
  state: WorkshopState;
  setState: (fn: (s: WorkshopState) => WorkshopState) => void;
  tz: string;
  canModify: boolean;
  emptyText: string;
}) {
  const teacher = state.role === "teacher";
  const verdictFor = (a: WorkshopAnalysis) => state.modes.find((m) => m.id === a.mode)?.actions.find((x) => x.id === a.action)?.verdict ?? false;
  const replace = <T extends { id: string }>(list: T[], id: string, v: T | null) => (v ? list.map((x) => (x.id === id ? v : x)) : list.filter((x) => x.id !== id));
  if (state.analyses.length === 0 && state.homework.length === 0) return <p className="text-sm text-muted">{emptyText}</p>;
  return (
    <div className="space-y-3">
      {state.homework.map((h) => (
        <HomeworkItem key={h.id} lessonId={lessonId} h={h} teacher={teacher} canModify={canModify} tz={tz} onChange={(v) => setState((s) => ({ ...s, homework: replace(s.homework, h.id, v) }))} />
      ))}
      {[...state.analyses].reverse().map((a) => (
        <AnalysisItem
          key={a.id}
          lessonId={lessonId}
          a={a}
          teacher={teacher}
          canModify={canModify}
          verdictAction={verdictFor(a)}
          tz={tz}
          onChange={(v) => setState((s) => ({ ...s, analyses: replace(s.analyses, a.id, v) }))}
        />
      ))}
    </div>
  );
}
