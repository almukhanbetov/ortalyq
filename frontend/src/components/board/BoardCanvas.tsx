"use client";

// Загружается только в браузере (см. board/index.tsx).
import "@excalidraw/excalidraw/index.css";
import { CaptureUpdateAction, Excalidraw, MainMenu, exportToBlob, restoreElements } from "@excalidraw/excalidraw";
import type { ExcalidrawImperativeAPI } from "@excalidraw/excalidraw/types";
import type { ExcalidrawElement } from "@excalidraw/excalidraw/element/types";
import { useCallback, useEffect, useRef, useState } from "react";
import type { LessonSocket, ServerMessage } from "@/lib/lessonSocket";

declare global {
  interface Window {
    EXCALIDRAW_ASSET_PATH?: string | string[];
  }
}
if (typeof window !== "undefined") window.EXCALIDRAW_ASSET_PATH = "/";

type El = ExcalidrawElement;

function byIndex(a: El, b: El) {
  const ai = a.index ?? "",
    bi = b.index ?? "";
  if (!ai || !bi) return 0;
  return ai < bi ? -1 : ai > bi ? 1 : 0;
}

/** Правило слияния как в Excalidraw: бо́льшая версия побеждает, при равенстве — меньший versionNonce. */
function newer(remote: El, local: El | undefined) {
  if (!local) return true;
  return remote.version > local.version || (remote.version === local.version && remote.versionNonce < local.versionNonce);
}

const uiOptions = {
  canvasActions: {
    clearCanvas: false,
    loadScene: false,
    saveToActiveFile: false,
    export: false as const,
    toggleTheme: false,
    saveAsImage: true,
    changeViewBackgroundColor: true,
  },
  tools: { image: false },
};

export interface LiveBoardProps {
  mode: "live";
  socket: LessonSocket | null;
  onRequestClear: () => void;
  readOnly?: boolean;
  /** Доступ к API Excalidraw (для собственной панели инструментов) */
  onApi?: (api: ExcalidrawImperativeAPI) => void;
  /** Вызывается при смене активного инструмента */
  onToolChange?: (tool: string) => void;
}

export interface ViewBoardProps {
  mode: "view";
  elements: unknown[];
  /** Менять при обновлении содержимого (шаблон записи). */
  revision?: number;
  fit?: boolean;
  onApi?: (api: ExcalidrawImperativeAPI) => void;
}

export default function BoardCanvas(props: LiveBoardProps | ViewBoardProps) {
  return props.mode === "live" ? <LiveBoard {...props} /> : <ViewBoard {...props} />;
}

