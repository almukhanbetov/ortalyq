"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { use, useCallback, useEffect, useState } from "react";
import { api, ApiError, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { formatTime } from "@/lib/format";
import { TRIAL_LABEL, TrialChip } from "@/components/TrialLesson";
import { ErrorBox, Loading, Spinner } from "@/components/ui";

interface InviteInfo {
  lessonId: string;
  teacherName: string;
  subject: string;
  status: string;
  startedAt: string | null;
  state: "open" | "owner" | "joined" | "taken" | "ended";
}

// Приглашение на бесплатный ознакомительный урок: /trial/<id>#<код>. Код — во фрагменте адреса (не попадает
// в журналы сервера и Referer); при входе и регистрации путь вместе с кодом передаётся в next, и ученик
// возвращается к приглашению. Присоединение — только кнопкой и только под аккаунтом ученика.
export default function TrialInvitePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const router = useRouter();
  const { user, loading, tz } = useAuth();
  const [code, setCode] = useState<string | null>(null);
  const [info, setInfo] = useState<InviteInfo | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const read = () => setCode(decodeURIComponent(window.location.hash.replace(/^#/, "")));
    read();
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, []);

  const load = useCallback(async () => {
    if (!code) return;
    setError(null);
    try {
      setInfo(await api<InviteInfo>(`/api/trial/${id}/invite`, { method: "POST", json: { code } }));
    } catch (e) {
      setError({ status: e instanceof ApiError ? e.status : 0, message: errorMessage(e) });
    }
  }, [id, code]);
  useEffect(() => {
    if (!loading) load();
  }, [load, loading, user?.id]);

  const accept = async () => {
    if (!code || busy) return;
    setBusy(true);
    setError(null);
    try {
      await api(`/api/trial/${id}/accept`, { method: "POST", json: { code } });
      router.push(`/lessons/${id}/room`);
    } catch (e) {
      setError({ status: e instanceof ApiError ? e.status : 0, message: errorMessage(e) });
      setBusy(false);
      load();
    }
  };

  const back = typeof window !== "undefined" ? `/trial/${id}${window.location.hash}` : `/trial/${id}`;
  const nextParam = encodeURIComponent(back);

  if (loading || (code && !info && !error)) return <Loading label="Открываем приглашение…" />;

  return (
    <div className="mx-auto max-w-md">
      <div className="card space-y-4 p-6 sm:p-8">
        <div>
          <TrialChip />
          <h1 className="mt-3 text-2xl font-bold">Приглашение на урок</h1>
          {info && (
            <p className="mt-2 text-sm text-muted">
              {info.teacherName} приглашает вас на {TRIAL_LABEL.toLowerCase()}
              {info.startedAt ? ` (начат в ${formatTime(info.startedAt, tz)})` : ""}. Урок проходит в виртуальном классе: видео, доска, чат, совместный код и
              учебная мастерская. Оплата не требуется.
            </p>
          )}
        </div>

        {code === "" && <ErrorBox message="В ссылке нет кода приглашения. Попросите преподавателя прислать ссылку целиком." />}
        {error && (
          <div className="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-800" role="alert" data-testid="trial-invite-error">
            {error.message}
          </div>
        )}

        {info?.state === "ended" && <p className="text-sm text-muted">Урок уже завершён — вход закрыт.</p>}
        {info?.state === "taken" && <p className="text-sm text-muted">К уроку уже присоединился другой ученик.</p>}
        {(info?.state === "owner" || info?.state === "joined") && (
          <Link href={`/lessons/${id}/room`} className="btn-primary w-full py-3" data-testid="trial-open">
            🎥 Открыть класс
          </Link>
        )}

        {info?.state === "open" &&
          (!user ? (
            <div className="space-y-2" data-testid="trial-login">
              <p className="text-sm">Войдите под своим аккаунтом ученика — после входа вы вернётесь к этому приглашению.</p>
              <Link href={`/login?next=${nextParam}`} className="btn-primary w-full py-3">
                Войти
              </Link>
              <Link href={`/register?next=${nextParam}`} className="btn-secondary w-full">
                Зарегистрироваться
              </Link>
            </div>
          ) : user.role !== "student" ? (
            <p className="text-sm text-amber-900">Вы вошли как {user.role === "teacher" ? "преподаватель" : "администратор"}. Присоединиться может только ученик — войдите под аккаунтом ученика.</p>
          ) : (
            <button className="btn-primary w-full py-3" onClick={accept} disabled={busy} data-testid="trial-accept">
              {busy && <Spinner className="h-4 w-4 text-white" />} Присоединиться к уроку
            </button>
          ))}
      </div>
    </div>
  );
}
