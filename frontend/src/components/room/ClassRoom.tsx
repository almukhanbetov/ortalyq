"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import type { ExcalidrawImperativeAPI } from "@excalidraw/excalidraw/types";
import {
  LiveKitRoom,
  RoomAudioRenderer,
  VideoTrack,
  isTrackReference,
  useConnectionState,
  useIsSpeaking,
  useLocalParticipant,
  useRemoteParticipants,
  useTracks,
  type TrackReferenceOrPlaceholder,
} from "@livekit/components-react";
import { ConnectionState, MediaDeviceFailure, Participant, ParticipantKind, Track } from "livekit-client";
import { api, errorMessage, LessonDetail, Recording } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { formatTime } from "@/lib/format";
import { useNow } from "@/lib/hooks";
import { lessonTiming } from "@/lib/lessonTime";
import { useLessonRealtime } from "@/lib/lessonSocket";
import { Board } from "../board";
import { CodePanel } from "../code";
import { WorkshopPanel } from "../workshop";
import { ConfirmModal, Modal, Spinner, toast } from "../ui";
import { DeviceChoice, mediaErrorText } from "./PreJoin";

export interface JoinInfo {
  token: string;
  url: string;
}

export function isHiddenParticipant(p: Participant) {
  return p.kind === ParticipantKind.EGRESS || p.identity.startsWith("EG_");
}

type Banner = { tone: "info" | "warn" | "error"; text: string; retry?: boolean } | null;

/** Всё, что относится к видеосвязи. При отсутствии LiveKit — пустые плитки и неактивные кнопки. */
interface Media {
  /** Видео доступно (LiveKit настроен и выдан токен) */
  enabled: boolean;
  connected: boolean;
  banner: Banner;
  remoteCount: number;
  videos: React.ReactNode;
  screen: React.ReactNode | null;
  screenLabel: string;
  controls: React.ReactNode;
}

export function ClassRoom({
  detail,
  join,
  devices,
  videoProblem,
  onRejoin,
}: {
  detail: LessonDetail;
  /** null — видеосвязь недоступна (videoProblem объясняет причину) */
  join: JoinInfo | null;
  devices: DeviceChoice | null;
  videoProblem?: string;
  onRejoin: () => void;
}) {
  const [ended, setEnded] = useState(false);
  const onEnded = useCallback(() => setEnded(true), []);

  if (!join || !devices) {
    return <ClassLayout detail={detail} media={noVideoMedia(detail, videoProblem ?? "Видеосвязь недоступна.")} onEnded={onEnded} />;
  }

  const serverUrl = join.url || `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}`;
  return (
    <LiveKitRoom
      serverUrl={serverUrl}
      token={join.token}
      connect={!ended}
      audio={devices.micOn ? (devices.micId ? { deviceId: devices.micId } : true) : false}
      video={devices.camOn ? (devices.camId ? { deviceId: devices.camId } : true) : false}
      options={{ adaptiveStream: true, dynacast: true }}
      onError={(e) => toast(`Ошибка видеосвязи: ${e.message}`, "err")}
      className="h-dvh"
    >
      <LiveMedia detail={detail} onRejoin={onRejoin} ended={ended}>
        {(media) => <ClassLayout detail={detail} media={media} onEnded={onEnded} />}
      </LiveMedia>
      <RoomAudioRenderer />
    </LiveKitRoom>
  );
}

// ---------- Видеосвязь недоступна ----------

