"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Spinner } from "../ui";

export interface DeviceChoice {
  camOn: boolean;
  micOn: boolean;
  camId?: string;
  micId?: string;
}

export function mediaErrorText(err: unknown, kind: "камере" | "микрофону" | "устройствам"): string {
  const name = err instanceof DOMException ? err.name : (err as { name?: string })?.name;
  switch (name) {
    case "NotAllowedError":
    case "PermissionDeniedError":
      return `Доступ к ${kind} запрещён. Разрешите его в настройках браузера (значок замка в адресной строке) и обновите страницу.`;
    case "NotFoundError":
    case "OverconstrainedError":
      return `Не найдено устройство: проверьте, что ${kind === "камере" ? "камера подключена" : kind === "микрофону" ? "микрофон подключён" : "устройства подключены"}.`;
    case "NotReadableError":
    case "TrackStartError":
      return `Устройство занято другим приложением (Zoom, Teams и т. п.). Закройте его и попробуйте снова.`;
    case "SecurityError":
      return "Браузер блокирует доступ к устройствам: откройте сайт по HTTPS.";
    default:
      return `Не удалось получить доступ к ${kind}.`;
  }
}

export function PreJoin({
  title,
  subtitle,
  onJoin,
  joining,
  joinError,
}: {
  title: string;
  subtitle: string;
  onJoin: (c: DeviceChoice) => void;
  joining: boolean;
  joinError: string;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const [cams, setCams] = useState<MediaDeviceInfo[]>([]);
  const [mics, setMics] = useState<MediaDeviceInfo[]>([]);
  const [camId, setCamId] = useState<string>();
  const [micId, setMicId] = useState<string>();
  const [camOn, setCamOn] = useState(true);
  const [micOn, setMicOn] = useState(true);
  const [camErr, setCamErr] = useState("");
  const [micErr, setMicErr] = useState("");
  const [level, setLevel] = useState(0);
  const [checking, setChecking] = useState(true);
  const insecure = typeof window !== "undefined" && (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia);

  const stopStream = () => {
    streamRef.current?.getTracks().forEach((t) => t.stop());
    streamRef.current = null;
  };

  const start = useCallback(async () => {
    if (insecure) {
      setChecking(false);
      return;
    }
    setChecking(true);
    stopStream();
    const stream = new MediaStream();
    // Камеру и микрофон запрашиваем раздельно, чтобы отказ в одном не блокировал другое.
    if (camOn) {
      try {
        const s = await navigator.mediaDevices.getUserMedia({ video: camId ? { deviceId: { exact: camId } } : { width: 1280, height: 720 } });
        s.getVideoTracks().forEach((t) => stream.addTrack(t));
        setCamErr("");
      } catch (e) {
        setCamErr(mediaErrorText(e, "камере"));
      }
    }
    if (micOn) {
      try {
        const s = await navigator.mediaDevices.getUserMedia({ audio: micId ? { deviceId: { exact: micId } } : true });
        s.getAudioTracks().forEach((t) => stream.addTrack(t));
        setMicErr("");
      } catch (e) {
        setMicErr(mediaErrorText(e, "микрофону"));
      }
    }
    streamRef.current = stream;
    if (videoRef.current) videoRef.current.srcObject = stream.getVideoTracks().length ? stream : null;
    try {
      const devices = await navigator.mediaDevices.enumerateDevices();
      setCams(devices.filter((d) => d.kind === "videoinput" && d.deviceId));
      setMics(devices.filter((d) => d.kind === "audioinput" && d.deviceId));
    } catch {
      /* ignore */
    }
    setChecking(false);
  }, [camOn, micOn, camId, micId, insecure]);

  useEffect(() => {
    start();
    return stopStream;
  }, [start]);

  // Индикатор громкости микрофона
  useEffect(() => {
    const track = streamRef.current?.getAudioTracks()[0];
    if (!track || checking) {
      setLevel(0);
      return;
    }
    const AC = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
    const ctx = new AC();
    const src = ctx.createMediaStreamSource(new MediaStream([track]));
    const an = ctx.createAnalyser();
    an.fftSize = 512;
    src.connect(an);
    const buf = new Uint8Array(an.fftSize);
    let raf = 0;
    const tick = () => {
      an.getByteTimeDomainData(buf);
      let sum = 0;
      for (const v of buf) sum += ((v - 128) / 128) ** 2;
      setLevel(Math.min(1, Math.sqrt(sum / buf.length) * 4));
      raf = requestAnimationFrame(tick);
    };
    tick();
    return () => {
      cancelAnimationFrame(raf);
      ctx.close();
    };
  }, [checking]);

  const join = () => {
    stopStream();
    onJoin({ camOn: camOn && !camErr, micOn: micOn && !micErr, camId, micId });
  };

  return (
    <div className="mx-auto grid min-h-dvh max-w-5xl items-center gap-8 px-4 py-8 md:grid-cols-[1.3fr_1fr]">
      <div>
        <div className="relative aspect-video overflow-hidden rounded-3xl bg-ink">
          <video ref={videoRef} autoPlay muted playsInline className={`h-full w-full -scale-x-100 object-cover ${camOn && !camErr ? "" : "hidden"}`} />
          {(!camOn || camErr) && (
            <div className="absolute inset-0 flex items-center justify-center p-6 text-center text-sm text-white/80">
              {camErr || "Камера выключена"}
            </div>
          )}
          {checking && (
            <div className="absolute inset-0 flex items-center justify-center bg-ink/50">
              <Spinner className="h-8 w-8 text-white" />
            </div>
          )}
          <div className="absolute bottom-3 left-1/2 flex -translate-x-1/2 gap-2">
            <button
              onClick={() => setMicOn((v) => !v)}
              className={`flex h-11 w-11 items-center justify-center rounded-full text-lg ${micOn ? "bg-white/90 text-ink" : "bg-red-600 text-white"}`}
              aria-label={micOn ? "Выключить микрофон" : "Включить микрофон"}
              aria-pressed={!micOn}
            >
              {micOn ? "🎙️" : "🔇"}
            </button>
            <button
              onClick={() => setCamOn((v) => !v)}
              className={`flex h-11 w-11 items-center justify-center rounded-full text-lg ${camOn ? "bg-white/90 text-ink" : "bg-red-600 text-white"}`}
              aria-label={camOn ? "Выключить камеру" : "Включить камеру"}
              aria-pressed={!camOn}
            >
              {camOn ? "📷" : "🚫"}
            </button>
          </div>
        </div>
        <div className="mt-3 flex items-center gap-3 text-xs text-muted">
          <span>Микрофон</span>
          <div className="h-2 flex-1 overflow-hidden rounded-full bg-line">
            <div className="h-full rounded-full bg-emerald-500 transition-[width] duration-75" style={{ width: `${micOn && !micErr ? Math.round(level * 100) : 0}%` }} />
          </div>
          <span>{micOn && !micErr ? (level > 0.05 ? "слышно" : "скажите что-нибудь") : "выкл"}</span>
        </div>
      </div>

      <div className="card space-y-4 p-6">
        <div>
          <h1 className="text-xl font-bold">{title}</h1>
          <p className="text-sm text-muted">{subtitle}</p>
        </div>
        <p className="text-sm">Проверьте камеру и микрофон перед входом.</p>
        {insecure && (
          <div className="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900">
            Браузер не даёт доступ к камере и микрофону на незащищённом соединении. Откройте сайт по HTTPS (см. README: адрес https://…:8443).
          </div>
        )}
        {(camErr || micErr) && (
          <div className="space-y-1 rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-800" role="alert">
            {camErr && <p>📷 {camErr}</p>}
            {micErr && <p>🎙️ {micErr}</p>}
            <button className="btn-secondary btn-sm mt-1" onClick={start}>
              Проверить снова
            </button>
          </div>
        )}
        <div>
          <label className="label" htmlFor="cam">Камера</label>
          <select id="cam" className="input" value={camId ?? ""} onChange={(e) => setCamId(e.target.value || undefined)} disabled={!cams.length}>
            {!cams.length && <option value="">Нет доступных камер</option>}
            {cams.map((d, i) => (
              <option key={d.deviceId} value={d.deviceId}>
                {d.label || `Камера ${i + 1}`}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="label" htmlFor="mic">Микрофон</label>
          <select id="mic" className="input" value={micId ?? ""} onChange={(e) => setMicId(e.target.value || undefined)} disabled={!mics.length}>
            {!mics.length && <option value="">Нет доступных микрофонов</option>}
            {mics.map((d, i) => (
              <option key={d.deviceId} value={d.deviceId}>
                {d.label || `Микрофон ${i + 1}`}
              </option>
            ))}
          </select>
        </div>
        {joinError && (
          <div className="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-800" role="alert">
            {joinError}
          </div>
        )}
        <button className="btn-primary w-full py-3" onClick={join} disabled={joining || checking}>
          {joining && <Spinner className="h-4 w-4 text-white" />} Войти в класс
        </button>
        {(camErr || micErr) && <p className="text-center text-xs text-muted">Можно войти и без устройства — включить его можно позже.</p>}
      </div>
    </div>
  );
}
