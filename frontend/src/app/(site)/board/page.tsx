"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage, Listing } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useSubjects } from "@/lib/hooks";
import { formatDate, formatPrice } from "@/lib/format";
import { ConfirmModal, DemoBadge, Empty, ErrorBox, Modal, PageHeader, Skeleton, Spinner, toast } from "@/components/ui";

type Tab = "all" | "mine";

export default function BoardPage() {
  const { user, tz } = useAuth();
  const subjects = useSubjects();
  const [tab, setTab] = useState<Tab>("all");
  const [kind, setKind] = useState("");
  const [subject, setSubject] = useState("");
  const [items, setItems] = useState<Listing[] | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<Listing | "new" | null>(null);
  const [deleting, setDeleting] = useState<Listing | null>(null);
  const [hiding, setHiding] = useState<Listing | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setItems(null);
    setError("");
    const sp = new URLSearchParams();
    if (kind) sp.set("kind", kind);
    if (subject) sp.set("subject", subject);
    try {
      const url = tab === "mine" ? "/api/my/listings" : `/api/listings?${sp}`;
      const r = await api<{ listings: Listing[] }>(url);
      setItems(
        tab === "mine" ? r.listings.filter((l) => (!kind || l.kind === kind) && (!subject || l.subject === subject)) : r.listings,
      );
    } catch (e) {
      setError(errorMessage(e));
      setItems([]);
    }
  }, [tab, kind, subject]);
  useEffect(() => {
    load();
  }, [load]);

  const remove = async () => {
    if (!deleting) return;
    setBusy(true);
    try {
      await api(`/api/listings/${deleting.id}`, { method: "DELETE" });
      toast("Объявление удалено");
      setDeleting(null);
      load();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setBusy(false);
    }
  };

  const hide = async () => {
    if (!hiding) return;
    setBusy(true);
    try {
      await api(`/api/admin/listings/${hiding.id}/hide`, { method: "POST", json: { reason: "Нарушение правил площадки" } });
      toast("Объявление скрыто");
      setHiding(null);
      load();
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setBusy(false);
    }
  };

  const canPost = user && (user.role === "teacher" || user.role === "student");

  return (
    <div>
      <PageHeader
        title="Доска объявлений"
        subtitle="Преподаватели предлагают занятия, ученики ищут преподавателя"
        action={
          canPost ? (
            <button className="btn-primary" onClick={() => setEditing("new")}>
              + {user.role === "teacher" ? "Предложить занятия" : "Найти преподавателя"}
            </button>
          ) : !user ? (
            <Link href="/login?next=/board" className="btn-secondary">
              Войдите, чтобы разместить объявление
            </Link>
          ) : null
        }
      />

      <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end">
        {canPost && (
          <div className="inline-flex rounded-xl border border-line bg-white p-1">
            {(["all", "mine"] as Tab[]).map((t) => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={`rounded-lg px-4 py-2 text-sm font-medium ${tab === t ? "bg-brand-600 text-white" : "text-muted hover:text-ink"}`}
              >
                {t === "all" ? "Все" : "Мои"}
              </button>
            ))}
          </div>
        )}
        <div className="grid flex-1 grid-cols-2 gap-3 sm:max-w-lg">
          <select className="input" aria-label="Тип объявления" value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="">Все типы</option>
            <option value="offer">Предложения занятий</option>
            <option value="request">Заявки учеников</option>
          </select>
          <select className="input" aria-label="Предмет" value={subject} onChange={(e) => setSubject(e.target.value)}>
            <option value="">Все предметы</option>
            {subjects.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        </div>
      </div>

      {error && <ErrorBox message={error} onRetry={load} />}
      {items === null ? (
        <div className="grid gap-4 md:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-40" />
          ))}
        </div>
      ) : items.length === 0 && !error ? (
        <Empty
          icon="📋"
          title={tab === "mine" ? "У вас пока нет объявлений" : "Объявлений не найдено"}
          text={tab === "mine" ? "Разместите первое объявление — его увидят все пользователи." : "Измените фильтры или загляните позже."}
          action={canPost ? <button className="btn-primary" onClick={() => setEditing("new")}>Разместить объявление</button> : undefined}
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {items.map((l) => (
            <article key={l.id} className={`card flex flex-col p-5 ${l.hidden ? "opacity-70" : ""}`}>
              <div className="mb-2 flex flex-wrap items-center gap-2">
                <span className={`chip ${l.kind === "offer" ? "bg-brand-100 text-brand-800" : "bg-sky-100 text-sky-800"}`}>
                  {l.kind === "offer" ? "Предлагаю занятия" : "Ищу преподавателя"}
                </span>
                <span className="chip bg-slate-100 text-slate-700">{l.subject}</span>
                {l.isDemo && <DemoBadge />}
                {l.hidden && <span className="chip bg-red-100 text-red-700">Скрыто модератором</span>}
              </div>
              <h3 className="font-semibold">{l.title}</h3>
              <p className="mt-1 line-clamp-4 whitespace-pre-line text-sm text-ink/80">{l.body}</p>
              {l.hidden && l.hiddenReason && <p className="mt-2 text-xs text-red-700">Причина: {l.hiddenReason}</p>}
              <div className="mt-auto flex flex-wrap items-center justify-between gap-2 pt-4 text-sm">
                <div className="text-muted">
                  {l.authorRole === "teacher" ? (
                    <Link href={`/teachers/${l.authorId}`} className="font-medium text-brand-700 hover:underline">
                      {l.authorName}
                    </Link>
                  ) : (
                    <span className="font-medium text-ink">{l.authorName}</span>
                  )}{" "}
                  · {formatDate(l.createdAt, tz)}
                </div>
                <div className="font-semibold">{formatPrice(l.price)}</div>
              </div>
              <div className="mt-3 flex flex-wrap gap-2">
                {user?.id === l.authorId && (
                  <>
                    <button className="btn-secondary btn-sm" onClick={() => setEditing(l)}>
                      Редактировать
                    </button>
                    <button className="btn-ghost btn-sm text-red-600" onClick={() => setDeleting(l)}>
                      Удалить
                    </button>
                  </>
                )}
                {user?.role === "admin" && !l.hidden && (
                  <button className="btn-ghost btn-sm text-red-600" onClick={() => setHiding(l)}>
                    Скрыть
                  </button>
                )}
                {user?.role === "student" && l.kind === "offer" && (
                  <Link href={`/teachers/${l.authorId}#book`} className="btn-primary btn-sm">
                    Записаться
                  </Link>
                )}
              </div>
            </article>
          ))}
        </div>
      )}

      {editing && (
        <ListingForm
          listing={editing === "new" ? null : editing}
          subjects={subjects}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
      <ConfirmModal
        open={!!deleting}
        title="Удалить объявление?"
        text="Объявление исчезнет с доски. Это действие нельзя отменить."
        confirmLabel="Удалить"
        danger
        busy={busy}
        onClose={() => setDeleting(null)}
        onConfirm={remove}
      />
      <ConfirmModal
        open={!!hiding}
        title="Скрыть объявление?"
        text="Объявление перестанет показываться на доске. Автор увидит пометку «Скрыто модератором». Вернуть можно в разделе «Модерация»."
        confirmLabel="Скрыть"
        danger
        busy={busy}
        onClose={() => setHiding(null)}
        onConfirm={hide}
      />
    </div>
  );
}

