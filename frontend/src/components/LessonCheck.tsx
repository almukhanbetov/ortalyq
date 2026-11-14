"use client";

// «Не вижу урок / Не могу войти» — проверка в браузере пользователя:
//   сессия и роль → собственные уроки (сервер: те же правила, что «Мои занятия» и вход в класс) →
//   загрузка списка занятий → (на странице преподавателя) свободное время для НОВОЙ записи →
//   серверное время и расхождение часов устройства.
// Временные ошибки повторяются один раз безопасно (только повторный запрос данных; страница не перезагружается,
// бронирования не меняются). По желанию пользователя сводка отправляется как обращение — с предпросмотром.
// В сводку не попадают пароли, токены, cookies, содержимое урока и тела запросов/ответов.
import Link from "next/link";
import { useState } from "react";
import { useAuth } from "@/lib/auth";
import { formatDateTime, formatRange } from "@/lib/format";
import { clockOffset, noteServerTime, SERVER_TIME_HEADER } from "@/lib/serverClock";
import { Modal, Spinner } from "./ui";

interface OwnLesson {
  id: string;
  subject: string;
  myRole: "teacher" | "student";
  otherName: string;
  startsAt: string;
  endsAt: string;
  status: string;
  cancelReason: string;
  alwaysOpen: boolean;
  isTrial?: boolean;
  list: "upcoming" | "history";
  explanation: string;
  canEnter: boolean;
  entryProblem: string;
  entryOpensAt: string;
  entryClosesAt: string;
}

interface Probe {
  name: string;
  ok: boolean;
  status: number;
  errorCode: string;
  requestId: string;
  ms: number;
}

type Outcome = "lesson_found" | "entry_not_open" | "lesson_ended" | "lesson_cancelled" | "no_lessons" | "load_failed" | "session_expired" | "server_unreachable" | "unknown";

interface Result {
  outcome: Outcome;
  role?: string;
  lessons: OwnLesson[];
  lessonCheckCode?: string;
  probes: Probe[];
  listConsistent: boolean | null;
  slots?: { intervals: number; reasons: string[] } | null;
  skewMs: number | null;
  serverTimeReceived: boolean;
  checkedAt: string;
}

const FRONTEND_VERSION = process.env.NEXT_PUBLIC_APP_VERSION || "dev";
const SKEW_WARN_MS = 2 * 60_000;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Запрос с замером: статус, X-Request-ID, длительность; серверное время учитывается в serverClock. */
async function probe(name: string, path: string, init: RequestInit = {}, seen: { server: boolean }): Promise<{ p: Probe; data: unknown }> {
  const sent = Date.now();
  try {
    const res = await fetch(path, { credentials: "include", cache: "no-store", signal: AbortSignal.timeout(12_000), ...init });
    const header = res.headers.get(SERVER_TIME_HEADER);
    if (header) seen.server = true;
    noteServerTime(header, sent, Date.now());
    let data: unknown = null;
    try {
      data = await res.json();
    } catch {
      /* не JSON */
    }
    return { p: { name, ok: res.ok, status: res.status, errorCode: res.ok ? "" : `http_${res.status}`, requestId: res.headers.get("X-Request-ID") ?? "", ms: Date.now() - sent }, data };
  } catch (e) {
    const code = (e as { name?: string })?.name === "TimeoutError" ? "timeout" : "network";
    return { p: { name, ok: false, status: 0, errorCode: code, requestId: "", ms: Date.now() - sent }, data: null };
  }
}

/** Временная ошибка (сеть, 5xx) — один безопасный повтор через 1,5 с. Обе попытки попадают в сводку. */
async function probeRetry(name: string, path: string, init: RequestInit, seen: { server: boolean }, all: Probe[]) {
  let r = await probe(name, path, init, seen);
  all.push(r.p);
  if (!r.p.ok && (r.p.status === 0 || r.p.status >= 500)) {
    await sleep(1500);
    r = await probe(name, path, init, seen);
    all.push(r.p);
  }
  return r;
}

