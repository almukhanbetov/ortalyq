"use client";

import { useEffect, useRef, useState } from "react";
import { api } from "./api";
import { onServerClockChange, serverNow } from "./serverClock";

// ---- Актуальность расписания ----
// Запись и отмена меняют занятость преподавателя и ученика. Открытые страницы (форма записи,
// списки занятий) узнают об этом сразу — через BroadcastChannel между вкладками и событие в текущей —
// а также при возврате на вкладку и раз в минуту, чтобы не требовался перезаход.

const LESSONS_EVENT = "ortalyq:lessons-changed";
const CHANNEL = "ortalyq-lessons";

/** Сообщает всем открытым страницам (во всех вкладках), что занятия изменились. */
export function notifyLessonsChanged() {
  window.dispatchEvent(new Event(LESSONS_EVENT));
  try {
    const ch = new BroadcastChannel(CHANNEL);
    ch.postMessage("changed");
    ch.close();
  } catch {
    /* BroadcastChannel недоступен — остаются фокус и периодическое обновление */
  }
}

/** Вызывает refresh при изменении занятий (в любой вкладке), возврате на вкладку и раз в periodMs. */
export function useLessonsRefresh(refresh: () => void, periodMs = 60_000) {
  const ref = useRef(refresh);
  ref.current = refresh;
  useEffect(() => {
    let last = Date.now();
    const run = () => {
      last = Date.now();
      ref.current();
    };
    const onVisible = () => {
      // focus и visibilitychange приходят парой — не запрашиваем дважды подряд
      if (document.visibilityState === "visible" && Date.now() - last > 1000) run();
    };
    let ch: BroadcastChannel | null = null;
    try {
      ch = new BroadcastChannel(CHANNEL);
      ch.onmessage = run;
    } catch {
      ch = null;
    }
    window.addEventListener(LESSONS_EVENT, run);
    window.addEventListener("focus", onVisible);
    document.addEventListener("visibilitychange", onVisible);
    const t = setInterval(() => document.visibilityState === "visible" && run(), periodMs);
    return () => {
      ch?.close();
      window.removeEventListener(LESSONS_EVENT, run);
      window.removeEventListener("focus", onVisible);
      document.removeEventListener("visibilitychange", onVisible);
      clearInterval(t);
    };
  }, [periodMs]);
}

const KZ_CITIES = ["Алматы", "Астана", "Шымкент", "Караганда"];

function useCatalogMeta() {
  const [meta, setMeta] = useState<{ subjects: string[]; cities: string[] }>({ subjects: [], cities: KZ_CITIES });
  useEffect(() => {
    api<{ subjects: string[]; cities?: string[] }>("/api/subjects")
      .then((r) => setMeta({ subjects: r.subjects, cities: r.cities?.length ? r.cities : KZ_CITIES }))
      .catch(() => {});
  }, []);
  return meta;
}

export function useSubjects() {
  return useCatalogMeta().subjects;
}

export function useCities() {
  return useCatalogMeta().cities;
}

/**
 * Текущий момент по серверным часам (не по часам устройства); обновляется раз в interval мс
 * и сразу после замера смещения часов (для статуса урока, «через N минут», кнопки входа).
 */
export function useNow(interval = 30000) {
  const [now, setNow] = useState(() => serverNow());
  useEffect(() => {
    const tick = () => setNow(serverNow());
    tick();
    const t = setInterval(tick, interval);
    const off = onServerClockChange(tick);
    return () => {
      clearInterval(t);
      off();
    };
  }, [interval]);
  return now;
}