function noVideoMedia(detail: LessonDetail, reason: string): Media {
  const other = detail.myRole === "teacher" ? detail.lesson.studentName : detail.lesson.teacherName;
  const tile = (label: string) => (
    <div className="flex aspect-video flex-col items-center justify-center rounded-2xl bg-ink/90 p-3 text-center text-xs text-white/75">
      <span className="mb-1 text-xl" aria-hidden>
        📵
      </span>
      {label}
      <span className="mt-0.5 text-[10px] text-white/50">видео недоступно</span>
    </div>
  );
  const disabled = (icon: string, label: string) => (
    <button className="btn rounded-full bg-canvas px-3 text-ink" disabled title={reason} aria-label={`${label}: недоступно`}>
      <span aria-hidden>{icon}</span>
      <span className="hidden sm:inline">{label}</span>
    </button>
  );
  return {
    enabled: false,
    connected: false,
    banner: { tone: "warn", text: `Видеосвязь недоступна: ${reason} Доска и чат работают.` },
    remoteCount: 0,
    videos: (
      <div className="grid grid-cols-2 gap-2 lg:grid-cols-1">
        {tile(other)}
        {tile("Вы")}
      </div>
    ),
    screen: null,
    screenLabel: "",
    controls: (
      <>
        {disabled("🎙️", "Микрофон")}
        {disabled("📷", "Камера")}
        {disabled("🖥️", "Экран")}
      </>
    ),
  };
}

// ---------- Видеосвязь через LiveKit ----------

function LiveMedia({
  detail,
  onRejoin,
  ended,
  children,
}: {
  detail: LessonDetail;
  onRejoin: () => void;
  ended: boolean;
  children: (m: Media) => React.ReactNode;
}) {
  const isTeacher = detail.myRole === "teacher";
  const otherName = isTeacher ? detail.lesson.studentName : detail.lesson.teacherName;
  const conn = useConnectionState();
  const remotes = useRemoteParticipants().filter((p) => !isHiddenParticipant(p));
  const { localParticipant, isMicrophoneEnabled, isCameraEnabled, isScreenShareEnabled, lastCameraError, lastMicrophoneError } = useLocalParticipant();
  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { onlySubscribed: false },
  ).filter((t) => !isHiddenParticipant(t.participant));
  const screen = tracks.find((t) => t.source === Track.Source.ScreenShare && isTrackReference(t));
  const cams = tracks.filter((t) => t.source === Track.Source.Camera);
  const [mediaError, setMediaError] = useState("");
  const canShare = typeof navigator !== "undefined" && !!navigator.mediaDevices && "getDisplayMedia" in navigator.mediaDevices;

  useEffect(() => {
    const err = lastCameraError ?? lastMicrophoneError;
    if (!err) return;
    const failure = MediaDeviceFailure.getFailure(err);
    const name = failure === MediaDeviceFailure.PermissionDenied ? "NotAllowedError" : failure === MediaDeviceFailure.NotFound ? "NotFoundError" : failure === MediaDeviceFailure.DeviceInUse ? "NotReadableError" : "";
    setMediaError(mediaErrorText({ name }, lastCameraError ? "камере" : "микрофону"));
  }, [lastCameraError, lastMicrophoneError]);

  const toggle = async (kind: "mic" | "cam" | "screen") => {
    try {
      if (kind === "mic") await localParticipant.setMicrophoneEnabled(!isMicrophoneEnabled);
      if (kind === "cam") await localParticipant.setCameraEnabled(!isCameraEnabled);
      if (kind === "screen") await localParticipant.setScreenShareEnabled(!isScreenShareEnabled, { audio: true, selfBrowserSurface: "include" });
      if (kind !== "screen") setMediaError("");
    } catch (e) {
      const name = (e as { name?: string })?.name;
      if (kind === "screen" && name === "NotAllowedError") return; // пользователь закрыл окно выбора
      setMediaError(kind === "screen" ? `Не удалось начать демонстрацию экрана: ${errorMessage(e)}` : mediaErrorText(e, kind === "mic" ? "микрофону" : "камере"));
    }
  };

  let banner: Banner = null;
  if (mediaError) banner = { tone: "error", text: mediaError };
  if (conn === ConnectionState.Connecting) banner = { tone: "info", text: "Подключаемся к видеосвязи…" };
  else if (conn === ConnectionState.Reconnecting || conn === ConnectionState.SignalReconnecting) banner = { tone: "warn", text: "Связь потеряна — переподключаемся…" };
  else if (conn === ConnectionState.Disconnected && !ended) banner = { tone: "error", text: "Видеосвязь разорвана.", retry: true };

  const videos = (
    <div className="grid grid-cols-2 gap-2 lg:grid-cols-1">
      {remotes.length === 0 ? (
        <div className="flex aspect-video flex-col items-center justify-center rounded-2xl bg-ink/90 p-3 text-center text-xs text-white/80">
          <Spinner className="mb-2 h-5 w-5 text-white" />
          Ожидаем {isTeacher ? "ученика" : "преподавателя"}: {otherName}
        </div>
      ) : (
        cams.filter((t) => !t.participant.isLocal).map((t) => <Tile key={t.participant.identity} trackRef={t} />)
      )}
      {cams
        .filter((t) => t.participant.isLocal)
        .map((t) => (
          <Tile key="local" trackRef={t} local />
        ))}
    </div>
  );

  const controls = (
    <>
      <CtrlButton on={isMicrophoneEnabled} onClick={() => toggle("mic")} label={isMicrophoneEnabled ? "Выключить микрофон" : "Включить микрофон"} icon={isMicrophoneEnabled ? "🎙️" : "🔇"} text="Микрофон" />
      <CtrlButton on={isCameraEnabled} onClick={() => toggle("cam")} label={isCameraEnabled ? "Выключить камеру" : "Включить камеру"} icon={isCameraEnabled ? "📷" : "🚫"} text="Камера" />
      <CtrlButton
        on={!isScreenShareEnabled}
        onClick={() => toggle("screen")}
        disabled={!canShare}
        label={!canShare ? "Демонстрация экрана недоступна на этом устройстве" : isScreenShareEnabled ? "Остановить демонстрацию" : "Показать экран"}
        icon="🖥️"
        text={isScreenShareEnabled ? "Стоп показ" : "Экран"}
      />
    </>
  );

  return (
    <>
      {children({
        enabled: true,
        connected: conn === ConnectionState.Connected,
        banner,
        remoteCount: remotes.length,
        videos,
        screen: screen && isTrackReference(screen) ? <VideoTrack trackRef={screen} className="h-full w-full object-contain" /> : null,
        screenLabel: screen ? (screen.participant.isLocal ? "вы" : screen.participant.name ?? "") : "",
        controls,
      })}
      {banner?.retry && (
        <div className="fixed bottom-24 left-1/2 z-40 -translate-x-1/2">
          <button className="btn-primary shadow-lg" onClick={onRejoin}>
            Переподключиться
          </button>
        </div>
      )}
    </>
  );
}