function LiveBoard({ socket, onRequestClear, readOnly, onApi: onApiProp, onToolChange }: LiveBoardProps) {
  const lastTool = useRef("");
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null);
  const known = useRef(new Map<string, number>()); // последняя синхронизированная версия элемента
  const pending = useRef(new Map<string, El>()); // ещё не отправленные изменения
  const buffered = useRef<ServerMessage[]>([]);
  const flushTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [synced, setSynced] = useState(false);

  const flush = useCallback(() => {
    flushTimer.current = null;
    if (!pending.current.size || !socket) return;
    const elements = [...pending.current.values()];
    if (socket.send({ type: "board.update", elements })) pending.current.clear();
    // если не отправилось — изменения останутся в pending и уйдут после переподключения
  }, [socket]);

  const schedule = useCallback(() => {
    if (!flushTimer.current) flushTimer.current = setTimeout(flush, 50);
  }, [flush]);

  const applyRemote = useCallback(
    (msg: ServerMessage) => {
      const api = apiRef.current;
      if (!api) {
        buffered.current.push(msg);
        return;
      }
      const full = msg.type === "hello" || msg.type === "board.snapshot";
      const raw = full ? (msg.board?.elements ?? []) : (msg.elements ?? []);
      const remote = restoreElements(raw, null) as El[];
      const local = api.getSceneElementsIncludingDeleted() as readonly El[];
      const map = new Map<string, El>(local.map((e) => [e.id, e]));
      let changed = false;
      for (const r of remote) {
        if (newer(r, map.get(r.id))) {
          map.set(r.id, r);
          changed = true;
        }
        known.current.set(r.id, Math.max(known.current.get(r.id) ?? -1, r.version));
      }
      if (full) {
        // После (пере)подключения отправляем локальные изменения, которых нет на сервере.
        const server = new Map(remote.map((e) => [e.id, e]));
        for (const l of local) {
          const s = server.get(l.id);
          if (!s || newer(l, s)) pending.current.set(l.id, l);
        }
        setSynced(true);
        if (pending.current.size) schedule();
      }
      if (changed) {
        const elements = [...map.values()].sort(byIndex);
        api.updateScene({ elements, captureUpdate: CaptureUpdateAction.NEVER });
      }
    },
    [schedule],
  );

  useEffect(() => {
    if (!socket) return;
    setSynced(false);
    const off = socket.on((m) => {
      if (m.type === "hello" || m.type === "board.snapshot" || m.type === "board.update") applyRemote(m);
    });
    const offStatus = socket.onStatus((s) => {
      if (s !== "open") setSynced(false);
    });
    // Доска могла подключиться позже сокета — запрашиваем актуальный снимок.
    if (socket.status === "open") socket.send({ type: "board.sync" });
    return () => {
      off();
      offStatus();
    };
  }, [socket, applyRemote]);

  const onApi = useCallback(
    (api: ExcalidrawImperativeAPI) => {
      apiRef.current = api;
      onApiProp?.(api);
      const queued = buffered.current;
      buffered.current = [];
      queued.forEach(applyRemote);
    },
    [applyRemote, onApiProp],
  );

  const onChange = useCallback(
    (elements: readonly El[], appState: { activeTool: { type: string } }) => {
      const tool = appState.activeTool.type;
      if (tool !== lastTool.current) {
        lastTool.current = tool;
        onToolChange?.(tool);
      }
      if (readOnly) return;
      let any = false;
      for (const el of elements) {
        if ((known.current.get(el.id) ?? -1) < el.version) {
          known.current.set(el.id, el.version);
          pending.current.set(el.id, el);
          any = true;
        }
      }
      if (any) schedule();
    },
    [schedule, readOnly, onToolChange],
  );

  return (
    <div className="board-host relative h-full w-full">
      <Excalidraw
        excalidrawAPI={onApi}
        onChange={onChange}
        langCode="ru-RU"
        UIOptions={uiOptions}
        viewModeEnabled={readOnly}
        initialData={{ appState: { viewBackgroundColor: "#ffffff", currentItemStrokeColor: "#5b21b6" } }}
      >
        <MainMenu>
          <MainMenu.DefaultItems.SaveAsImage />
          <MainMenu.DefaultItems.ChangeCanvasBackground />
          {!readOnly && (
            <MainMenu.Item onSelect={onRequestClear} icon={<span aria-hidden>🗑️</span>}>
              Очистить доску
            </MainMenu.Item>
          )}
          <MainMenu.DefaultItems.Help />
        </MainMenu>
      </Excalidraw>
      {!synced && (
        <div className="pointer-events-none absolute left-1/2 top-16 z-10 -translate-x-1/2 rounded-full bg-white/95 px-3 py-1.5 text-xs font-medium text-muted shadow">
          Синхронизация доски…
        </div>
      )}
    </div>
  );
}

function ViewBoard({ elements, revision, fit = true, onApi }: ViewBoardProps) {
  const [api, setApi] = useState<ExcalidrawImperativeAPI | null>(null);
  const [initial] = useState(() => restoreElements(elements as never, null));
  const lastCount = useRef(-1);

  useEffect(() => {
    if (!api) return;
    const restored = restoreElements(elements as never, null);
    if (revision !== undefined) api.updateScene({ elements: restored, captureUpdate: CaptureUpdateAction.NEVER });
    const visible = restored.filter((e) => !e.isDeleted).length;
    if (fit && visible !== lastCount.current) {
      lastCount.current = visible;
      // Вписываем содержимое в окно после того, как Excalidraw посчитал размеры контейнера
      if (visible > 0) requestAnimationFrame(() => api.scrollToContent(undefined, { fitToViewport: true, viewportZoomFactor: 0.85, animate: false }));
    }
  }, [api, revision, elements, fit]);

  return (
    <div className="board-host h-full w-full">
      <Excalidraw
        excalidrawAPI={(a) => {
          setApi(a);
          onApi?.(a);
        }}
        langCode="ru-RU"
        viewModeEnabled
        zenModeEnabled
        UIOptions={{ canvasActions: { ...uiOptions.canvasActions, saveAsImage: false, changeViewBackgroundColor: false }, tools: { image: false } }}
        initialData={{ elements: initial, appState: { viewBackgroundColor: "#ffffff" }, scrollToContent: true }}
      />
    </div>
  );
}

/** Экспорт доски в PNG (для страницы занятия). */
export async function exportBoardPng(elements: unknown[]): Promise<Blob> {
  const restored = restoreElements(elements as never, null).filter((e) => !e.isDeleted);
  return exportToBlob({
    elements: restored,
    appState: { exportBackground: true, viewBackgroundColor: "#ffffff" },
    files: null,
    mimeType: "image/png",
    exportPadding: 24,
  });
}
