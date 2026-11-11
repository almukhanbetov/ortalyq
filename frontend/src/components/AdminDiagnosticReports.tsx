"use client";

// Обращения диагностики для администратора: данные браузера пользователя рядом с серверной оценкой,
// статусы «Новое / В работе / Решено». «Решено» — только с подтверждённой причиной (сервер это проверяет).
// Данные занятий здесь не меняются.
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { AdminLessonDiagnostics } from "./AdminLessonDiagnostics";
import { ErrorBox, Skeleton, Spinner, toast } from "./ui";

type Review = "new" | "in_progress" | "resolved";

interface Probe {
  name: string;
  ok: boolean;
  status: number;
  errorCode: string;
  requestId: string;
  ms: number;
}

interface DiagReport {
  code: string;
  kind: string;
  user: { id: string; name: string; emailMasked: string; role: string } | null;
  lesson: { id: string; subject: string; startsAt: string; status: string } | null;
  details: { cause?: { codes: string[]; established: boolean; text: string }; result?: string; checked?: string; codes?: string[] };
  client: { report?: { page: string; frontendVersion: string; clockSkewMs: number | null; serverTimeReceived: boolean; online: boolean; probes: Probe[]; outcome: string; checkedAt: string }; browser?: string };
  appVersion: string;
  reviewStatus: Review;
  confirmedCause: string;
  history: { at: string; from: string; to: string; cause?: string; note?: string }[];
  hits: number;
  createdAt: string;
  lastSeenAt: string;
}

const reviewLabels: Record<Review, string> = { new: "Новое", in_progress: "В работе", resolved: "Решено" };
const kindLabels: Record<string, string> = {
  user_device_check: "С устройства пользователя",
  user_lesson_check: "«Не найдено» при проверке",
  admin_lesson: "Серверная диагностика",
};

