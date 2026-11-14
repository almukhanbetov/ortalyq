"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useState } from "react";
import { api, ApiError, DemoState, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useNow } from "@/lib/hooks";
import { serverNow } from "@/lib/serverClock";
import { demoTempNotice } from "@/lib/demo";
import { ClassRoom, JoinInfo } from "@/components/room/ClassRoom";
import { DeviceChoice, PreJoin } from "@/components/room/PreJoin";
import { InviteLink } from "@/components/demo/samples";
import { Loading } from "@/components/ui";

function Screen({ icon, title, children }: { icon: string; title: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <div className="card w-full max-w-md p-6 text-center" role="status">
        <div className="mb-2 text-3xl" aria-hidden>
          {icon}
        </div>
        <h1 className="text-lg font-semibold">{title}</h1>
        {children}
      </div>
    </div>
  );
}

function Ended({ demo, reason }: { demo: DemoState | null; reason: string }) {
  const { user } = useAuth();
  const text =
    reason === "expired"
      ? `${demo?.durationMinutes ?? 15} минут прошли — доступ к демонстрации закрыт.`
      : demo?.me.role === "student"
        ? "Организатор завершил демонстрацию."
        : "Демонстрация завершена.";
  return (
    <Screen icon="✅" title="Демонстрация завершена">
      <p className="mt-2 text-sm text-muted" data-testid="demo-ended">
        {text} Доска, текст, код и чат пробного урока удалены. Бронирований и оплат демонстрация не создавала.
      </p>
      <div className="mt-5 flex flex-col gap-2">
        <Link href="/demo" className="btn-primary">
          Начать новую демонстрацию
        </Link>
        <Link href="/teachers" className="btn-secondary">
          Найти преподавателя
        </Link>
        {!user && (
          <Link href="/register" className="btn-ghost">
            Зарегистрироваться
          </Link>
        )}
      </div>
    </Screen>
  );
}

function Left({ expiresAt }: { expiresAt: string }) {
  const now = useNow(1000);
  const ms = Math.max(0, Date.parse(expiresAt) - now);
  const s = Math.ceil(ms / 1000);
  return (
    <span className="font-mono tabular-nums">
      {Math.floor(s / 60)}:{String(s % 60).padStart(2, "0")}
    </span>
  );
}