// ---------- Общий экран класса ----------

const tools: { type: string; icon: string; label: string; key: string }[] = [
  { type: "selection", icon: "🖱️", label: "Выбор", key: "V" },
  { type: "freedraw", icon: "✏️", label: "Карандаш", key: "P" },
  { type: "text", icon: "🔤", label: "Текст", key: "T" },
  { type: "rectangle", icon: "▭", label: "Прямоугольник", key: "R" },
  { type: "ellipse", icon: "◯", label: "Круг", key: "O" },
  { type: "arrow", icon: "➜", label: "Стрелка", key: "A" },
  { type: "line", icon: "／", label: "Линия", key: "L" },
  { type: "eraser", icon: "🧽", label: "Ластик", key: "E" },
];

function ClassLayout({ detail, media, onEnded }: { detail: LessonDetail; media: Media; onEnded: () => void }) {
  const router = useRouter();
  const { tz, config, user } = useAuth();
  const lesson = detail.lesson;
  const isTeacher = detail.myRole === "teacher";
  const otherName = isTeacher ? lesson.studentName : lesson.teacherName;
  const rt = useLessonRealtime(lesson.id, true);
  const [view, setView] = useState<"board" | "screen" | "code" | "workshop">("board");
  const [codeOpened, setCodeOpened] = useState(false); // после первого открытия редактор остаётся смонтированным
  const [workshopOpened, setWorkshopOpened] = useState(false); // то же для мастерской
  const [clearOpen, setClearOpen] = useState(false);
  const [endOpen, setEndOpen] = useState(false);
  const [recOpen, setRecOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [mobileTab, setMobileTab] = useState<"board" | "chat">("board");
  const [unread, setUnread] = useState(0);
  const boardApi = useRef<ExcalidrawImperativeAPI | null>(null);
  const [activeTool, setActiveTool] = useState("selection");

  const otherOnline = rt.presence.some((p) => p.id !== user?.id);
  const bothHere = media.enabled ? media.remoteCount > 0 : otherOnline;

  // Автопереключение на демонстрацию экрана
  const hasScreen = !!media.screen;
  useEffect(() => {
    setView((v) => (hasScreen ? "screen" : v === "screen" ? "board" : v));
  }, [hasScreen]);
  const openCode = () => {
    setCodeOpened(true);
    setView("code");
  };
  const openWorkshop = () => {
    setWorkshopOpened(true);
    setView("workshop");
  };
  const boardHidden = view === "code" || view === "workshop";

  const ended = rt.lessonStatus === "completed" || rt.lessonStatus === "cancelled";
  useEffect(() => {
    if (ended) onEnded();
  }, [ended, onEnded]);

  // Уведомления о статусе записи
  const recStatus = rt.recording?.status;
  const prevRec = useRef(recStatus);
  useEffect(() => {
    const prev = prevRec.current;
    prevRec.current = recStatus;
    if (prev === recStatus || !rt.recording) return;
    if (recStatus === "recording") toast("Запись началась");
    if (recStatus === "declined" && prev === "awaiting_consent") toast(rt.recording.error || "Запись отклонена", "err");
    if (recStatus === "failed") toast(`Ошибка записи: ${rt.recording.error}`, "err");
    if (recStatus === "processing") toast("Запись остановлена, файл обрабатывается");
  }, [recStatus, rt.recording]);

  // Непрочитанные сообщения на мобильном
  const chatLen = rt.chat.length;
  const prevChat = useRef(chatLen);
  useEffect(() => {
    if (chatLen > prevChat.current && mobileTab !== "chat") setUnread((u) => u + chatLen - prevChat.current);
    prevChat.current = chatLen;
  }, [chatLen, mobileTab]);

  const pickTool = (type: string) => {
    setView("board");
    boardApi.current?.setActiveTool({ type: type as "selection" });
    setActiveTool(type);
  };

  const call = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
      return true;
    } catch (e) {
      toast(errorMessage(e), "err");
      return false;
    } finally {
      setBusy(false);
    }
  };

  const requestRecording = async () => {
    if (await call(() => api(`/api/lessons/${lesson.id}/recordings`, { method: "POST" }))) setRecOpen(false);
  };
  const stopRecording = (r: Recording) => call(() => api(`/api/lessons/${lesson.id}/recordings/${r.id}/stop`, { method: "POST" }));
  const consent = (accept: boolean) =>
    rt.recording && call(() => api(`/api/lessons/${lesson.id}/recordings/${rt.recording!.id}/consent`, { method: "POST", json: { accept } }));
  const endLesson = async () => {
    if (await call(() => api(`/api/lessons/${lesson.id}/end`, { method: "POST" }))) router.push(`/lessons/${lesson.id}`);
  };

  const rec = rt.recording;
  const recActive = rec && ["awaiting_consent", "starting", "recording"].includes(rec.status);
  const needMyConsent = rec?.status === "awaiting_consent" && (isTeacher ? !rec.teacherConsentAt : !rec.studentConsentAt);
  const recordingDisabledReason = !config?.recordingConfigured
    ? `Запись недоступна: ${config?.recordingProblem ?? ""}`
    : !bothHere
      ? "Дождитесь второго участника — запись требует согласия обоих"
      : "";

  const banner: Banner = useMemo(() => {
    if (rt.status === "reconnecting") return { tone: "warn", text: "Нет связи с сервером доски и чата — переподключаемся…" };
    return media.banner;
  }, [rt.status, media.banner]);

  return (
    <div className="flex h-dvh flex-col bg-canvas">
      {/* Верхняя панель */}
      <header className="flex items-center gap-3 border-b border-line bg-white px-3 py-2 sm:px-4">
        <Link href={`/lessons/${lesson.id}`} className="btn-ghost btn-sm" aria-label="К странице занятия">
          ←
        </Link>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">
            Виртуальный класс · {lesson.subject} · {otherName}
          </div>
          <div className="flex items-center gap-1.5 text-xs text-muted">
            <span className={`h-2 w-2 rounded-full ${bothHere ? "bg-emerald-500" : "bg-amber-400"}`} aria-hidden />
            {lesson.alwaysOpen ? "демо-класс, открыт всегда" : `${formatTime(lesson.startsAt, tz)}–${formatTime(lesson.endsAt, tz)}`}
            <TimeLeft lesson={lesson} /> ·{" "}
            {bothHere ? "оба участника в классе" : `ожидаем: ${otherName}`}
          </div>
        </div>
        {rec?.status === "recording" && (
          <span className="chip bg-red-600 py-1 text-white" role="status">
            <span className="rec-dot h-2 w-2 rounded-full bg-white" /> Идёт запись
          </span>
        )}
        {rec?.status === "starting" && <span className="chip bg-amber-100 py-1 text-amber-800">Запуск записи…</span>}
        {rec?.status === "awaiting_consent" && <span className="chip bg-brand-100 py-1 text-brand-800">Ожидаем согласия на запись</span>}
        {rec?.status === "processing" && <span className="chip hidden bg-amber-100 py-1 text-amber-800 sm:inline-flex">Запись обрабатывается</span>}
      </header>
      {rec?.status === "recording" && <div className="h-1 bg-red-600" aria-hidden />}

      {banner && (
        <div
          className={`flex items-center justify-center gap-3 px-4 py-2 text-center text-sm ${
            banner.tone === "error" ? "bg-red-600 text-white" : banner.tone === "warn" ? "bg-amber-100 text-amber-900" : "bg-brand-50 text-brand-900"
          }`}
          role="status"
        >
          {banner.tone === "info" && <Spinner className="h-4 w-4" />}
          {banner.text}
        </div>
      )}

      {/* Основная область: доска в центре, справа видео и чат */}
      <div className="flex min-h-0 flex-1 flex-col gap-2 p-2 lg:flex-row">
        <div className="lg:hidden">{media.videos}</div>
        <div className="flex gap-1 lg:hidden">
          {(["board", "chat"] as const).map((t) => (
            <button
              key={t}
              onClick={() => {
                setMobileTab(t);
                if (t === "chat") setUnread(0);
              }}
              className={`flex-1 rounded-lg py-2 text-sm font-medium ${mobileTab === t ? "bg-brand-600 text-white" : "bg-white text-muted"}`}
            >
              {t === "board" ? (hasScreen ? "Доска / экран / код" : "Доска / код / мастерская") : `Чат${unread ? ` (${unread})` : ""}`}
            </button>
          ))}
        </div>
        <main className={`relative min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line bg-white ${mobileTab === "board" ? "flex" : "hidden lg:flex"}`}>
          <div className="flex items-center gap-1 overflow-x-auto border-b border-line px-2 py-1.5">
            <button onClick={() => setView("board")} className={`shrink-0 rounded-lg px-3 py-1.5 text-sm font-semibold ${view === "board" ? "bg-brand-600 text-white" : "text-muted"}`}>
              Доска
            </button>
            <button
              onClick={() => setView("screen")}
              disabled={!hasScreen}
              title={hasScreen ? "" : "Никто не показывает экран"}
              className={`shrink-0 rounded-lg px-3 py-1.5 text-sm font-semibold disabled:opacity-40 ${view === "screen" ? "bg-brand-600 text-white" : "text-muted"}`}
            >
              Экран{media.screenLabel ? `: ${media.screenLabel}` : ""}
            </button>
            <button
              onClick={openCode}
              className={`shrink-0 rounded-lg px-3 py-1.5 text-sm font-semibold ${view === "code" ? "bg-brand-600 text-white" : "text-muted"}`}
              data-testid="tab-code"
            >
              Код
            </button>
            <button
              onClick={openWorkshop}
              className={`shrink-0 rounded-lg px-3 py-1.5 text-sm font-semibold ${view === "workshop" ? "bg-brand-600 text-white" : "text-muted"}`}
              data-testid="tab-workshop"
              title="Учебная мастерская: общий текст, разбор фрагментов, домашнее задание"
            >
              <span className="sm:hidden">Мастерская</span>
              <span className="hidden sm:inline">Учебная мастерская</span>
            </button>
            <div className={`mx-1 h-6 w-px shrink-0 bg-line ${boardHidden ? "hidden" : ""}`} />
            <div className={`flex shrink-0 items-center gap-0.5 ${boardHidden ? "hidden" : ""}`} role="toolbar" aria-label="Инструменты доски">
              {tools.map((t) => (
                <button
                  key={t.type}
                  onClick={() => pickTool(t.type)}
                  title={`${t.label} (${t.key})`}
                  aria-pressed={activeTool === t.type}
                  className={`flex items-center gap-1 rounded-lg px-2 py-1.5 text-xs font-medium ${
                    activeTool === t.type && view === "board" ? "bg-brand-100 text-brand-800" : "text-ink/80 hover:bg-brand-50"
                  }`}
                >
                  <span aria-hidden>{t.icon}</span>
                  <span className="hidden xl:inline">{t.label}</span>
                </button>
              ))}
            </div>
            <div className="flex-1" />
            <button className={`btn-ghost btn-sm shrink-0 text-red-600 ${boardHidden ? "hidden" : ""}`} onClick={() => setClearOpen(true)}>
              🗑️ Очистить
            </button>
          </div>
          <div className="relative min-h-0 flex-1">
            <div className={view === "board" ? "absolute inset-0" : "hidden"}>
              <Board
                mode="live"
                socket={rt.socket}
                onRequestClear={() => setClearOpen(true)}
                onApi={(a) => (boardApi.current = a)}
                onToolChange={setActiveTool}
              />
            </div>
            {view === "screen" && media.screen && <div className="absolute inset-0 bg-ink">{media.screen}</div>}
            {codeOpened && (
              <div className={view === "code" ? "absolute inset-0" : "hidden"}>
                <CodePanel lessonId={lesson.id} socket={rt.socket} status={rt.status} />
              </div>
            )}
            {workshopOpened && (
              <div className={view === "workshop" ? "absolute inset-0" : "hidden"}>
                <WorkshopPanel lessonId={lesson.id} socket={rt.socket} status={rt.status} />
              </div>
            )}
          </div>
        </main>
        <aside className={`flex min-h-0 flex-col gap-2 lg:w-80 ${mobileTab === "chat" ? "flex flex-1" : "hidden lg:flex"}`}>
          <div className="hidden lg:block">{media.videos}</div>
          <Chat rt={rt} />
        </aside>
      </div>

      {/* Панель управления */}
      <footer className="flex flex-wrap items-center justify-center gap-2 border-t border-line bg-white px-2 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
        {media.controls}
        {isTeacher &&
          (recActive && rec ? (
            <button className="btn rounded-full bg-red-50 text-red-700 hover:bg-red-100" onClick={() => stopRecording(rec)} disabled={busy}>
              ⏹ {rec.status === "awaiting_consent" ? "Отменить запрос записи" : "Остановить запись"}
            </button>
          ) : (
            <button
              className="btn rounded-full bg-brand-50 text-brand-800 hover:bg-brand-100 disabled:opacity-50"
              onClick={() => setRecOpen(true)}
              disabled={!!recordingDisabledReason}
              title={recordingDisabledReason || "Начать запись"}
            >
              ⏺ Начать запись
            </button>
          ))}
        <div className="mx-1 hidden h-8 w-px bg-line sm:block" />
        {isTeacher && (
          <button className="btn-danger rounded-full" onClick={() => setEndOpen(true)}>
            Завершить занятие
          </button>
        )}
        <Link href={`/lessons/${lesson.id}`} className="btn-secondary rounded-full">
          Выйти из класса
        </Link>
      </footer>
      {isTeacher && config && !config.recordingConfigured && (
        <div className="bg-amber-50 px-4 py-1.5 text-center text-xs text-amber-900">Запись недоступна: {config.recordingProblem}</div>
      )}

      <ConfirmModal
        open={clearOpen}
        title="Очистить доску?"
        text="Все объекты на доске будут удалены у обоих участников."
        confirmLabel="Очистить"
        danger
        onClose={() => setClearOpen(false)}
        onConfirm={() => {
          if (!rt.socket?.send({ type: "board.clear" })) toast("Нет связи с сервером доски", "err");
          setClearOpen(false);
        }}
      />
      <ConfirmModal
        open={endOpen}
        title="Завершить занятие?"
        text="Класс закроется у обоих участников, запись (если идёт) будет остановлена. Доска, чат и материалы сохранятся на странице занятия."
        confirmLabel="Завершить"
        danger
        busy={busy}
        onClose={() => setEndOpen(false)}
        onConfirm={endLesson}
      />
      <ConfirmModal
        open={recOpen}
        title="Начать запись занятия?"
        busy={busy}
        confirmLabel="Согласен, запросить запись"
        onClose={() => setRecOpen(false)}
        onConfirm={requestRecording}
        text={
          <div className="space-y-2">
            <p>Запись начнётся только после согласия ученика. В запись попадут: звук и видео участников, интерактивная доска и демонстрация экрана.</p>
            <p>Нажимая кнопку, вы подтверждаете своё согласие на запись. Файл будет доступен только участникам занятия.</p>
          </div>
        }
      />
      <Modal
        open={!!needMyConsent}
        title="Запрос на запись занятия"
        onClose={() => {}}
        dismissible={false}
        footer={
          <>
            <button className="btn-secondary" disabled={busy} onClick={() => consent(false)}>
              Не согласен
            </button>
            <button className="btn-primary" disabled={busy} onClick={() => consent(true)}>
              {busy && <Spinner className="h-4 w-4 text-white" />}
              Согласен на запись
            </button>
          </>
        }
      >
        <div className="space-y-2">
          <p>{isTeacher ? "Подтвердите согласие на запись." : `${lesson.teacherName} предлагает записать занятие.`}</p>
          <p className="text-muted">
            В запись попадут звук и видео обоих участников, доска и демонстрация экрана. Запись будет доступна только вам и преподавателю на странице занятия. Пока
            идёт запись, вверху горит красный индикатор.
          </p>
        </div>
      </Modal>

      {ended && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/60 p-4 backdrop-blur-sm">
          <div className="card max-w-sm p-6 text-center">
            <div className="mb-2 text-3xl">✅</div>
            <h2 className="text-lg font-semibold">{rt.lessonStatus === "cancelled" ? "Занятие отменено" : "Занятие завершено"}</h2>
            <p className="mt-1 text-sm text-muted">Доска, чат, материалы, опубликованные разборы и задания мастерской и запись доступны на странице занятия.</p>
            <Link href={`/lessons/${lesson.id}`} className="btn-primary mt-4 w-full">
              Перейти к занятию
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}

function CtrlButton({ on, onClick, label, icon, text, disabled }: { on: boolean; onClick: () => void; label: string; icon: string; text?: string; disabled?: boolean }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      aria-pressed={!on}
      className={`btn rounded-full px-3 ${on ? "bg-canvas text-ink hover:bg-brand-50" : "bg-red-600 text-white hover:bg-red-700"} disabled:opacity-40`}
    >
      <span aria-hidden>{icon}</span>
      {text && <span className="hidden sm:inline">{text}</span>}
    </button>
  );
}

