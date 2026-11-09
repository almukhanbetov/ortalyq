// Копирует статические ресурсы из node_modules в public/, чтобы класс работал без внешнего CDN
// (важно для локальной сети и для шаблона записи в Egress):
//   • шрифты Excalidraw → public/fonts
//   • Monaco Editor (AMD-сборка min/vs) → public/monaco/vs; загружается только при открытии вкладки «Код»
import { cpSync, existsSync, rmSync } from "node:fs";
const fonts = "node_modules/@excalidraw/excalidraw/dist/prod/fonts";
if (existsSync(fonts)) {
  cpSync(fonts, "public/fonts", { recursive: true });
  console.log("excalidraw fonts → public/fonts");
}
const monaco = "node_modules/monaco-editor/min/vs";
if (existsSync(monaco)) {
  rmSync("public/monaco", { recursive: true, force: true }); // без файлов прошлых версий
  cpSync(monaco, "public/monaco/vs", { recursive: true });
  console.log("monaco-editor → public/monaco/vs");
}
