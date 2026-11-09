"use client";

// Шаблон записи для LiveKit Egress (RoomComposite с custom_base_url).
// Egress открывает эту страницу в headless Chrome и добавляет параметры ?url=…&token=…
// Страница показывает доску (или демонстрацию экрана), видео участников и воспроизводит их звук.
// Запись начинается, когда страница пишет в консоль START_RECORDING.

import { Suspense, use, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import {
  LiveKitRoom,
  RoomAudioRenderer,
  VideoTrack,
  isTrackReference,
  useConnectionState,
  useTracks,
} from "@livekit/components-react";
import { ConnectionState, Track } from "livekit-client";
import { Board } from "@/components/board";
import { isHiddenParticipant } from "@/components/room/ClassRoom";

interface State {
  elements: unknown[];
  revision: number;
  subject: string;
  teacherName: string;
  studentName: string;
}

function useBoardState(recToken: string) {
  const [state, setState] = useState<State | null>(null);
  const rev = useRef(-1);
  useEffect(() => {
    let stop = false;
    const tick = async () => {
      try {
        const r = await fetch(`/api/recorder/${recToken}/state`, { cache: "no-store" });
        if (r.ok) {
          const s = (await r.json()) as State;
          if (s.revision !== rev.current) {
            rev.current = s.revision;
            setState(s);
          }
        }
      } catch {
        /* повторим */
      }
      if (!stop) setTimeout(tick, 700);
    };
    tick();
    return () => {
      stop = true;
    };
  }, [recToken]);
  return state;
}

function Layout({ state }: { state: State | null }) {
  const conn = useConnectionState();
  const started = useRef(false);
  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { onlySubscribed: true },
  ).filter((t) => !isHiddenParticipant(t.participant));
  const screen = tracks.find((t) => t.source === Track.Source.ScreenShare && isTrackReference(t));
  const cams = tracks.filter((t) => t.source === Track.Source.Camera);

  useEffect(() => {
    if (conn === ConnectionState.Connected && state && !started.current) {
      started.current = true;
      // Сигнал для Egress: можно начинать запись
      setTimeout(() => console.log("START_RECORDING"), 500);
    }
    if (conn === ConnectionState.Disconnected && started.current) console.log("END_RECORDING");
  }, [conn, state]);

  return (
    <div className="recorder-view flex h-screen w-screen flex-col bg-[#1e1b2e] text-white" translate="no">
      <div className="flex h-9 items-center justify-between px-4 text-sm">
        <span className="font-semibold">ORTALYQ · {state?.subject}</span>
        <span className="flex items-center gap-2 text-red-300">
          <span className="rec-dot h-2 w-2 rounded-full bg-red-500" /> REC
        </span>
      </div>
      <div className="flex min-h-0 flex-1 gap-2 px-2 pb-2">
        <div className="relative min-w-0 flex-1 overflow-hidden rounded-xl bg-white">
          {screen && isTrackReference(screen) ? (
            <div className="absolute inset-0 bg-black">
              <VideoTrack trackRef={screen} className="h-full w-full object-contain" />
            </div>
          ) : state ? (
            <Board mode="view" elements={state.elements} revision={state.revision} />
          ) : null}
        </div>
        <div className="flex w-64 flex-col gap-2">
          {cams.map((t) => (
            <div key={t.participant.identity} className="relative aspect-video overflow-hidden rounded-xl bg-black">
              {isTrackReference(t) && !t.publication.isMuted ? (
                <VideoTrack trackRef={t} className="h-full w-full object-cover" />
              ) : (
                <div className="flex h-full items-center justify-center text-2xl font-semibold text-white/80">{(t.participant.name || "?").charAt(0)}</div>
              )}
              <div className="absolute bottom-1 left-1 rounded bg-black/60 px-1.5 text-[11px]">{t.participant.name}</div>
            </div>
          ))}
          {screen && state && (
            <div className="relative min-h-0 flex-1 overflow-hidden rounded-xl bg-white">
              <Board mode="view" elements={state.elements} revision={state.revision} />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function Recorder({ recToken }: { recToken: string }) {
  const sp = useSearchParams();
  const url = sp.get("url") ?? "";
  const token = sp.get("token") ?? "";
  const state = useBoardState(recToken);
  if (!url || !token) {
    return <div className="p-8 text-sm">Шаблон записи: отсутствуют параметры url и token (страницу открывает сервис Egress).</div>;
  }
  return (
    <LiveKitRoom serverUrl={url} token={token} connect audio={false} video={false} options={{ adaptiveStream: false }}>
      <Layout state={state} />
      <RoomAudioRenderer />
    </LiveKitRoom>
  );
}

export default function RecorderPage({ params }: { params: Promise<{ id: string; token: string }> }) {
  const { token } = use(params);
  return (
    <Suspense>
      <Recorder recToken={token} />
    </Suspense>
  );
}