function Tile({ trackRef, local }: { trackRef: TrackReferenceOrPlaceholder; local?: boolean }) {
  const p = trackRef.participant;
  const speaking = useIsSpeaking(p);
  const hasVideo = isTrackReference(trackRef) && !trackRef.publication.isMuted;
  const micMuted = !p.isMicrophoneEnabled;
  return (
    <div className={`relative aspect-video overflow-hidden rounded-2xl bg-ink ring-2 ${speaking ? "ring-emerald-400" : "ring-transparent"}`}>
      {hasVideo && isTrackReference(trackRef) ? (
        <VideoTrack trackRef={trackRef} className={`h-full w-full object-cover ${local ? "-scale-x-100" : ""}`} />
      ) : (
        <div className="flex h-full items-center justify-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-full bg-brand-600 text-lg font-semibold text-white">
            {(p.name || "?").replace(/\(ДЕМО\)/, "").trim().charAt(0)}
          </div>
        </div>
      )}
      <div className="absolute bottom-1.5 left-1.5 flex items-center gap-1 rounded-md bg-black/55 px-1.5 py-0.5 text-[11px] text-white">
        {micMuted && <span aria-label="микрофон выключен">🔇</span>}
        {local ? "Вы" : p.name}
      </div>
    </div>
  );
}