function ListingForm({
  listing,
  subjects,
  onClose,
  onSaved,
}: {
  listing: Listing | null;
  subjects: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { user } = useAuth();
  const [subject, setSubject] = useState(listing?.subject ?? "");
  const [title, setTitle] = useState(listing?.title ?? "");
  const [body, setBody] = useState(listing?.body ?? "");
  const [price, setPrice] = useState(listing?.price != null ? String(listing.price) : "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const json = { subject, title, body, price: price === "" ? null : Number(price) };
    try {
      if (listing) await api(`/api/listings/${listing.id}`, { method: "PUT", json });
      else await api("/api/listings", { method: "POST", json });
      toast(listing ? "Изменения сохранены" : "Объявление опубликовано");
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const isTeacher = user?.role === "teacher";
  return (
    <Modal open title={listing ? "Редактирование объявления" : isTeacher ? "Предложение занятий" : "Заявка на поиск преподавателя"} onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        <div>
          <label className="label" htmlFor="l-subject">Предмет</label>
          <input id="l-subject" className="input" list="subjects-list" required maxLength={40} value={subject} onChange={(e) => setSubject(e.target.value)} />
          <datalist id="subjects-list">
            {subjects.map((s) => (
              <option key={s} value={s} />
            ))}
          </datalist>
        </div>
        <div>
          <label className="label" htmlFor="l-title">Заголовок</label>
          <input id="l-title" className="input" required minLength={3} maxLength={120} value={title} onChange={(e) => setTitle(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="l-body">Описание</label>
          <textarea
            id="l-body"
            className="input min-h-32"
            required
            minLength={10}
            maxLength={3000}
            placeholder={isTeacher ? "Чему научите, для кого занятия, формат" : "Класс/уровень, цель, удобное время"}
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
        </div>
        <div>
          <label className="label" htmlFor="l-price">{isTeacher ? "Стоимость занятия, ₸" : "Бюджет за занятие, ₸"} (необязательно)</label>
          <input id="l-price" className="input" type="number" min={0} step={500} placeholder="5 000" inputMode="numeric" value={price} onChange={(e) => setPrice(e.target.value)} />
        </div>
        {error && <ErrorBox message={error} />}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn-secondary" onClick={onClose}>
            Отмена
          </button>
          <button className="btn-primary" disabled={busy}>
            {busy && <Spinner className="h-4 w-4 text-white" />}
            {listing ? "Сохранить" : "Опубликовать"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