function ReportCard({ r, tz, onSaved }: { r: DiagReport; tz: string; onSaved: () => void }) {
  const [status, setStatus] = useState<Review>(r.reviewStatus);
  const [cause, setCause] = useState(r.confirmedCause);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [diag, setDiag] = useState(false);
  const c = r.client?.report;
  const serverCause = r.details?.cause;

  const save = async () => {
    setBusy(true);
    try {
      await api(`/api/admin/diagnostics/reports/${r.code}`, { method: "PATCH", json: { reviewStatus: status, confirmedCause: cause, note } });
      toast("Статус обращения сохранён");
      setNote("");
      onSaved();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setBusy(false);
    }
  };

  return (
    <article className="card space-y-3 p-4 text-sm" data-testid="diag-report" data-code={r.code}>
      <div className="flex flex-wrap items-center gap-2">
        <b className="font-mono">{r.code}</b>
        <span className={`chip ${r.reviewStatus === "new" ? "bg-brand-100 text-brand-800" : r.reviewStatus === "in_progress" ? "bg-amber-100 text-amber-900" : "bg-emerald-100 text-emerald-800"}`}>
          {reviewLabels[r.reviewStatus]}
        </span>
        <span className="text-xs text-muted">{kindLabels[r.kind] ?? r.kind}</span>
        <span className="text-xs text-muted">
          · {formatDateTime(r.createdAt, tz)}
          {r.hits > 1 && ` · повторов: ${r.hits}, последний ${formatDateTime(r.lastSeenAt, tz)}`}
        </span>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        <div>
          <div className="text-xs font-semibold text-muted">Пользователь</div>
          {r.user ? (
            <div>
              {r.user.name} · {r.user.emailMasked} · {r.user.role}
              <div className="font-mono text-[11px] text-muted">{r.user.id}</div>
            </div>
          ) : (
            <div className="text-muted">—</div>
          )}
          <div className="mt-2 text-xs font-semibold text-muted">Урок</div>
          {r.lesson ? (
            <div>
              {r.lesson.subject} · {r.lesson.status} · {formatDateTime(r.lesson.startsAt, tz)}
              <div className="font-mono text-[11px] text-muted">{r.lesson.id}</div>
            </div>
          ) : (
            <div className="text-muted">Урок не выбран или не найден среди уроков пользователя</div>
          )}
          <div className="mt-2 text-xs font-semibold text-muted">Оценка сервера</div>
          <div data-testid="diag-report-cause">{serverCause ? serverCause.text : r.details?.result === "not_found" ? "Причина не установлена: подходящих уроков не найдено" : "Причина не установлена"}</div>
        </div>
        <div>
          <div className="text-xs font-semibold text-muted">Данные браузера</div>
          {c ? (
            <div className="space-y-1 text-xs">
              <div>
                Версия: {r.appVersion || "—"} · страница {c.page || "—"} · {c.online ? "в сети" : "офлайн"}
              </div>
              <div>Проверено (часы устройства): {c.checkedAt ? formatDateTime(c.checkedAt, tz) : "—"}</div>
              <div>
                Время: {c.serverTimeReceived ? `расхождение часов ${c.clockSkewMs !== null ? Math.round(c.clockSkewMs / 1000) : "?"} с` : "серверное время не получено"}
              </div>
              <table className="w-full text-left">
                <thead className="text-muted">
                  <tr>
                    <th>проверка</th>
                    <th>код</th>
                    <th>ошибка</th>
                    <th>запрос</th>
                    <th>мс</th>
                  </tr>
                </thead>
                <tbody>
                  {c.probes.map((p, i) => (
                    <tr key={i} className={p.ok ? "" : "text-red-700"}>
                      <td>{p.name}</td>
                      <td>{p.status || "нет ответа"}</td>
                      <td>{p.errorCode || "—"}</td>
                      <td className="font-mono">{p.requestId || "—"}</td>
                      <td>{p.ms}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <div className="break-all text-muted">{r.client.browser}</div>
            </div>
          ) : (
            <div className="text-xs text-muted">Нет — обращение создано сервером</div>
          )}
        </div>
      </div>
      {r.lesson && (
        <div>
          <button className="btn-secondary btn-sm" onClick={() => setDiag((v) => !v)} data-testid="diag-report-server">
            {diag ? "Скрыть серверную диагностику" : "Серверная диагностика урока"}
          </button>
          {diag && (
            <div className="mt-2">
              <AdminLessonDiagnostics initialId={r.lesson.id} compact />
            </div>
          )}
        </div>
      )}
      <div className="grid gap-2 border-t border-line pt-3 md:grid-cols-[160px_1fr_1fr_auto]">
        <select className="input py-1.5" value={status} onChange={(e) => setStatus(e.target.value as Review)} aria-label="Статус обращения">
          {(Object.keys(reviewLabels) as Review[]).map((k) => (
            <option key={k} value={k}>
              {reviewLabels[k]}
            </option>
          ))}
        </select>
        <input className="input py-1.5" placeholder="Подтверждённая причина (обязательна для «Решено»)" value={cause} onChange={(e) => setCause(e.target.value)} aria-label="Подтверждённая причина" />
        <input className="input py-1.5" placeholder="Заметка" value={note} onChange={(e) => setNote(e.target.value)} aria-label="Заметка" />
        <button className="btn-primary btn-sm" onClick={save} disabled={busy} data-testid="diag-report-save">
          {busy && <Spinner className="h-3 w-3 text-white" />} Сохранить
        </button>
      </div>
      {r.history.length > 0 && (
        <ul className="text-xs text-muted">
          {r.history.map((h, i) => (
            <li key={i}>
              {formatDateTime(h.at, tz)}: {reviewLabels[h.from as Review] ?? h.from} → {reviewLabels[h.to as Review] ?? h.to}
              {h.cause && ` · причина: ${h.cause}`}
              {h.note && ` · ${h.note}`}
            </li>
          ))}
        </ul>
      )}
    </article>
  );
}

export function AdminDiagnosticReports({ tz }: { tz: string }) {
  const [filter, setFilter] = useState<Review | "">("new");
  const [items, setItems] = useState<DiagReport[] | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setError("");
    try {
      setItems((await api<{ reports: DiagReport[] }>(`/api/admin/diagnostics/reports${filter ? `?status=${filter}` : ""}`)).reports);
    } catch (e) {
      setError(errorMessage(e));
      setItems([]);
    }
  }, [filter]);
  useEffect(() => {
    load();
  }, [load]);

  return (
    <section data-testid="diag-reports">
      <h2 className="mb-1 text-lg font-semibold">Обращения «Не вижу урок / Не могу войти»</h2>
      <p className="mb-3 text-sm text-muted">Сведения с устройства пользователя и оценка сервера. «Решено» — только после подтверждения причины.</p>
      <div className="mb-3 inline-flex flex-wrap rounded-xl border border-line bg-white p-1">
        {([["new", "Новые"], ["in_progress", "В работе"], ["resolved", "Решённые"], ["", "Все"]] as const).map(([k, label]) => (
          <button key={k} onClick={() => setFilter(k)} className={`rounded-lg px-3 py-1.5 text-sm font-medium ${filter === k ? "bg-brand-600 text-white" : "text-muted hover:text-ink"}`}>
            {label}
          </button>
        ))}
      </div>
      {error && <ErrorBox message={error} onRetry={load} />}
      {items === null ? (
        <Skeleton className="h-32" />
      ) : items.length === 0 ? (
        <p className="text-sm text-muted">Обращений нет.</p>
      ) : (
        <div className="space-y-3">
          {items.map((r) => (
            <ReportCard key={r.code + r.reviewStatus} r={r} tz={tz} onSaved={load} />
          ))}
        </div>
      )}
    </section>
  );
}

