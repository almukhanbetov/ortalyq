"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useRef, useState } from "react";
import { api, ChatMessage, errorMessage, Homework, LessonDetail, Recording, upload } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { notifyLessonsChanged, useLessonsRefresh, useNow } from "@/lib/hooks";
import {
  formatDate,
  formatDateTime,
  formatDuration,
  formatPrice,
  formatRange,
  formatSize,
  formatTime,
  recordingLabels,
  relativeStart,
  tzLabel,
} from "@/lib/format";
import { Board, exportBoardPng } from "@/components/board";
import { openSignedLink, RecordingBadge } from "@/components/lessonShared";
import { Avatar, BackLink, ConfirmModal, DemoBadge, ErrorBox, Loading, Notice, Spinner, StatusBadge, Stars, toast } from "@/components/ui";

export default function LessonPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { ready, tz, config } = useRequireUser(["student", "teacher"]);
  const [data, setData] = useState<LessonDetail | null>(null);
  const [error, setError] = useState("");
  const [cancelOpen, setCancelOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const now = useNow(15000);

  const load = useCallback(async () => {
    try {
      setData(await api<LessonDetail>(`/api/lessons/${id}`));
      setError("");
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [id]);
  useEffect(() => {
    if (ready) load();
  }, [ready, load]);
  // Отмена второй стороной или в другой вкладке видна без перезахода
  useLessonsRefresh(() => ready && load());

  // Пока запись обрабатывается — периодически обновляем статус
  const processing = data?.recordings.some((r) => ["starting", "recording", "processing"].includes(r.status));
  useEffect(() => {
    if (!processing) return;
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [processing, load]);

  if (!ready) return <Loading />;
  if (error && !data)
    return (
      <div>
        <BackLink href="/lessons" label="Все занятия" />
        <ErrorBox message={error} onRetry={load} />
      </div>
    );
  if (!data) return <Loading />;

  const { lesson: l, myRole } = data;
  const isTeacher = myRole === "teacher";
  const opensAt = new Date(data.joinOpensAt).getTime();
  const closesAt = new Date(data.joinClosesAt).getTime();
  const canJoin = (l.status === "scheduled" || l.status === "live") && now >= opensAt && now <= closesAt;

  const cancel = async () => {
    setBusy(true);
    try {
      await api(`/api/lessons/${id}/cancel`, { method: "POST", json: {} });
      toast("Занятие отменено");
      setCancelOpen(false);
      notifyLessonsChanged(); // освобождённое время сразу появляется в открытых формах записи и списках
      load();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <BackLink href="/lessons" label="Все занятия" />
      <div className="card p-6">
        <div className="flex flex-col gap-6 md:flex-row md:items-start md:justify-between">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-2xl font-bold">{l.subject}</h1>
              <StatusBadge status={l.status} />
              {l.isDemo && <DemoBadge />}
            </div>
            <p className="mt-1 text-sm">
              {formatRange(l.startsAt, l.endsAt, tz)}
              {(l.status === "scheduled" || l.status === "live") && <span className="text-muted"> · {relativeStart(l.startsAt)}</span>}
            </p>
            <p className="text-xs text-muted">Часовой пояс: {tzLabel(tz)}</p>
            {l.note && <p className="mt-3 rounded-xl bg-canvas px-3 py-2 text-sm text-ink/80">«{l.note}»</p>}
            {l.status === "cancelled" && l.cancelReason && <p className="mt-2 text-sm text-red-700">Причина: {l.cancelReason}</p>}
          </div>
          <div className="flex flex-wrap gap-2">
            {(l.status === "scheduled" || l.status === "live") &&
              (canJoin ? (
                <Link href={`/lessons/${id}/room`} className="btn-primary px-6 py-3">
                  🎥 Войти в класс
                </Link>
              ) : (
                now < opensAt && (
                  <button className="btn-primary px-6 py-3" disabled>
                    Вход с {formatTime(data.joinOpensAt, tz)}
                  </button>
                )
              ))}
            {l.status === "scheduled" && (l.alwaysOpen || now < new Date(l.startsAt).getTime()) && (
              <button className="btn-secondary" onClick={() => setCancelOpen(true)}>
                Отменить
              </button>
            )}
          </div>
        </div>
        <div className="mt-6 grid gap-3 sm:grid-cols-3">
          <Person role="Преподаватель" name={l.teacherName} photo={l.hasTeacherPhoto ? `/api/teachers/${l.teacherId}/photo` : null} href={`/teachers/${l.teacherId}`} />
          <Person role="Ученик" name={l.studentName} />
          <div className="rounded-2xl bg-canvas p-4">
            <div className="text-xs text-muted">Стоимость</div>
            <div className="font-semibold">{formatPrice(l.price)}</div>
            <div className="text-xs text-muted">Оплата не подключена (MVP)</div>
          </div>
        </div>
        {config && !config.videoConfigured && (l.status === "scheduled" || l.status === "live") && (
          <div className="mt-4">
            <Notice tone="warn">Видеосвязь не настроена на сервере — вход в класс будет недоступен.</Notice>
          </div>
        )}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <Materials lessonId={id} items={data.materials} isTeacher={isTeacher} reload={load} storage={!!config?.storageConfigured} maxMB={config?.maxUploadMB ?? 20} />
        <Recordings lessonId={id} items={data.recordings} tz={tz} lessonStatus={l.status} />
      </div>

      <BoardSection lessonId={id} count={data.board.elementCount} revision={data.board.revision} />

      <HomeworkSection lessonId={id} items={data.homework} isTeacher={isTeacher} reload={load} tz={tz} storage={!!config?.storageConfigured} />

      <div className="grid gap-6 lg:grid-cols-2">
        <ChatHistory lessonId={id} count={data.chatCount} tz={tz} />
        <ReviewSection lessonId={id} review={data.review} isTeacher={isTeacher} completed={l.status === "completed"} reload={load} />
      </div>

      <ConfirmModal
        open={cancelOpen}
        title="Отменить занятие?"
        text="Время освободится, и другой ученик сможет на него записаться."
        confirmLabel="Отменить занятие"
        danger
        busy={busy}
        onClose={() => setCancelOpen(false)}
        onConfirm={cancel}
      />
    </div>
  );
}

function Person({ role, name, photo, href }: { role: string; name: string; photo?: string | null; href?: string }) {
  const inner = (
    <div className="flex items-center gap-3 rounded-2xl bg-canvas p-4">
      <Avatar name={name} src={photo} size={40} />
      <div className="min-w-0">
        <div className="text-xs text-muted">{role}</div>
        <div className="truncate font-medium">{name}</div>
      </div>
    </div>
  );
  return href ? (
    <Link href={href} className="hover:opacity-90">
      {inner}
    </Link>
  ) : (
    inner
  );
}

function Section({ title, children, action, id }: { title: string; children: React.ReactNode; action?: React.ReactNode; id?: string }) {
  return (
    <section id={id} className="card p-5">
      <div className="mb-4 flex items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}

const fileAccept = ".pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.png,.jpg,.jpeg,.webp,.mp3";

function Materials({
  lessonId,
  items,
  isTeacher,
  reload,
  storage,
  maxMB,
}: {
  lessonId: string;
  items: LessonDetail["materials"];
  isTeacher: boolean;
  reload: () => void;
  storage: boolean;
  maxMB: number;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    if (f.size > maxMB * 1024 * 1024) {
      toast(`Файл больше ${maxMB} МБ`, "err");
      return;
    }
    const fd = new FormData();
    fd.append("file", f);
    setUploading(true);
    try {
      await upload(`/api/lessons/${lessonId}/materials`, fd);
      toast("Материал добавлен");
      reload();
    } catch (err) {
      toast(errorMessage(err), "err");
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  const remove = async () => {
    if (!removing) return;
    try {
      await api(`/api/lessons/${lessonId}/materials/${removing}`, { method: "DELETE" });
      toast("Файл удалён");
      reload();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setRemoving(null);
    }
  };

  return (
    <Section
      title="Материалы"
      action={
        isTeacher && (
          <>
            <input ref={fileRef} type="file" className="hidden" accept={fileAccept} onChange={onFile} />
            <button className="btn-secondary btn-sm" disabled={!storage || uploading} onClick={() => fileRef.current?.click()} title={storage ? "" : "Хранилище не настроено"}>
              {uploading ? <Spinner className="h-3 w-3" /> : "+"} Добавить файл
            </button>
          </>
        )
      }
    >
      {items.length === 0 ? (
        <p className="text-sm text-muted">{isTeacher ? `Прикрепите учебные файлы (до ${maxMB} МБ): PDF, документы, презентации, изображения.` : "Преподаватель пока не добавил материалы."}</p>
      ) : (
        <ul className="divide-y divide-line">
          {items.map((m) => (
            <li key={m.id} className="flex items-center gap-3 py-2.5">
              <span className="text-xl" aria-hidden>
                📄
              </span>
              <div className="min-w-0 flex-1">
                <button className="truncate text-left text-sm font-medium text-brand-700 hover:underline" onClick={() => openSignedLink(`/api/lessons/${lessonId}/materials/${m.id}/link`)}>
                  {m.filename}
                </button>
                <div className="text-xs text-muted">{formatSize(m.size)}</div>
              </div>
              <button className="btn-ghost btn-sm" onClick={() => openSignedLink(`/api/lessons/${lessonId}/materials/${m.id}/link`, true)}>
                Скачать
              </button>
              {isTeacher && (
                <button className="btn-ghost btn-sm text-red-600" onClick={() => setRemoving(m.id)} aria-label="Удалить файл">
                  ✕
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <ConfirmModal open={!!removing} title="Удалить файл?" text="Файл будет удалён из хранилища." confirmLabel="Удалить" danger onClose={() => setRemoving(null)} onConfirm={remove} />
    </Section>
  );
}

function Recordings({ lessonId, items, tz, lessonStatus }: { lessonId: string; items: Recording[]; tz: string; lessonStatus: string }) {
  const [playing, setPlaying] = useState<{ id: string; url: string } | null>(null);
  const [loadingId, setLoadingId] = useState("");

  const play = async (r: Recording) => {
    setLoadingId(r.id);
    try {
      const res = await api<{ url: string }>(`/api/lessons/${lessonId}/recordings/${r.id}/link`);
      setPlaying({ id: r.id, url: res.url });
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setLoadingId("");
    }
  };

  return (
    <Section title="Записи урока">
      {items.length === 0 ? (
        <p className="text-sm text-muted">
          {lessonStatus === "completed" ? "Это занятие не записывалось." : "Преподаватель может включить запись в видеоклассе — с согласия обоих участников."}
        </p>
      ) : (
        <ul className="space-y-3">
          {items.map((r, i) => (
            <li key={r.id} className="rounded-2xl bg-canvas p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <div className="text-sm font-medium">Запись {i + 1}</div>
                  <div className="text-xs text-muted">
                    {formatDateTime(r.startedAt ?? r.createdAt, tz)}
                    {r.durationMs ? ` · ${formatDuration(r.durationMs)}` : ""}
                    {r.sizeBytes ? ` · ${formatSize(r.sizeBytes)}` : ""}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <RecordingBadge status={r.status} />
                  {r.status === "ready" && (
                    <>
                      <button className="btn-primary btn-sm" onClick={() => play(r)} disabled={loadingId === r.id}>
                        {loadingId === r.id ? <Spinner className="h-3 w-3 text-white" /> : "▶"} Смотреть
                      </button>
                      <button className="btn-ghost btn-sm" onClick={() => openSignedLink(`/api/lessons/${lessonId}/recordings/${r.id}/link`, true)}>
                        Скачать
                      </button>
                    </>
                  )}
                </div>
              </div>
              {r.status === "failed" && r.error && <p className="mt-2 text-xs text-red-700">{r.error}</p>}
              {r.status === "processing" && <p className="mt-2 text-xs text-muted">Файл формируется и загружается в хранилище — обычно это занимает до минуты.</p>}
              {playing?.id === r.id && (
                <video className="mt-3 w-full rounded-xl bg-black" src={playing.url} controls autoPlay playsInline />
              )}
            </li>
          ))}
        </ul>
      )}
      <p className="mt-3 text-xs text-muted">Записи доступны только участникам занятия; ссылки на просмотр действуют 10 минут.</p>
    </Section>
  );
}

function BoardSection({ lessonId, count, revision }: { lessonId: string; count: number; revision: number }) {
  const [elements, setElements] = useState<unknown[] | null>(null);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");

  const show = async () => {
    setOpen(true);
    if (elements) return;
    try {
      const r = await api<{ elements: unknown[] }>(`/api/lessons/${lessonId}/board`);
      setElements(r.elements);
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  const download = async () => {
    try {
      const els = elements ?? (await api<{ elements: unknown[] }>(`/api/lessons/${lessonId}/board`)).elements;
      const blob = await exportBoardPng(els);
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `doska-${lessonId.slice(0, 8)}.png`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 2000);
    } catch (e) {
      toast(errorMessage(e), "err");
    }
  };

  return (
    <Section
      title="Доска занятия"
      action={
        count > 0 && (
          <div className="flex gap-2">
            {!open && (
              <button className="btn-secondary btn-sm" onClick={show}>
                Открыть
              </button>
            )}
            <button className="btn-ghost btn-sm" onClick={download}>
              Скачать PNG
            </button>
          </div>
        )
      }
    >
      {count === 0 ? (
        <p className="text-sm text-muted">На доске пока ничего нет. Всё, что нарисовано в видеоклассе, сохраняется здесь.</p>
      ) : !open ? (
        <p className="text-sm text-muted">Сохранено объектов: {count}. Доска доступна для просмотра после урока.</p>
      ) : error ? (
        <ErrorBox message={error} />
      ) : !elements ? (
        <Loading />
      ) : (
        <div className="h-[420px] overflow-hidden rounded-xl border border-line">
          <Board mode="view" elements={elements} revision={revision} />
        </div>
      )}
    </Section>
  );
}

function HomeworkSection({
  lessonId,
  items,
  isTeacher,
  reload,
  tz,
  storage,
}: {
  lessonId: string;
  items: Homework[];
  isTeacher: boolean;
  reload: () => void;
  tz: string;
  storage: boolean;
}) {
  const [creating, setCreating] = useState(false);
  return (
    <Section
      id="homework"
      title="Домашнее задание"
      action={
        isTeacher &&
        !creating && (
          <button className="btn-secondary btn-sm" onClick={() => setCreating(true)}>
            + Задать
          </button>
        )
      }
    >
      {creating && <HomeworkForm lessonId={lessonId} storage={storage} onDone={() => { setCreating(false); reload(); }} onCancel={() => setCreating(false)} />}
      {items.length === 0 && !creating ? (
        <p className="text-sm text-muted">{isTeacher ? "Задайте домашнюю работу — ученик увидит её здесь и в кабинете." : "Домашних заданий нет."}</p>
      ) : (
        <div className="space-y-4">
          {items.map((h) => (
            <HomeworkItem key={h.id} lessonId={lessonId} hw={h} isTeacher={isTeacher} reload={reload} tz={tz} storage={storage} />
          ))}
        </div>
      )}
    </Section>
  );
}

function HomeworkForm({ lessonId, storage, onDone, onCancel }: { lessonId: string; storage: boolean; onDone: () => void; onCancel: () => void }) {
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");
  const [due, setDue] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const fd = new FormData();
    fd.append("title", title);
    fd.append("description", desc);
    if (due) fd.append("dueAt", new Date(due).toISOString());
    if (file) fd.append("file", file);
    try {
      await upload(`/api/lessons/${lessonId}/homework`, fd);
      toast("Задание выдано");
      onDone();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="mb-4 space-y-3 rounded-2xl border border-brand-200 bg-brand-50/50 p-4">
      <div>
        <label className="label" htmlFor="hw-title">Название</label>
        <input id="hw-title" className="input" required minLength={3} maxLength={150} value={title} onChange={(e) => setTitle(e.target.value)} />
      </div>
      <div>
        <label className="label" htmlFor="hw-desc">Описание</label>
        <textarea id="hw-desc" className="input min-h-24" maxLength={5000} value={desc} onChange={(e) => setDesc(e.target.value)} />
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div>
          <label className="label" htmlFor="hw-due">Срок сдачи</label>
          <input id="hw-due" type="datetime-local" className="input" value={due} onChange={(e) => setDue(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="hw-file">Файл (необязательно)</label>
          <input id="hw-file" type="file" accept={fileAccept} disabled={!storage} className="input py-2" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        </div>
      </div>
      {error && <ErrorBox message={error} />}
      <div className="flex gap-2">
        <button className="btn-primary" disabled={busy}>
          {busy && <Spinner className="h-4 w-4 text-white" />} Выдать задание
        </button>
        <button type="button" className="btn-ghost" onClick={onCancel}>
          Отмена
        </button>
      </div>
    </form>
  );
}

function HomeworkItem({ lessonId, hw, isTeacher, reload, tz, storage }: { lessonId: string; hw: Homework; isTeacher: boolean; reload: () => void; tz: string; storage: boolean }) {
  const sub = hw.submission;
  const [body, setBody] = useState(sub?.body ?? "");
  const [file, setFile] = useState<File | null>(null);
  const [comment, setComment] = useState(sub?.teacherComment ?? "");
  const [grade, setGrade] = useState<number | "">(sub?.grade ?? "");
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(!sub);
  const [removing, setRemoving] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const fd = new FormData();
    fd.append("body", body);
    if (file) fd.append("file", file);
    try {
      await upload(`/api/lessons/${lessonId}/homework/${hw.id}/submission`, fd);
      toast("Ответ отправлен");
      setEditing(false);
      reload();
    } catch (err) {
      toast(errorMessage(err), "err");
    } finally {
      setBusy(false);
    }
  };

  const review = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api(`/api/lessons/${lessonId}/homework/${hw.id}/review`, { method: "POST", json: { comment, grade: grade === "" ? null : grade } });
      toast("Работа отмечена проверенной");
      reload();
    } catch (err) {
      toast(errorMessage(err), "err");
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    try {
      await api(`/api/lessons/${lessonId}/homework/${hw.id}`, { method: "DELETE" });
      toast("Задание удалено");
      reload();
    } catch (e) {
      toast(errorMessage(e), "err");
    }
  };

  const status = !sub ? "Не выполнено" : sub.status === "reviewed" ? "Проверено" : "На проверке";
  const statusCls = !sub ? "bg-amber-100 text-amber-800" : sub.status === "reviewed" ? "bg-emerald-100 text-emerald-800" : "bg-sky-100 text-sky-800";

  return (
    <div className="rounded-2xl border border-line p-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <div className="font-semibold">{hw.title}</div>
          {hw.dueAt && <div className="text-xs text-muted">Срок: {formatDateTime(hw.dueAt, tz)}</div>}
        </div>
        <div className="flex items-center gap-2">
          <span className={`chip ${statusCls}`}>{status}</span>
          {isTeacher && (
            <button className="btn-ghost btn-sm text-red-600" onClick={() => setRemoving(true)} aria-label="Удалить задание">
              ✕
            </button>
          )}
        </div>
      </div>
      {hw.description && <p className="mt-2 whitespace-pre-line text-sm text-ink/85">{hw.description}</p>}
      {hw.fileName && (
        <button className="mt-2 text-sm font-medium text-brand-700 hover:underline" onClick={() => openSignedLink(`/api/lessons/${lessonId}/homework/${hw.id}/link`)}>
          📎 {hw.fileName} <span className="text-xs text-muted">{formatSize(hw.fileSize)}</span>
        </button>
      )}

      {sub && !editing && (
        <div className="mt-4 rounded-xl bg-canvas p-3">
          <div className="text-xs font-medium text-muted">Ответ ученика · {formatDateTime(sub.submittedAt, tz)}</div>
          {sub.body && <p className="mt-1 whitespace-pre-line text-sm">{sub.body}</p>}
          {sub.fileName && (
            <button className="mt-1 text-sm font-medium text-brand-700 hover:underline" onClick={() => openSignedLink(`/api/lessons/${lessonId}/homework/${hw.id}/submission/link`)}>
              📎 {sub.fileName} <span className="text-xs text-muted">{formatSize(sub.fileSize)}</span>
            </button>
          )}
          {!isTeacher && sub.status !== "reviewed" && (
            <button className="btn-ghost btn-sm mt-2" onClick={() => setEditing(true)}>
              Изменить ответ
            </button>
          )}
        </div>
      )}

      {sub?.status === "reviewed" && (
        <div className="mt-3 rounded-xl border border-emerald-200 bg-emerald-50 p-3 text-sm">
          <div className="flex items-center justify-between text-xs font-medium text-emerald-800">
            <span>Комментарий преподавателя{sub.reviewedAt ? ` · ${formatDate(sub.reviewedAt, tz)}` : ""}</span>
            {sub.grade != null && <span>Оценка: {sub.grade}/5</span>}
          </div>
          {sub.teacherComment && <p className="mt-1 whitespace-pre-line text-emerald-950">{sub.teacherComment}</p>}
        </div>
      )}

      {!isTeacher && editing && (
        <form onSubmit={submit} className="mt-4 space-y-3">
          <textarea className="input min-h-24" placeholder="Ваш ответ" maxLength={10000} value={body} onChange={(e) => setBody(e.target.value)} />
          <input type="file" accept={fileAccept} disabled={!storage} className="input py-2" onChange={(e) => setFile(e.target.files?.[0] ?? null)} aria-label="Прикрепить файл" />
          <div className="flex gap-2">
            <button className="btn-primary" disabled={busy || (!body.trim() && !file)}>
              {busy && <Spinner className="h-4 w-4 text-white" />} Отправить ответ
            </button>
            {sub && (
              <button type="button" className="btn-ghost" onClick={() => setEditing(false)}>
                Отмена
              </button>
            )}
          </div>
        </form>
      )}

      {isTeacher && sub && (
        <form onSubmit={review} className="mt-4 space-y-3 border-t border-line pt-4">
          <textarea className="input min-h-20" placeholder="Комментарий для ученика" maxLength={5000} value={comment} onChange={(e) => setComment(e.target.value)} />
          <div className="flex flex-wrap items-center gap-2">
            <select className="input w-auto" value={grade} onChange={(e) => setGrade(e.target.value === "" ? "" : Number(e.target.value))} aria-label="Оценка">
              <option value="">Без оценки</option>
              {[5, 4, 3, 2, 1].map((g) => (
                <option key={g} value={g}>
                  Оценка {g}
                </option>
              ))}
            </select>
            <button className="btn-primary" disabled={busy}>
              {busy && <Spinner className="h-4 w-4 text-white" />}
              {sub.status === "reviewed" ? "Обновить комментарий" : "Отметить проверенной"}
            </button>
          </div>
        </form>
      )}
      {isTeacher && !sub && <p className="mt-3 text-xs text-muted">Ученик ещё не отправил ответ.</p>}
      <ConfirmModal open={removing} title="Удалить задание?" text="Задание и ответ ученика будут удалены." confirmLabel="Удалить" danger onClose={() => setRemoving(false)} onConfirm={() => { setRemoving(false); remove(); }} />
    </div>
  );
}

function ChatHistory({ lessonId, count, tz }: { lessonId: string; count: number; tz: string }) {
  const [msgs, setMsgs] = useState<ChatMessage[] | null>(null);
  const [error, setError] = useState("");
  const load = async () => {
    try {
      setMsgs((await api<{ messages: ChatMessage[] }>(`/api/lessons/${lessonId}/chat`)).messages);
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  return (
    <Section
      title="Чат занятия"
      action={
        count > 0 &&
        !msgs && (
          <button className="btn-secondary btn-sm" onClick={load}>
            Показать ({count})
          </button>
        )
      }
    >
      {error && <ErrorBox message={error} />}
      {count === 0 ? (
        <p className="text-sm text-muted">Сообщений нет. Чат доступен в видеоклассе и сохраняется.</p>
      ) : !msgs ? (
        <p className="text-sm text-muted">Сохранено сообщений: {count}</p>
      ) : (
        <ul className="max-h-80 space-y-2 overflow-y-auto">
          {msgs.map((m) => (
            <li key={m.id} className="text-sm">
              <span className="font-medium">{m.userName}</span> <span className="text-xs text-muted">{formatTime(m.createdAt, tz)}</span>
              <p className="whitespace-pre-line text-ink/85">{m.body}</p>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function ReviewSection({
  lessonId,
  review,
  isTeacher,
  completed,
  reload,
}: {
  lessonId: string;
  review: LessonDetail["review"];
  isTeacher: boolean;
  completed: boolean;
  reload: () => void;
}) {
  const [rating, setRating] = useState(0);
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!rating) return;
    setBusy(true);
    try {
      await api(`/api/lessons/${lessonId}/review`, { method: "POST", json: { rating, body } });
      toast("Спасибо за отзыв!");
      reload();
    } catch (err) {
      toast(errorMessage(err), "err");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title="Отзыв">
      {review ? (
        <div>
          <Stars value={review.rating} size="text-lg" />
          {review.body && <p className="mt-1 text-sm">{review.body}</p>}
        </div>
      ) : !completed ? (
        <p className="text-sm text-muted">Оценить занятие можно после его завершения.</p>
      ) : isTeacher ? (
        <p className="text-sm text-muted">Ученик ещё не оставил отзыв.</p>
      ) : (
        <form onSubmit={submit} className="space-y-3">
          <div className="flex gap-1" role="radiogroup" aria-label="Оценка">
            {[1, 2, 3, 4, 5].map((n) => (
              <button
                key={n}
                type="button"
                role="radio"
                aria-checked={rating === n}
                aria-label={`${n} из 5`}
                onClick={() => setRating(n)}
                className={`text-3xl transition ${n <= rating ? "text-amber-500" : "text-slate-300 hover:text-amber-300"}`}
              >
                ★
              </button>
            ))}
          </div>
          <textarea className="input min-h-20" placeholder="Что понравилось, что можно улучшить?" maxLength={2000} value={body} onChange={(e) => setBody(e.target.value)} />
          <button className="btn-primary" disabled={busy || !rating}>
            {busy && <Spinner className="h-4 w-4 text-white" />} Оставить отзыв
          </button>
        </form>
      )}
    </Section>
  );
}