function Chat({ rt }: { rt: ReturnType<typeof useLessonRealtime> }) {
  const { user, tz } = useAuth();
  const [text, setText] = useState("");
  const listRef = useRef<HTMLDivElement>(null);
  const online = rt.status === "open";

  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight, behavior: "smooth" });
  }, [rt.chat.length]);

  useEffect(() => {
    if (rt.lastError) {
      toast(rt.lastError, "err");
      rt.setLastError("");
    }
  }, [rt]);

  const send = (e: React.FormEvent) => {
    e.preventDefault();
    const body = text.trim();
    if (!body) return;
    if (rt.sendChat(body)) setText("");
    else toast("Нет соединения — сообщение не отправлено", "err");
  };

  return (
    <div className="flex min-h-48 flex-1 flex-col overflow-hidden rounded-2xl border border-line bg-white">
      <div className="flex items-center justify-between border-b border-line px-3 py-2">
        <span className="text-sm font-semibold">Чат занятия</span>
        <span className={`text-xs ${online ? "text-emerald-600" : "text-amber-600"}`}>{online ? "в сети" : "подключение…"}</span>
      </div>
      <div ref={listRef} className="flex-1 space-y-2 overflow-y-auto p-3">
        {rt.chat.length === 0 ? (
          <p className="py-6 text-center text-xs text-muted">Сообщений пока нет. Они сохраняются и будут доступны после урока.</p>
        ) : (
          rt.chat.map((m) => {
            const mine = m.userId === user?.id;
            return (
              <div key={m.id} className={`flex flex-col ${mine ? "items-end" : "items-start"}`}>
                <div className={`max-w-[85%] whitespace-pre-line break-words rounded-2xl px-3 py-1.5 text-sm ${mine ? "bg-brand-600 text-white" : "bg-canvas"}`}>{m.body}</div>
                <span className="mt-0.5 text-[10px] text-muted">
                  {mine ? "Вы" : m.userName} · {formatTime(m.createdAt, tz)}
                </span>
              </div>
            );
          })
        )}
      </div>
      <form onSubmit={send} className="flex gap-2 border-t border-line p-2">
        <input className="input py-2" placeholder={online ? "Сообщение…" : "Нет соединения"} maxLength={2000} value={text} onChange={(e) => setText(e.target.value)} disabled={!online} aria-label="Сообщение" />
        <button className="btn-primary px-3" disabled={!online || !text.trim()} aria-label="Отправить">
          ➤
        </button>
      </form>
    </div>
  );
}

/** «· осталось 55 минут» — до окончания урока по расписанию (опоздание урок не продлевает). Свой таймер, чтобы не перерисовывать класс. */
function TimeLeft({ lesson }: { lesson: LessonDetail["lesson"] }) {
  const now = useNow(15000);
  const t = lessonTiming(lesson, now);
  return t ? <span data-testid="class-time-left"> · {t}</span> : null;
}
