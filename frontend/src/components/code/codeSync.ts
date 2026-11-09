// Синхронизация документа Yjs редактора кода через WebSocket занятия (LessonSocket).
// Загружается только вместе с вкладкой «Код».
//
// Протокол (см. backend/internal/realtime/code.go):
//   → code.sync                      ← code.snapshot {updates[], seq}
//   → code.update {update, cid}      ← code.ack {cid, seq}; остальным: code.update {update, seq}
//   ← code.compact.request {upTo}    → code.compact {upTo, state}
//   ← code.reject {cid, message}
//
// Петли нет: обновления, применённые из сети, помечаются origin=REMOTE и не отправляются обратно.
// Потерь нет: после каждого (пере)подключения клиент получает журнал сервера и досылает разницу
// своего состояния (в т.ч. правки, сделанные без связи).
import * as Y from "yjs";
import type { LessonSocket, ServerMessage } from "@/lib/lessonSocket";

export const REMOTE = Symbol("remote");

function toB64(u: Uint8Array): string {
  let s = "";
  for (let i = 0; i < u.length; i += 0x8000) s += String.fromCharCode(...u.subarray(i, i + 0x8000));
  return btoa(s);
}
function fromB64(s: string): Uint8Array {
  const bin = atob(s);
  const u = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) u[i] = bin.charCodeAt(i);
  return u;
}

export type SyncState = "connecting" | "synced" | "offline";

export class CodeSync {
  readonly doc = new Y.Doc();
  readonly code = this.doc.getText("code");
  readonly stdin = this.doc.getText("stdin");
  readonly meta = this.doc.getMap<string>("meta");
  state: SyncState = "connecting";
  private lastSeq = 0;
  private outbox: Uint8Array[] = [];
  private scheduled = false;
  private counter = 0;
  private offs: (() => void)[] = [];

  constructor(
    private socket: LessonSocket,
    private onState: (s: SyncState) => void,
    private onReject: (message: string) => void,
  ) {}

  start() {
    const onUpdate = (u: Uint8Array, origin: unknown) => {
      if (origin === REMOTE) return;
      this.outbox.push(u);
      // Отправка в микрозадаче: изменения одной задачи склеиваются, а браузер не откладывает её,
      // как setTimeout во вкладке в фоне (иначе отмена/правка могла уйти с задержкой в секунды).
      if (this.state === "synced" && !this.scheduled) {
        this.scheduled = true;
        queueMicrotask(() => this.flush());
      }
    };
    this.doc.on("update", onUpdate);
    this.offs.push(() => this.doc.off("update", onUpdate));
    this.offs.push(this.socket.on((m) => this.handle(m)));
    this.offs.push(
      this.socket.onStatus((s) => {
        if (s === "open") this.socket.send({ type: "code.sync" });
        else this.setState("offline");
      }),
    );
    if (this.socket.status === "open") this.socket.send({ type: "code.sync" });
  }

  stop() {
    this.flush();
    this.offs.forEach((f) => f());
    this.doc.destroy();
  }

  private setState(s: SyncState) {
    if (this.state === s) return;
    this.state = s;
    this.onState(s);
  }

  private flush() {
    this.scheduled = false;
    if (this.state !== "synced" || !this.outbox.length) return;
    const update = this.outbox.length === 1 ? this.outbox[0] : Y.mergeUpdates(this.outbox);
    this.outbox = [];
    const cid = `${Date.now().toString(36)}-${this.counter++}`;
    if (!this.socket.send({ type: "code.update", update: toB64(update), cid })) {
      // Нет связи: разница будет досылаться после переподключения (code.snapshot).
      this.setState("offline");
    }
  }

  private apply(updates: string[]) {
    Y.transact(
      this.doc,
      () => {
        for (const u of updates) {
          try {
            Y.applyUpdate(this.doc, fromB64(u), REMOTE);
          } catch {
            /* повреждённое обновление пропускаем, остальные применяются */
          }
        }
      },
      REMOTE,
    );
  }

  private handle(m: ServerMessage) {
    switch (m.type) {
      case "code.snapshot": {
        const updates: string[] = m.updates ?? [];
        this.apply(updates);
        this.lastSeq = Math.max(this.lastSeq, m.seq ?? 0);
        // Состояние сервера → чего у сервера нет, досылаем одним обновлением.
        const server = new Y.Doc();
        for (const u of updates) {
          try {
            Y.applyUpdate(server, fromB64(u));
          } catch {
            /* пропуск */
          }
        }
        this.outbox = [];
        if (!Y.equalSnapshots(Y.snapshot(server), Y.snapshot(this.doc))) {
          this.outbox.push(Y.encodeStateAsUpdate(this.doc, Y.encodeStateVector(server)));
        }
        server.destroy();
        this.setState("synced");
        this.flush();
        break;
      }
      case "code.update":
        this.apply([m.update]);
        this.lastSeq = Math.max(this.lastSeq, m.seq ?? 0);
        break;
      case "code.ack":
        this.lastSeq = Math.max(this.lastSeq, m.seq ?? 0);
        break;
      case "code.reject":
        this.onReject(m.message ?? "Изменение не принято сервером");
        break;
      case "code.compact.request":
        // Сообщения приходят по порядку seq: если мы видели upTo, в документе есть все обновления до него.
        if (this.state === "synced" && this.lastSeq >= m.upTo) {
          this.socket.send({ type: "code.compact", upTo: m.upTo, state: toB64(Y.encodeStateAsUpdate(this.doc)) });
        }
        break;
    }
  }
}

/** Пересчёт позиции курсора в тексте после изменения delta (Yjs). */
export function transformIndex(delta: { retain?: number; insert?: unknown; delete?: number }[], index: number): number {
  let pos = 0; // позиция в старом тексте
  let out = index;
  for (const d of delta) {
    if (pos > index) break;
    if (d.retain) pos += d.retain;
    else if (typeof d.insert === "string") {
      out += d.insert.length; // вставка в pos <= index сдвигает курсор вправо
    } else if (d.delete) {
      out -= Math.min(d.delete, index - pos);
      pos += d.delete;
    }
  }
  return Math.max(0, out);
}
