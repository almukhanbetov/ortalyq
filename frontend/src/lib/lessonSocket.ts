"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ChatMessage, LessonStatus, Recording } from "./api";

/* eslint-disable @typescript-eslint/no-explicit-any */
export type ServerMessage = { type: string; [k: string]: any };
type Listener = (m: ServerMessage) => void;
export type SocketStatus = "connecting" | "open" | "reconnecting" | "closed";

export function wsBase(): string {
  const env = process.env.NEXT_PUBLIC_WS_URL;
  if (env) return env.replace(/\/$/, "");
  return `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}`;
}

/** WebSocket занятия с автоматическим переподключением. После переподключения сервер
 *  присылает полный снимок (hello), поэтому состояние доски и чата восстанавливается. */
export class LessonSocket {
  private ws: WebSocket | null = null;
  private listeners = new Set<Listener>();
  private statusListeners = new Set<(s: SocketStatus) => void>();
  private stopped = false;
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  status: SocketStatus = "connecting";

  constructor(private lessonId: string) {}

  connect() {
    this.stopped = false;
    const ws = new WebSocket(`${wsBase()}/api/lessons/${this.lessonId}/ws`);
    this.ws = ws;
    ws.onopen = () => {
      this.attempt = 0;
      this.setStatus("open");
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as ServerMessage;
        this.listeners.forEach((l) => l(msg));
      } catch {
        /* игнорируем */
      }
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (this.stopped) {
        this.setStatus("closed");
        return;
      }
      this.setStatus("reconnecting");
      const delay = Math.min(10000, 500 * 2 ** this.attempt++);
      this.timer = setTimeout(() => this.connect(), delay);
    };
  }

  private setStatus(s: SocketStatus) {
    this.status = s;
    this.statusListeners.forEach((l) => l(s));
  }

  send(obj: unknown): boolean {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(obj));
      return true;
    }
    return false;
  }

  on(fn: Listener) {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  }

  onStatus(fn: (s: SocketStatus) => void) {
    this.statusListeners.add(fn);
    return () => {
      this.statusListeners.delete(fn);
    };
  }

  close() {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    const ws = this.ws;
    this.ws = null;
    ws?.close();
    this.setStatus("closed");
  }
}

export interface PresenceUser {
  id: string;
  name: string;
  role: "teacher" | "student";
}

export function useLessonRealtime(lessonId: string, enabled: boolean) {
  const [socket, setSocket] = useState<LessonSocket | null>(null);
  const [status, setStatus] = useState<SocketStatus>("connecting");
  const [chat, setChat] = useState<ChatMessage[]>([]);
  const [presence, setPresence] = useState<PresenceUser[]>([]);
  const [recording, setRecording] = useState<Recording | null>(null);
  const [lessonStatus, setLessonStatus] = useState<LessonStatus | null>(null);
  const [lastError, setLastError] = useState("");
  const [version, setVersion] = useState(0); // счётчик событий «материалы/задания изменились»

  useEffect(() => {
    if (!enabled) return;
    const s = new LessonSocket(lessonId);
    setSocket(s);
    const offStatus = s.onStatus(setStatus);
    const off = s.on((m) => {
      switch (m.type) {
        case "hello":
          setChat(m.chat ?? []);
          setRecording(m.recording ?? null);
          setLessonStatus(m.lessonStatus);
          break;
        case "chat.message":
          setChat((c) => (c.some((x) => x.id === m.message.id) ? c : [...c, m.message]));
          break;
        case "presence":
          setPresence(m.users ?? []);
          break;
        case "recording":
          setRecording(m.recording);
          break;
        case "lesson.status":
          setLessonStatus(m.status);
          break;
        case "materials.changed":
        case "homework.changed":
          setVersion((v) => v + 1);
          break;
        case "error":
          setLastError(m.message);
          break;
      }
    });
    s.connect();
    return () => {
      off();
      offStatus();
      s.close();
      setSocket(null);
    };
  }, [lessonId, enabled]);

  const counter = useRef(0);
  const sendChat = useCallback(
    (body: string) => {
      const clientId = `${Date.now()}-${counter.current++}`;
      return socket?.send({ type: "chat.send", body, clientId }) ?? false;
    },
    [socket],
  );

  return { socket, status, chat, presence, recording, setRecording, lessonStatus, lastError, setLastError, sendChat, version };
}
