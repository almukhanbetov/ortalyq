"use client";

import Link from "next/link";
import { Suspense, useCallback, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, errorMessage, Teacher } from "@/lib/api";
import { formatPrice, plural } from "@/lib/format";
import { useCities, useSubjects } from "@/lib/hooks";
import { Avatar, DemoBadge, Empty, ErrorBox, PageHeader, Skeleton, Stars } from "@/components/ui";

function TeachersInner() {
  const router = useRouter();
  const params = useSearchParams();
  const subjects = useSubjects();
  const cities = useCities();
  const [city, setCity] = useState(params.get("city") ?? "");
  const [subject, setSubject] = useState(params.get("subject") ?? "");
  const [minPrice, setMinPrice] = useState(params.get("minPrice") ?? "");
  const [maxPrice, setMaxPrice] = useState(params.get("maxPrice") ?? "");
  const [sort, setSort] = useState(params.get("sort") ?? "");
  const [q, setQ] = useState(params.get("q") ?? "");
  const [teachers, setTeachers] = useState<Teacher[] | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    const sp = new URLSearchParams();
    if (subject) sp.set("subject", subject);
    if (city) sp.set("city", city);
    if (minPrice) sp.set("minPrice", minPrice);
    if (maxPrice) sp.set("maxPrice", maxPrice);
    if (sort) sp.set("sort", sort);
    if (q.trim()) sp.set("q", q.trim());
    router.replace(`/teachers${sp.size ? `?${sp}` : ""}`, { scroll: false });
    setError("");
    setTeachers(null);
    try {
      const r = await api<{ teachers: Teacher[] }>(`/api/teachers?${sp}`);
      setTeachers(r.teachers);
    } catch (e) {
      setError(errorMessage(e));
      setTeachers([]);
    }
  }, [subject, city, minPrice, maxPrice, sort, q, router]);

  useEffect(() => {
    const t = setTimeout(load, 250);
    return () => clearTimeout(t);
  }, [load]);

  const reset = () => {
    setSubject("");
    setCity("");
    setMinPrice("");
    setMaxPrice("");
    setSort("");
    setQ("");
  };

  return (
    <div>
      <PageHeader title="Преподаватели" subtitle="Выберите преподавателя и запишитесь на удобное время" />
      <div className="card mb-6 grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-[1.2fr_1fr_0.9fr_0.7fr_0.7fr_1fr_auto] lg:items-end">
        <div>
          <label className="label" htmlFor="q">Поиск по имени</label>
          <input id="q" className="input" placeholder="Например, Айгерим" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="subject">Предмет</label>
          <select id="subject" className="input" value={subject} onChange={(e) => setSubject(e.target.value)}>
            <option value="">Все предметы</option>
            {subjects.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        </div>
        <div>
          <label className="label" htmlFor="city">Город</label>
          <select id="city" className="input" value={city} onChange={(e) => setCity(e.target.value)}>
            <option value="">Все города</option>
            {cities.map((c) => (
              <option key={c}>{c}</option>
            ))}
          </select>
        </div>
        <div>
          <label className="label" htmlFor="min">Цена от, ₸</label>
          <input id="min" className="input" inputMode="numeric" type="number" min={0} step={500} placeholder="3 000" value={minPrice} onChange={(e) => setMinPrice(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="max">до, ₸</label>
          <input id="max" className="input" inputMode="numeric" type="number" min={0} step={500} placeholder="15 000" value={maxPrice} onChange={(e) => setMaxPrice(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="sort">Сортировка</label>
          <select id="sort" className="input" value={sort} onChange={(e) => setSort(e.target.value)}>
            <option value="">По рейтингу</option>
            <option value="price_asc">Сначала дешевле</option>
            <option value="price_desc">Сначала дороже</option>
          </select>
        </div>
        <button className="btn-ghost" onClick={reset}>
          Сбросить
        </button>
      </div>

      {error && <ErrorBox message={error} onRetry={load} />}
      {teachers === null ? (
        <div className="grid gap-4 md:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-48" />
          ))}
        </div>
      ) : teachers.length === 0 && !error ? (
        <Empty icon="🔍" title="Никого не нашли" text="Попробуйте изменить фильтры или выбрать другой предмет." action={<button className="btn-secondary" onClick={reset}>Сбросить фильтры</button>} />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {teachers.map((t) => (
            <TeacherCard key={t.id} t={t} />
          ))}
        </div>
      )}
    </div>
  );
}

function TeacherCard({ t }: { t: Teacher }) {
  return (
    <Link href={`/teachers/${t.id}`} className="card group flex flex-col gap-4 p-5 transition hover:border-brand-300 hover:shadow-lg">
      <div className="flex gap-4">
        <Avatar name={t.name} src={t.photoUrl} size={64} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-semibold group-hover:text-brand-700">{t.name}</h3>
            {t.isDemo && <DemoBadge />}
          </div>
          {t.city && <div className="text-xs text-muted">📍 {t.city} · занятия онлайн</div>}
          <p className="mt-0.5 line-clamp-2 text-sm text-muted">{t.headline || "Преподаватель"}</p>
          <div className="mt-1 flex items-center gap-2 text-xs text-muted">
            {t.rating != null ? (
              <>
                <Stars value={t.rating} /> {t.rating.toFixed(1)} · {t.reviewCount} {plural(t.reviewCount, "отзыв", "отзыва", "отзывов")}
              </>
            ) : (
              "Пока нет отзывов"
            )}
          </div>
        </div>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {t.subjects.map((s) => (
          <span key={s} className="chip bg-brand-50 text-brand-700">
            {s}
          </span>
        ))}
      </div>
      <div className="mt-auto flex items-center justify-between border-t border-line pt-4">
        <div>
          <span className="text-lg font-bold">{formatPrice(t.price)}</span>
          <span className="text-sm text-muted"> / 60 мин</span>
        </div>
        <span className="btn-primary btn-sm">Записаться</span>
      </div>
    </Link>
  );
}

export default function TeachersPage() {
  return (
    <Suspense>
      <TeachersInner />
    </Suspense>
  );
}
