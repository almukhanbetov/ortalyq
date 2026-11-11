"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { statusColors, statusLabels } from "@/lib/format";

export function Spinner({ className = "h-5 w-5" }: { className?: string }) {
  return (
    <svg className={`animate-spin text-brand-600 ${className}`} viewBox="0 0 24 24" fill="none" aria-hidden>
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeOpacity="0.2" strokeWidth="4" />
      <path d="M22 12a10 10 0 0 0-10-10" stroke="currentColor" strokeWidth="4" strokeLinecap="round" />
    </svg>
  );
}

function EyeIcon({ crossed }: { crossed: boolean }) {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z" />
      <circle cx="12" cy="12" r="3" />
      {crossed && <path d="M3 3l18 18" />}
    </svg>
  );
}

/** Поле пароля с кнопкой «показать/скрыть». По умолчанию пароль скрыт. id, name, autoComplete, required,
 *  minLength и прочие атрибуты передаются полю как есть — менеджеры паролей и проверка формы работают как раньше. */
export function PasswordInput({ className = "", ...props }: Omit<React.InputHTMLAttributes<HTMLInputElement>, "type">) {
  const [visible, setVisible] = useState(false);
  const ref = useRef<HTMLInputElement>(null);
  const toggle = () => {
    const el = ref.current;
    const focused = !!el && document.activeElement === el;
    const start = el?.selectionStart ?? null;
    const end = el?.selectionEnd ?? null;
    setVisible((v) => !v);
    // Смена type в некоторых браузерах сбрасывает курсор — возвращаем фокус и позицию курсора в поле.
    if (el && focused)
      requestAnimationFrame(() => {
        el.focus();
        if (start !== null && end !== null) el.setSelectionRange(start, end);
      });
  };
  const label = visible ? "Скрыть пароль" : "Показать пароль";
  return (
    <div className="relative">
      <input {...props} ref={ref} type={visible ? "text" : "password"} autoCapitalize="none" autoCorrect="off" spellCheck={false} className={`input pr-11 ${className}`} />
      <button
        type="button"
        onMouseDown={(e) => e.preventDefault()} // нажатие мышью не забирает фокус у поля
        onClick={toggle}
        aria-label={label}
        aria-pressed={visible}
        aria-controls={props.id}
        title={label}
        className="absolute inset-y-0 right-0 flex w-11 items-center justify-center rounded-r-xl text-muted transition hover:text-brand-700 focus-visible:-outline-offset-2 disabled:opacity-50"
        disabled={props.disabled}
        data-testid="password-toggle"
      >
        <EyeIcon crossed={visible} />
      </button>
    </div>
  );
}

export function Loading({ label = "Загрузка…" }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-3 py-16 text-sm text-muted" role="status">
      <Spinner />
      {label}
    </div>
  );
}

export function Skeleton({ className = "h-24" }: { className?: string }) {
  return <div className={`animate-pulse rounded-2xl bg-brand-100/50 ${className}`} />;
}

export function ErrorBox({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="rounded-2xl border border-red-200 bg-red-50 p-4 text-sm text-red-800" role="alert">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span>{message}</span>
        {onRetry && (
          <button className="btn-secondary btn-sm" onClick={onRetry}>
            Повторить
          </button>
        )}
      </div>
    </div>
  );
}

export function Notice({
  tone = "info",
  title,
  children,
}: {
  tone?: "info" | "warn" | "success";
  title?: string;
  children: React.ReactNode;
}) {
  const cls =
    tone === "warn"
      ? "border-amber-200 bg-amber-50 text-amber-900"
      : tone === "success"
        ? "border-emerald-200 bg-emerald-50 text-emerald-900"
        : "border-brand-200 bg-brand-50 text-brand-900";
  return (
    <div className={`rounded-2xl border p-4 text-sm ${cls}`}>
      {title && <div className="mb-1 font-semibold">{title}</div>}
      {children}
    </div>
  );
}

export function Empty({
  icon = "📭",
  title,
  text,
  action,
}: {
  icon?: string;
  title: string;
  text?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-line bg-white/60 px-6 py-12 text-center">
      <div className="mb-3 text-3xl" aria-hidden>
        {icon}
      </div>
      <div className="font-semibold">{title}</div>
      {text && <p className="mt-1 max-w-md text-sm text-muted">{text}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`chip ${statusColors[status] ?? "bg-slate-100 text-slate-700"}`}>
      {status === "live" && <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />}
      {statusLabels[status] ?? status}
    </span>
  );
}

export function DemoBadge() {
  return (
    <span className="chip bg-amber-100 text-amber-800" title="Вымышленные демонстрационные данные">
      ДЕМО
    </span>
  );
}

