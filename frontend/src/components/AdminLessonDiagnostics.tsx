"use client";

// Диагностика занятия для администратора: участники, время UTC и в поясах участников, списки, вход,
// несоответствия и предлагаемые исправления. Только чтение — данные здесь не меняются.
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage } from "@/lib/api";
import { ErrorBox, Spinner } from "./ui";

interface Finding {
  code: string;
  severity: "info" | "warning" | "error";
  message: string;
  suggestion?: string;
  relatedIds?: string[];
}

interface Participant {
  role: "teacher" | "student";
  userId: string;
  name: string;
  emailMasked: string;
  accountRole: string;
  blocked: boolean;
  timezone: string;
  startsLocal: string;
  endsLocal: string;
  lists: { upcoming: boolean; history: boolean; explanation: string };
}

interface Report {
  lessonId: string;
  teacherId: string;
  studentId: string;
  subject: string;
  status: string;
  cancelReason: string;
  isDemo: boolean;
  alwaysOpen: boolean;
  startsAtUtc: string;
  endsAtUtc: string;
  createdAt: string;
  participants: Participant[];
  entry: { allowed: boolean; problem: string };
  findings: Finding[];
  serverTime: string;
  reportCode?: string;
}

const tone = { info: "bg-sky-50 text-sky-900", warning: "bg-amber-50 text-amber-900", error: "bg-red-50 text-red-800" };
// Абсолютный момент в UTC независимо от смещения в строке (сервер может прислать +05:00).
const utc = (iso: string) => new Date(iso).toISOString().replace("T", " ").slice(0, 16) + " UTC";

/** initialId — сразу выполнить диагностику этого урока (из обращения пользователя); compact — без заголовка. */
export function AdminLessonDiagnostics({ initialId = "", compact = false }: { initialId?: string; compact?: boolean }) {
  const [id, setId] = useState(initialId);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [report, setReport] = useState<Report | null>(null);

  const diagnose = useCallback(async (lessonId: string) => {
    setBusy(true);
    setError("");
    setReport(null);
    try {
      setReport((await api<{ report: Report }>(`/api/admin/diagnostics/lessons/${encodeURIComponent(lessonId.trim())}`, { method: "POST" })).report);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    if (initialId) diagnose(initialId);
  }, [initialId, diagnose]);
  const run = (e: React.FormEvent) => {
    e.preventDefault();
    diagnose(id);
  };

  return (
    <section data-testid={compact ? "admin-diagnostics-inline" : "admin-diagnostics"}>
      {!compact && (
        <>
          <h2 className="mb-1 text-lg font-semibold">Диагностика занятия</h2>
          <p className="mb-3 text-sm text-muted">Проверка по тем же правилам, что выдача «Моих занятий» и вход в класс. Данные не изменяются.</p>
        </>
      )}
      <form onSubmit={run} className={`flex flex-wrap gap-2 ${compact ? "hidden" : ""}`}>
        <input className="input max-w-md font-mono" placeholder="ID занятия (UUID)" value={id} onChange={(e) => setId(e.target.value)} aria-label="ID занятия" required />
        <button className="btn-primary" disabled={busy || !id.trim()}>
          {busy && <Spinner className="h-4 w-4 text-white" />} Проверить
        </button>
      </form>
      {error && (
        <div className="mt-3">
          <ErrorBox message={error} />
        </div>
      )}
      {report && (
        <div className="card mt-4 space-y-3 p-4 text-sm" data-testid="admin-diagnostics-report">
          <div>
            <div className="font-semibold">
              {report.subject} · <span className="font-mono text-xs">{report.lessonId}</span>
            </div>
            <div className="text-muted">
              Статус: {report.status}
              {report.cancelReason && ` (${report.cancelReason})`} · демо: {report.isDemo ? "да" : "нет"} · {utc(report.startsAtUtc)} – {utc(report.endsAtUtc).slice(11)}
            </div>
            <div className="text-xs text-muted">
              Создано {utc(report.createdAt)} · сервер сейчас {utc(report.serverTime)}
            </div>
          </div>
          {report.participants.map((p) => (
            <div key={p.role} className="rounded-xl bg-canvas p-3">
              <div className="font-medium">
                {p.role === "teacher" ? "Преподаватель" : "Ученик"}: {p.name} · {p.emailMasked}
                {p.blocked && <span className="ml-2 chip bg-red-100 text-red-700">заблокирован</span>}
              </div>
              <div className="font-mono text-xs text-muted">{p.userId}</div>
              <div className="text-xs">
                В его поясе: {p.startsLocal} – {p.endsLocal}
              </div>
              <div className="text-xs">
                «Предстоящие»: {p.lists.upcoming ? "да" : "нет"} · «История»: {p.lists.history ? "да" : "нет"} — {p.lists.explanation}
              </div>
            </div>
          ))}
          <div>Вход в класс: {report.entry.allowed ? "разрешён" : `закрыт — ${report.entry.problem}`}</div>
          {report.findings.length === 0 ? (
            <div className="text-emerald-700">Несоответствий не найдено.</div>
          ) : (
            <ul className="space-y-2">
              {report.findings.map((f) => (
                <li key={f.code} className={`rounded-xl p-3 ${tone[f.severity]}`} data-testid="admin-finding">
                  <div>
                    <b>{f.code}</b> · {f.message}
                  </div>
                  {f.suggestion && <div className="mt-1 text-xs">Предложение: {f.suggestion}</div>}
                  {!!f.relatedIds?.length && <div className="mt-1 font-mono text-xs break-all">ID: {f.relatedIds.join(", ")}</div>}
                </li>
              ))}
            </ul>
          )}
          {report.reportCode && <div className="text-xs text-muted">Зафиксировано обращение: <b>{report.reportCode}</b></div>}
        </div>
      )}
    </section>
  );
}
