"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { api, ApiError, errorMessage } from "@/lib/api";
import { Spinner, toast } from "./ui";

/** Подпись бесплатного ознакомительного урока — везде одинаковая. */
export const TRIAL_LABEL = "Бесплатный ознакомительный урок";

export function TrialChip() {
  return <span className="chip bg-emerald-100 text-emerald-800">{TRIAL_LABEL}</span>;
}

/**
 * «Начать ознакомительный урок» для преподавателя: урок создаётся сразу и открывается класс.
 * Если у преподавателя уже идёт урок, сервер возвращает его — показываем предупреждение и предлагаем открыть,
 * второй урок не создаётся.
 */
export function StartTrialButton({ className = "btn-primary" }: { className?: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [conflict, setConflict] = useState<{ id: string; message: string } | null>(null);

  const start = async () => {
    if (busy) return;
    setBusy(true);
    setConflict(null);
    try {
      const r = await api<{ id: string }>("/api/lessons/trial", { method: "POST" });
      router.push(`/lessons/${r.id}/room`);
    } catch (e) {
      const id = e instanceof ApiError ? (e.body?.lessonId as string | undefined) : undefined;
      if (e instanceof ApiError && e.status === 409 && id) setConflict({ id, message: e.message });
      else toast(errorMessage(e), "err");
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <button className={className} onClick={start} disabled={busy} data-testid="start-trial">
        {busy ? <Spinner className="h-4 w-4" /> : <span aria-hidden>▶</span>} Начать ознакомительный урок
      </button>
      {conflict && (
        <div className="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900" role="alert" data-testid="trial-conflict">
          <p>{conflict.message}</p>
          <Link href={`/lessons/${conflict.id}/room`} className="btn-primary btn-sm mt-2" data-testid="trial-conflict-open">
            Открыть идущий урок
          </Link>
        </div>
      )}
    </div>
  );
}

/** Ссылка-приглашение ученика (видит только преподаватель, пока ученик не присоединился). */
export function TrialInvite({ path, compact = false }: { path: string; compact?: boolean }) {
  const url = typeof window !== "undefined" ? `${window.location.origin}${path}` : path;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      toast("Ссылка скопирована");
    } catch {
      toast("Не удалось скопировать — выделите ссылку и скопируйте вручную", "err");
    }
  };
  return (
    <div className={compact ? "flex flex-wrap items-center gap-2" : "space-y-2"} data-testid="trial-invite">
      <span className={compact ? "text-xs font-medium" : "text-sm font-medium"}>Пригласите ученика по ссылке — он войдёт под своим аккаунтом:</span>
      <div className="flex min-w-0 flex-1 gap-2">
        <input
          className="input min-w-0 flex-1 font-mono text-xs"
          readOnly
          value={url}
          onFocus={(e) => e.currentTarget.select()}
          aria-label="Ссылка-приглашение"
          data-testid="trial-invite-url"
        />
        <button className="btn-secondary btn-sm shrink-0" onClick={copy}>
          Копировать
        </button>
      </div>
    </div>
  );
}
