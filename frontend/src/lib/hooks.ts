"use client";

import { useEffect, useState } from "react";
import { api } from "./api";

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

/** Обновляет компонент раз в interval мс (для «через N минут», доступности кнопки входа). */
export function useNow(interval = 30000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), interval);
    return () => clearInterval(t);
  }, [interval]);
  return now;
}
