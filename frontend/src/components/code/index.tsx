"use client";

import dynamic from "next/dynamic";
import { Spinner } from "../ui";

function CodeLoading() {
  return (
    <div className="flex h-full w-full items-center justify-center gap-2 bg-white text-sm text-muted">
      <Spinner className="h-4 w-4" /> Загружаем редактор кода…
    </div>
  );
}

// Monaco и Yjs загружаются только при первом открытии вкладки «Код».
export const CodePanel = dynamic(() => import("./CodePanel"), { ssr: false, loading: CodeLoading });
