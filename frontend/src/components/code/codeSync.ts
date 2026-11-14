// Синхронизация документа Yjs редактора кода через WebSocket занятия (LessonSocket).
// Загружается только вместе с вкладкой «Код». Механизм синхронизации общий — см. lib/yjsSync.ts
// (префикс сообщений «code.*»); учебный текст мастерской — отдельный документ с префиксом «workshop.*».
import type { LessonSocket } from "@/lib/lessonSocket";
import type * as Y from "yjs";
import { REMOTE, SyncState, transformIndex, YjsSync } from "@/lib/yjsSync";

export { REMOTE, transformIndex };
export type { SyncState };

export class CodeSync extends YjsSync {
  readonly code = this.doc.getText("code");
  readonly stdin = this.doc.getText("stdin");
  readonly meta = this.doc.getMap<string>("meta");

  constructor(socket: LessonSocket, onState: (s: SyncState) => void, onReject: (message: string) => void, seed?: (doc: Y.Doc) => void) {
    super("code", socket, onState, onReject, undefined, seed);
  }
}
