"use client";

import Link from "next/link";
import { use, useCallback, useEffect, useState } from "react";
import { api, errorMessage, LessonDetail } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { formatTime } from "@/lib/format";
import { lessonTiming } from "@/lib/lessonTime";
import { serverNow } from "@/lib/serverClock";
import { ClassRoom, JoinInfo } from "@/components/room/ClassRoom";
import { DeviceChoice, PreJoin } from "@/components/room/PreJoin";
import { Loading } from "@/components/ui";

function Message({ title, text, lessonId }: { title: string; text: string; lessonId: string }) {
  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <div className="card max-w-md p-6 text-center">
        <h1 className="text-lg font-semibold">{title}</h1>
        <p className="mt-2 text-sm text-muted">{text}</p>
        <Link href={`/lessons/${lessonId}`} className="btn-primary mt-5">
          К странице занятия
        </Link>
      </div>
    </div>
  );
}

export default function RoomPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { ready, config, tz } = useRequireUser(["student", "teacher"]);
  const [detail, setDetail] = useState<LessonDetail | null>(null);
  const [error, setError] = useState("");
  const [join, setJoin] = useState<JoinInfo | null>(null);
  const [devices, setDevices] = useState<DeviceChoice | null>(null);
  const [joining, setJoining] = useState(false);
  const [joinError, setJoinError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!ready) return;
    api<LessonDetail>(`/api/lessons/${id}`)
      .then(setDetail)
      .catch((e) => setError(errorMessage(e)));
  }, [ready, id]);

  const getToken = useCallback(async () => {
    const r = await api<JoinInfo>(`/api/lessons/${id}/join`, { method: "POST" });
    setJoin(r);
  }, [id]);

  const onJoin = async (c: DeviceChoice) => {
    setJoining(true);
    setJoinError("");
    try {
      await getToken();
      setDevices(c);
    } catch (e) {
      setJoinError(errorMessage(e));
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
      setDevices(null);
    }
  };

  if (!ready) return <Loading />;
  if (error) return <Message title="Не удалось открыть класс" text={error} lessonId={id} />;
  if (!detail) return <Loading label="Загружаем занятие…" />;
  const l = detail.lesson;
  if (l.status === "completed" || l.status === "cancelled")
    return <Message title={l.status === "completed" ? "Занятие завершено" : "Занятие отменено"} text="Материалы, доска и записи доступны на странице занятия." lessonId={id} />;
  if (!detail.canJoin) {
    // Опоздание не продлевает урок: после окончания обычный вход закрыт.
    if (!l.alwaysOpen && serverNow() >= Date.parse(l.endsAt))
      return (
        <Message
          title="Время занятия истекло"
          text={`Занятие закончилось в ${formatTime(l.endsAt, tz)} — вход в класс закрыт. Материалы, доска и записи доступны на странице занятия.`}
          lessonId={id}
        />
      );
    return <Message title="Класс ещё закрыт" text={`Вход откроется в ${formatTime(detail.joinOpensAt, tz)}.`} lessonId={id} />;
  }
  if (!config) return <Loading label="Загружаем настройки…" />;
  // Без LiveKit всё равно открываем класс (доска и чат работают), видео честно помечено как недоступное.
  if (!config.videoConfigured)
    return (
      <ClassRoom
        detail={detail}
        join={null}
        devices={null}
        videoProblem="сервер LiveKit не настроен (LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET — см. README)."
        onRejoin={() => {}}
      />
    );

  if (!join || !devices) {
    const other = detail.myRole === "teacher" ? detail.lesson.studentName : detail.lesson.teacherName;
    return (
      <PreJoin
        title={`${l.subject} · ${other}`}
        subtitle={`${formatTime(l.startsAt, tz)}–${formatTime(l.endsAt, tz)}${lessonTiming(l) ? ` · ${lessonTiming(l)}` : ""}`}
        onJoin={onJoin}
        joining={joining}
        joinError={joinError}
      />
    );
  }
  return <ClassRoom key={attempt} detail={detail} join={join} devices={devices} onRejoin={onRejoin} />;
}