async function runCheck(opts: { lessonId?: string; teacherId?: string }): Promise<Result> {
  const seen = { server: false };
  const probes: Probe[] = [];
  const base: Result = { outcome: "unknown", lessons: [], probes, listConsistent: null, skewMs: null, serverTimeReceived: false, checkedAt: new Date().toISOString() };
  const finish = (r: Partial<Result>): Result => ({ ...base, ...r, probes, serverTimeReceived: seen.server, skewMs: seen.server ? Math.round(clockOffset()) : null });

  // 1. Сессия и роль
  const s = await probeRetry("session", "/api/auth/me", {}, seen, probes);
  if (s.p.status === 0) return finish({ outcome: "server_unreachable" });
  if (!s.p.ok) return finish({ outcome: "load_failed" });
  const user = (s.data as { user: { role: string } | null })?.user;
  if (!user) return finish({ outcome: "session_expired" });

  // 2. Собственные уроки по серверным правилам
  const json = (b: unknown): RequestInit => ({ method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(b) });
  const lc = await probeRetry("lesson_check", "/api/me/lesson-check", json(opts.lessonId ? { lessonId: opts.lessonId } : opts.teacherId ? { teacherId: opts.teacherId } : {}), seen, probes);
  if (lc.p.status === 401) return finish({ outcome: "session_expired", role: user.role });
  if (lc.p.status === 0) return finish({ outcome: "server_unreachable", role: user.role });
  if (!lc.p.ok) return finish({ outcome: "load_failed", role: user.role });
  const check = lc.data as { lessons: OwnLesson[]; reportCode?: string };

  // 3. Список «Предстоящие» загружается и совпадает с проверкой
  const up = await probeRetry("lessons_upcoming", "/api/lessons?scope=upcoming", {}, seen, probes);
  let listConsistent: boolean | null = null;
  if (up.p.ok) {
    const ids = new Set(((up.data as { lessons: { id: string }[] })?.lessons ?? []).map((l) => l.id));
    listConsistent = check.lessons.filter((l) => l.list === "upcoming").every((l) => ids.has(l.id));
  }

  // 4. Свободное время для НОВОЙ записи (только на странице преподавателя) — отдельно от уже забронированных уроков
  let slots: Result["slots"] = null;
  if (opts.teacherId && user.role === "student") {
    const sl = await probeRetry("slots", `/api/teachers/${opts.teacherId}/slots?duration=60`, {}, seen, probes);
    if (sl.p.ok) {
      const d = sl.data as { intervals: unknown[]; emptyReasons?: string[] };
      slots = { intervals: d.intervals?.length ?? 0, reasons: d.emptyReasons ?? [] };
    }
  }

  const lessons = check.lessons ?? [];
  const main = lessons.find((l) => l.list === "upcoming") ?? lessons[0];
  let outcome: Outcome = "no_lessons";
  if (!up.p.ok) outcome = up.p.status === 0 ? "server_unreachable" : "load_failed";
  else if (main) {
    if (main.status === "cancelled") outcome = "lesson_cancelled";
    else if (main.list === "history") outcome = "lesson_ended";
    else if (!main.canEnter && Date.parse(main.entryOpensAt) > Date.now() + clockOffset()) outcome = "entry_not_open";
    else outcome = "lesson_found";
  }
  return finish({ outcome, role: user.role, lessons, lessonCheckCode: check.reportCode, listConsistent, slots });
}

function safePage() {
  return typeof location === "undefined" ? "" : location.pathname; // без параметров и фрагмента
}

function minutes(ms: number) {
  return Math.round(Math.abs(ms) / 60000);
}

