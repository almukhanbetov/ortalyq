"use client";

import dynamic from "next/dynamic";
import { Spinner } from "../ui";

function WorkshopLoading() {
  return (
    <div className="flex h-full w-full items-center justify-center gap-2 bg-white text-sm text-muted">
      <Spinner className="h-4 w-4" /> Загружаем мастерскую…
    </div>
  );
}

// Модуль мастерской (Yjs и панель) загружается только при первом открытии вкладки.
export const WorkshopPanel = dynamic(() => import("./WorkshopPanel"), { ssr: false, loading: WorkshopLoading });
