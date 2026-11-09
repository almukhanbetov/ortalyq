"use client";

import dynamic from "next/dynamic";
import { Spinner } from "../ui";

function BoardLoading() {
  return (
    <div className="flex h-full w-full items-center justify-center gap-2 bg-white text-sm text-muted">
      <Spinner className="h-4 w-4" /> Загружаем доску…
    </div>
  );
}

// Excalidraw работает только в браузере
export const Board = dynamic(() => import("./BoardCanvas"), { ssr: false, loading: BoardLoading });

export async function exportBoardPng(elements: unknown[]) {
  const mod = await import("./BoardCanvas");
  return mod.exportBoardPng(elements);
}