function LessonState({ l, tz }: { l: OwnLesson; tz: string }) {
  let title: string;
  let tone = "border-emerald-200 bg-emerald-50";
  let actions: React.ReactNode;
  if (l.status === "cancelled") {
    title = `Урок отменён${l.cancelReason ? `: ${l.cancelReason}` : ""}`;
    tone = "border-red-200 bg-red-50";
    actions = <Link href={`/lessons/${l.id}`} className="btn-secondary btn-sm">Открыть занятие</Link>;
  } else if (l.list === "history") {
    title = "Урок завершился";
    tone = "border-slate-200 bg-slate-50";
    actions = (
      <>
        <Link href="/lessons?tab=history" className="btn-secondary btn-sm">Открыть историю</Link>
        <Link href={`/lessons/${l.id}`} className="btn-ghost btn-sm">Страница занятия</Link>
      </>
    );
  } else if (l.canEnter) {
    title = "Ваш урок найден — вход в класс открыт";
    actions = (
      <>
        <Link href={`/lessons/${l.id}/room`} className="btn-primary btn-sm">Войти в класс</Link>
        <Link href={`/lessons/${l.id}`} className="btn-secondary btn-sm">Открыть занятие</Link>
      </>
    );
  } else {
    title = l.entryProblem && !l.entryProblem.startsWith("Вход в класс откроется") ? `Ваш урок найден. ${l.entryProblem}` : `Ваш урок найден. Вход откроется ${formatDateTime(l.entryOpensAt, tz)}`;
    tone = "border-brand-200 bg-brand-50";
    actions = <Link href={`/lessons/${l.id}`} className="btn-secondary btn-sm">Открыть занятие</Link>;
  }
  return (
    <li className={`rounded-xl border p-3 ${tone}`} data-testid="device-check-lesson">
      <div className="font-semibold">{title}</div>
      <div className="text-sm">
        {l.subject} · {l.myRole === "student" ? "преподаватель" : "ученик"}: {l.otherName}
      </div>
      <div className="text-sm text-muted">{l.isTrial ? "Бесплатный ознакомительный урок" : l.alwaysOpen ? "Демо-класс, открыт всегда" : formatRange(l.startsAt, l.endsAt, tz)}</div>
      <div className="mt-2 flex flex-wrap gap-2">{actions}</div>
    </li>
  );
}