export default function DemoRoomPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { config } = useAuth();
  const [demo, setDemo] = useState<DemoState | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [endReason, setEndReason] = useState("");
  const [join, setJoin] = useState<JoinInfo | null>(null);
  const [devices, setDevices] = useState<DeviceChoice | null>(null);
  const [videoProblem, setVideoProblem] = useState("");
  const [entered, setEntered] = useState(false);
  const [joining, setJoining] = useState(false);
  const [joinError, setJoinError] = useState("");
  const [attempt, setAttempt] = useState(0);

  const load = useCallback(async () => {
    try {
      const st = await api<DemoState>(`/api/demo/rooms/${id}`);
      setDemo(st);
      setError(null);
      if (st.status === "ended") setEndReason(st.endReason || "ended");
    } catch (e) {
      setError({ status: e instanceof ApiError ? e.status : 0, message: errorMessage(e) });
    }
  }, [id]);
  useEffect(() => {
    load();
  }, [load]);

  // Запасной путь: если сообщение об окончании не дошло (нет связи), после срока перепроверяем состояние на сервере.
  const expiresAt = demo?.expiresAt;
  useEffect(() => {
    if (!expiresAt || endReason) return;
    const t = setTimeout(load, Math.max(0, Date.parse(expiresAt) - serverNow()) + 3000);
    return () => clearTimeout(t);
  }, [expiresAt, endReason, load]);

  const onDemoEnded = useCallback(
    (reason: string) => {
      setEndReason(reason || "ended");
      load();
    },
    [load],
  );

  const getToken = useCallback(async () => {
    const r = await api<JoinInfo>(`/api/demo/rooms/${id}/join`, { method: "POST" });
    setJoin(r);
  }, [id]);

  const onJoin = async (c: DeviceChoice) => {
    if (!demo) return;
    setJoining(true);
    setJoinError("");
    try {
      if (demo.features.video) {
        try {
          await getToken();
          setDevices(c);
        } catch (e) {
          if (e instanceof ApiError && e.status === 410) {
            onDemoEnded("expired");
            return;
          }
          if (!(e instanceof ApiError && e.status === 503)) throw e;
          // Видеосвязь недоступна на сервере — входим без видео, остальные инструменты работают.
          setVideoProblem(e.message);
        }
      } else {
        setVideoProblem(demo.features.videoProblem);
      }
      setEntered(true);
    } catch (e) {
      setJoinError(e instanceof ApiError && e.status === 0 ? "Нет соединения с сервером. Проверьте интернет и попробуйте снова." : errorMessage(e));
    } finally {
      setJoining(false);
    }
  };

  const onRejoin = async () => {
    try {
      await getToken();
      setAttempt((a) => a + 1);
    } catch (e) {
      setJoinError(errorMessage(e));
      setEntered(false);
      setDevices(null);
    }
  };

  if (error && !demo) {
    if (error.status === 404)
      return (
        <Screen icon="🔍" title="Демонстрация не найдена">
          <p className="mt-2 text-sm text-muted" data-testid="demo-not-found">
            {error.message}. Демонстрация открывается только в том браузере, где её начали или приняли приглашение. Если вас пригласили — откройте ссылку-приглашение ещё раз.
          </p>
          <Link href="/demo" className="btn-primary mt-5 w-full">
            Начать новую демонстрацию
          </Link>
        </Screen>
      );
    return (
      <Screen icon="⚠️" title="Не удалось открыть демонстрацию">
        <p className="mt-2 text-sm text-muted" role="alert">
          {error.message}
        </p>
        <div className="mt-5 flex flex-col gap-2">
          <button className="btn-primary" onClick={load}>
            Повторить
          </button>
          <Link href="/demo" className="btn-secondary">
            К описанию пробного урока
          </Link>
        </div>
      </Screen>
    );
  }
  if (!demo) return <Loading label="Открываем пробный урок…" />;
  if (endReason || demo.status === "ended") return <Ended demo={demo} reason={endReason || demo.endReason} />;

  if (!entered) {
    const f = demo.features;
    return (
      <PreJoin
        title="Пробный урок"
        subtitle={`Вы — ${demo.me.role === "teacher" ? "преподаватель" : "ученик"} · ${demo.mode === "solo" ? "самостоятельно" : "вдвоём"}`}
        onJoin={onJoin}
        joining={joining}
        joinError={joinError}
        joinHelp={
          joinError ? (
            <Link href="/demo" className="btn-secondary btn-sm">
              Начать новую демонстрацию
            </Link>
          ) : null
        }
        allowSkip
        joinLabel="Войти в пробный урок"
        notice={
          <div className="space-y-2 text-sm">
            <p className="text-muted">
              До конца демонстрации: <Left expiresAt={demo.expiresAt} />. Запись видео отключена.
            </p>
            <p className="rounded-xl border border-amber-200 bg-amber-50 p-3 text-amber-900" role="note" data-testid="demo-temp-notice">
              {demoTempNotice(demo.durationMinutes ?? 15)}.
            </p>
            {!f.video && <p className="rounded-xl bg-amber-50 p-3 text-amber-900">{f.videoProblem}.</p>}
            {demo.invite?.path && (
              <div className="rounded-xl border border-brand-200 bg-brand-50 p-3" data-testid="demo-invite">
                <InviteLink path={demo.invite.path} expiresAt={demo.expiresAt} />
              </div>
            )}
          </div>
        }
      />
    );
  }

  if (!config) return <Loading label="Загружаем настройки…" />;
  return (
    <ClassRoom
      key={attempt}
      demo={demo}
      join={devices ? join : null}
      devices={devices}
      videoProblem={videoProblem || undefined}
      onRejoin={onRejoin}
      onDemoEnded={onDemoEnded}
      onDemoChanged={load}
    />
  );
}
