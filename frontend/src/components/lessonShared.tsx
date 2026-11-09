"use client";

import { api, errorMessage } from "@/lib/api";
import { recordingLabels } from "@/lib/format";
import { toast } from "./ui";

/** Получает временную ссылку на файл (после проверки прав на сервере) и открывает её. */
export async function openSignedLink(path: string, download = false) {
  const w = window.open("", "_blank");
  try {
    const r = await api<{ url: string }>(path);
    const url = r.url + (download ? "?download=1" : "");
    if (w) w.location.href = url;
    else window.location.href = url;
  } catch (e) {
    w?.close();
    toast(errorMessage(e), "err");
  }
}

export function RecordingBadge({ status }: { status: string }) {
  const cls: Record<string, string> = {
    ready: "bg-emerald-100 text-emerald-800",
    failed: "bg-red-100 text-red-700",
    recording: "bg-red-600 text-white",
    processing: "bg-amber-100 text-amber-800",
    starting: "bg-amber-100 text-amber-800",
    awaiting_consent: "bg-brand-100 text-brand-800",
  };
  return <span className={`chip ${cls[status] ?? "bg-slate-100 text-slate-700"}`}>{recordingLabels[status] ?? status}</span>;
}