export function LessonCheckButton({ teacherId, lessonId, className = "btn-secondary btn-sm", label = "Не вижу урок / Не могу войти" }: { teacherId?: string; lessonId?: string; className?: string; label?: string }) {
  const { user, tz, config } = useAuth();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Result | null>(null);
  const [selected, setSelected] = useState("");
  const [preview, setPreview] = useState(false);
  const [send, setSend] = useState<{ state: "idle" | "sending" | "saved" | "failed" | "auth"; code?: string; cause?: string; error?: string }>({ state: "idle" });
  const [copied, setCopied] = useState(false);
  if (!user || (user.role !== "student" && user.role !== "teacher")) return null;

  const run = async () => {
    setOpen(true);
    setBusy(true);
    setResult(null);
    setPreview(false);
    setSend({ state: "idle" });
    const r = await runCheck({ lessonId, teacherId });
    setResult(r);
    setSelected(lessonId && r.lessons.some((l) => l.id === lessonId) ? lessonId : (r.lessons.find((l) => l.list === "upcoming") ?? r.lessons[0])?.id ?? "");
    setBusy(false);
  };

  // Ровно то, что будет отправлено (без паролей, токенов, cookies, содержимого урока и тел ответов).
  const summary = result && {
    frontendVersion: FRONTEND_VERSION,
    backendVersion: config?.version ?? "",
    page: safePage(),
    checkedAt: result.checkedAt,
    clockSkewMs: result.skewMs,
    serverTimeReceived: result.serverTimeReceived,
    online: typeof navigator === "undefined" ? true : navigator.onLine,
    probes: result.probes,
    lessonId: selected || undefined,
    teacherId: teacherId || undefined,
    outcome: result.outcome,
  };
  const browser = typeof navigator === "undefined" ? "" : navigator.userAgent;

  const submit = async () => {
    if (!summary) return;
    setSend({ state: "sending" });
    const sent = Date.now();
    try {
      const res = await fetch("/api/me/diagnostics", { method: "POST", credentials: "include", headers: { "Content-Type": "application/json" }, body: JSON.stringify(summary), signal: AbortSignal.timeout(12_000) });
      noteServerTime(res.headers.get(SERVER_TIME_HEADER), sent, Date.now());
      const data = await res.json().catch(() => null);
      if (res.status === 401) return setSend({ state: "auth" });
      if (!res.ok || !data?.code) return setSend({ state: "failed", error: data?.error ?? `Ошибка сервера ${res.status}` });
      setSend({ state: "saved", code: data.code, cause: data.cause?.text });
    } catch {
      setSend({ state: "failed", error: "Сервер недоступен — обращение НЕ отправлено" });
    }
  };

  const copy = async () => {
    const text = JSON.stringify({ ...summary, browser }, null, 2);
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch {
      setPreview(true); // копирование запрещено браузером — сводка показана, её можно выделить вручную
    }
  };

  const loginHref = `/login?next=${encodeURIComponent(safePage())}`;
  const r = result;
  const failedProbes = r?.probes.filter((p) => !p.ok) ?? [];
  const recovered = r ? failedProbes.length > 0 && failedProbes.every((f) => r.probes.some((p) => p.name === f.name && p.ok)) : false;

  return (
    <>
      <button type="button" className={className} onClick={run} disabled={busy} data-testid="lesson-check">
        {label}
      </button>
      <Modal open={open} title="Проверка: не вижу урок / не могу войти" onClose={() => setOpen(false)} footer={<button className="btn-secondary" onClick={() => setOpen(false)}>Закрыть</button>}>
        {busy && (
          <div className="flex items-center gap-2 text-muted" role="status">
            <Spinner className="h-4 w-4" /> Проверяем на этом устройстве…
          </div>
        )}
        {r && (
          <div className="space-y-3" data-testid="device-check-result" data-outcome={r.outcome}>
            {r.outcome === "session_expired" && (
              <div className="rounded-xl border border-amber-200 bg-amber-50 p-3" data-testid="device-check-session">
                <div className="font-semibold">Сессия истекла</div>
                <p className="text-sm">Войдите снова — после входа уроки будут видны.</p>
                <Link href={loginHref} className="btn-primary btn-sm mt-2">Войти снова</Link>
              </div>
            )}
            {(r.outcome === "server_unreachable" || r.outcome === "load_failed") && (
              <div className="rounded-xl border border-red-200 bg-red-50 p-3" data-testid="device-check-load">
                <div className="font-semibold">{r.outcome === "server_unreachable" ? "Нет связи с сервером" : "Не удалось загрузить занятия"}</div>
                <p className="text-sm">Это не значит, что урока нет. Проверьте интернет и повторите — бронирования не меняются.</p>
                <button className="btn-primary btn-sm mt-2" onClick={run}>Повторить</button>
              </div>
            )}
            {r.role && r.outcome !== "session_expired" && (
              <ul className="space-y-1 text-sm" data-testid="device-check-status">
                <li>✓ Вход выполнен: {r.role === "student" ? "ученик" : "преподаватель"} {user.name}</li>
                {recovered && <li>⚠ Первая загрузка не удалась, повторный запрос прошёл успешно.</li>}
                {r.listConsistent === false && <li>⚠ Список «Предстоящие» не совпал с проверкой — отправьте диагностику.</li>}
                {r.skewMs !== null && Math.abs(r.skewMs) > SKEW_WARN_MS ? (
                  <li data-testid="device-check-clock">
                    ⚠ Часы устройства отличаются от серверных на {minutes(r.skewMs)} мин. Доступ к уроку определяет сервер, а не часы устройства; чтобы время на экране
                    было верным, включите автоматическую установку времени.
                  </li>
                ) : (
                  r.serverTimeReceived && <li>✓ Часы устройства совпадают с серверными</li>
                )}
              </ul>
            )}
            {r.slots && (
              <div className="rounded-xl bg-canvas p-3 text-sm" data-testid="device-check-slots">
                <b>Новая запись к этому преподавателю:</b>{" "}
                {r.slots.intervals > 0 ? "свободное время есть." : "свободного времени для урока 60 мин сейчас нет."} Это не влияет на уже забронированные вами уроки — они
                перечислены ниже.
              </div>
            )}
            {r.lessons.length > 0 && (
              <ul className="space-y-2">
                {r.lessons.slice(0, 5).map((l) => (
                  <LessonState key={l.id} l={l} tz={tz} />
                ))}
              </ul>
            )}
            {r.outcome === "no_lessons" && (
              <div className="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm" data-testid="device-check-none">
                Среди занятий этого аккаунта подходящих не найдено. Проверьте в профиле, под каким email вы вошли.
                {r.lessonCheckCode && (
                  <>
                    {" "}
                    Номер обращения: <b>{r.lessonCheckCode}</b>
                  </>
                )}
              </div>
            )}

            {/* Отправка диагностики */}
            {r.outcome !== "session_expired" && (
              <div className="border-t border-line pt-3">
                <p className="text-sm">Проблема сохраняется? Отправьте диагностику — администратор сопоставит её с данными сервера.</p>
                {r.lessons.length > 1 && (
                  <label className="mt-2 block text-sm">
                    Урок, о котором речь:{" "}
                    <select className="input mt-1 py-1.5" value={selected} onChange={(e) => setSelected(e.target.value)} aria-label="Урок для обращения">
                      {r.lessons.map((l) => (
                        <option key={l.id} value={l.id}>
                          {l.subject}, {formatRange(l.startsAt, l.endsAt, tz)}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                {!preview && send.state === "idle" && (
                  <button className="btn-secondary btn-sm mt-2" onClick={() => setPreview(true)} data-testid="device-check-preview">
                    Отправить диагностику…
                  </button>
                )}
                {preview && send.state !== "saved" && (
                  <div className="mt-2 space-y-2" data-testid="device-check-summary">
                    <p className="text-xs text-muted">
                      Будут сохранены: время проверки и версия приложения, страница без параметров, ваш ID и выбранный урок, результаты запросов (коды и идентификаторы),
                      расхождение часов, тип и версия браузера. Не отправляются: пароли, токены, cookies, содержимое урока, аудио и видео, тела запросов и ответов.
                    </p>
                    <pre className="max-h-48 overflow-auto rounded-lg bg-canvas p-2 text-[11px]">{JSON.stringify({ userId: user.id, ...summary, browser }, null, 2)}</pre>
                    <div className="flex flex-wrap gap-2">
                      <button className="btn-primary btn-sm" onClick={submit} disabled={send.state === "sending"} data-testid="device-check-send">
                        {send.state === "sending" && <Spinner className="h-3 w-3 text-white" />} Отправить
                      </button>
                      <button className="btn-ghost btn-sm" onClick={copy} data-testid="device-check-copy">
                        {copied ? "Скопировано" : "Скопировать сводку"}
                      </button>
                    </div>
                  </div>
                )}
                {send.state === "saved" && (
                  <div className="mt-2 rounded-xl border border-emerald-200 bg-emerald-50 p-3 text-sm" data-testid="device-check-saved">
                    Обращение отправлено. Номер: <b data-testid="device-check-code">{send.code}</b>. Статус: новое — его посмотрит администратор.
                    {send.cause && <div className="mt-1 text-xs text-muted">Предварительная оценка сервера: {send.cause}</div>}
                  </div>
                )}
                {send.state === "failed" && (
                  <div className="mt-2 rounded-xl border border-red-200 bg-red-50 p-3 text-sm" role="alert" data-testid="device-check-send-failed">
                    Обращение не отправлено: {send.error}. Повторите позже или скопируйте сводку и передайте её в поддержку.
                    <div className="mt-2 flex gap-2">
                      <button className="btn-secondary btn-sm" onClick={submit}>Повторить</button>
                      <button className="btn-ghost btn-sm" onClick={copy}>{copied ? "Скопировано" : "Скопировать сводку"}</button>
                    </div>
                  </div>
                )}
                {send.state === "auth" && (
                  <div className="mt-2 rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm" role="alert">
                    Сессия истекла — обращение не отправлено. <Link href={loginHref} className="font-semibold underline">Войдите снова</Link> и повторите проверку.
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </Modal>
    </>
  );
}