const avatarColors = ["from-violet-500 to-fuchsia-500", "from-indigo-500 to-violet-500", "from-purple-500 to-pink-500", "from-fuchsia-500 to-rose-400", "from-violet-600 to-indigo-400"];

export function Avatar({ name, src, size = 48 }: { name: string; src?: string | null; size?: number }) {
  const [broken, setBroken] = useState(false);
  const clean = name.replace(/\(ДЕМО\)/g, "").trim();
  const initials = clean
    .split(/\s+/)
    .slice(0, 2)
    .map((p) => p[0])
    .join("")
    .toUpperCase();
  const idx = [...clean].reduce((a, c) => a + c.charCodeAt(0), 0) % avatarColors.length;
  if (src && !broken) {
    // eslint-disable-next-line @next/next/no-img-element
    return (
      <img
        src={src}
        alt={clean}
        width={size}
        height={size}
        onError={() => setBroken(true)}
        className="shrink-0 rounded-2xl object-cover"
        style={{ width: size, height: size }}
      />
    );
  }
  return (
    <div
      className={`flex shrink-0 items-center justify-center rounded-2xl bg-gradient-to-br ${avatarColors[idx]} font-semibold text-white`}
      style={{ width: size, height: size, fontSize: size * 0.36 }}
      aria-hidden
    >
      {initials}
    </div>
  );
}

export function Stars({ value, size = "text-sm" }: { value: number; size?: string }) {
  return (
    <span className={`${size} tracking-tight text-amber-500`} aria-label={`Оценка ${value.toFixed(1)} из 5`}>
      {"★★★★★".slice(0, Math.round(value))}
      <span className="text-slate-300">{"★★★★★".slice(Math.round(value))}</span>
    </span>
  );
}

export function Modal({
  open,
  title,
  onClose,
  children,
  footer,
  dismissible = true,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
  dismissible?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && dismissible) onClose();
    };
    window.addEventListener("keydown", onKey);
    ref.current?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose, dismissible]);
  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-ink/40 p-0 backdrop-blur-sm sm:items-center sm:p-4"
      onClick={() => dismissible && onClose()}
    >
      <div
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="max-h-[92dvh] w-full overflow-y-auto rounded-t-3xl bg-white p-6 shadow-2xl outline-none sm:max-w-lg sm:rounded-3xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="mb-3 text-lg font-semibold">{title}</h2>
        <div className="text-sm text-ink/90">{children}</div>
        {footer && <div className="mt-6 flex flex-wrap justify-end gap-2">{footer}</div>}
      </div>
    </div>
  );
}

export function ConfirmModal({
  open,
  title,
  text,
  confirmLabel = "Подтвердить",
  danger,
  busy,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  text: React.ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      footer={
        <>
          <button className="btn-secondary" onClick={onClose} disabled={busy}>
            Отмена
          </button>
          <button className={danger ? "btn-danger" : "btn-primary"} onClick={onConfirm} disabled={busy}>
            {busy && <Spinner className="h-4 w-4 text-white" />}
            {confirmLabel}
          </button>
        </>
      }
    >
      {text}
    </Modal>
  );
}

export function PageHeader({ title, subtitle, action }: { title: string; subtitle?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-bold tracking-tight sm:text-3xl">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-muted">{subtitle}</p>}
      </div>
      {action}
    </div>
  );
}

export function BackLink({ href, label }: { href: string; label: string }) {
  return (
    <Link href={href} className="mb-4 inline-flex items-center gap-1 text-sm text-muted hover:text-brand-700">
      ← {label}
    </Link>
  );
}

// Простые всплывающие уведомления
type Toast = { id: number; text: string; tone: "ok" | "err" };
let pushToast: ((t: Omit<Toast, "id">) => void) | null = null;

export function toast(text: string, tone: "ok" | "err" = "ok") {
  pushToast?.({ text, tone });
}

export function Toaster() {
  const [list, setList] = useState<Toast[]>([]);
  useEffect(() => {
    pushToast = (t) => {
      const id = Date.now() + Math.random();
      setList((l) => [...l, { ...t, id }]);
      setTimeout(() => setList((l) => l.filter((x) => x.id !== id)), 4500);
    };
    return () => {
      pushToast = null;
    };
  }, []);
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-4 z-[60] flex flex-col items-center gap-2 px-4" aria-live="polite">
      {list.map((t) => (
        <div
          key={t.id}
          className={`pointer-events-auto rounded-xl px-4 py-3 text-sm font-medium shadow-lg ${
            t.tone === "ok" ? "bg-ink text-white" : "bg-red-600 text-white"
          }`}
        >
          {t.text}
        </div>
      ))}
    </div>
  );
}
