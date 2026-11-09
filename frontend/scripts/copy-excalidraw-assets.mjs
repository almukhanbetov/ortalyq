// Копирует шрифты Excalidraw в public/, чтобы доска работала без внешнего CDN
// (важно для локальной сети и для шаблона записи в Egress).
import { cpSync, existsSync } from "node:fs";
const src = "node_modules/@excalidraw/excalidraw/dist/prod/fonts";
if (existsSync(src)) {
  cpSync(src, "public/fonts", { recursive: true });
  console.log("excalidraw fonts → public/fonts");
}
