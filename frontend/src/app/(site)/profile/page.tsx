"use client";

import { Suspense, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { api, errorMessage, Teacher, upload } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { useCities, useSubjects } from "@/lib/hooks";
import { browserTimezone, CURRENCY, formatPhone } from "@/lib/format";
import { Avatar, ErrorBox, Loading, Notice, PageHeader, Spinner, toast } from "@/components/ui";

// Сначала часовые пояса Казахстана, затем соседние регионы
const timezones = [
  "Asia/Almaty", "Asia/Qostanay", "Asia/Aqtobe", "Asia/Aqtau", "Asia/Atyrau", "Asia/Oral", "Asia/Qyzylorda",
  "Asia/Bishkek", "Asia/Tashkent", "Asia/Novosibirsk", "Asia/Omsk", "Asia/Yekaterinburg", "Europe/Moscow", "Asia/Baku",
  "Asia/Tbilisi", "Europe/Istanbul", "Europe/Berlin", "Europe/London", "UTC",
];

function ProfileInner() {
  const { user, ready, refresh, config } = useRequireUser();
  const params = useSearchParams();
  const [name, setName] = useState("");
  const [tz, setTz] = useState("");
  const [city, setCity] = useState("");
  const [phone, setPhone] = useState("");
  const cities = useCities();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (user) {
      setName(user.name);
      setTz(user.timezone);
      setCity(user.city ?? "");
      setPhone(user.phone ? formatPhone(user.phone) : "");
    }
  }, [user]);

  if (!ready || !user) return <Loading />;
  const tzOptions = Array.from(new Set([tz, browserTimezone(), ...timezones].filter(Boolean)));

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("/api/me", { method: "PATCH", json: { name, timezone: tz, city, phone } });
      await refresh();
      toast("Профиль сохранён");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title="Профиль" subtitle={user.email} />
      {params.get("welcome") && user.role === "teacher" && (
        <Notice tone="success" title="Добро пожаловать!">
          Заполните профиль преподавателя и добавьте интервалы в расписании — после этого ученики смогут вас найти и записаться.
        </Notice>
      )}
      <form onSubmit={save} className="card grid gap-4 p-5 sm:grid-cols-2">
        <h2 className="font-semibold sm:col-span-2">Аккаунт</h2>
        <div>
          <label className="label" htmlFor="name">Имя</label>
          <input id="name" className="input" required minLength={2} maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="tz">Часовой пояс</label>
          <select id="tz" className="input" value={tz} onChange={(e) => setTz(e.target.value)}>
            {tzOptions.map((z) => (
              <option key={z}>{z}</option>
            ))}
          </select>
          <p className="mt-1 text-xs text-muted">Всё время на сайте показывается в этом поясе.</p>
        </div>
        <div>
          <label className="label" htmlFor="city">Город</label>
          <input id="city" className="input" list="city-list" maxLength={60} placeholder="Алматы" value={city} onChange={(e) => setCity(e.target.value)} />
          <datalist id="city-list">
            {cities.map((c) => (
              <option key={c} value={c} />
            ))}
          </datalist>
          {user.role === "teacher" && <p className="mt-1 text-xs text-muted">Показывается в каталоге преподавателей.</p>}
        </div>
        <div>
          <label className="label" htmlFor="phone">Телефон</label>
          <input id="phone" className="input" type="tel" inputMode="tel" placeholder="+7 700 000 00 00" value={phone} onChange={(e) => setPhone(e.target.value)} />
          <p className="mt-1 text-xs text-muted">Формат +7. Виден только вам.</p>
        </div>
        {error && (
          <div className="sm:col-span-2">
            <ErrorBox message={error} />
          </div>
        )}
        <div className="sm:col-span-2">
          <button className="btn-primary" disabled={busy}>
            {busy && <Spinner className="h-4 w-4 text-white" />} Сохранить
          </button>
        </div>
      </form>
      {user.role === "teacher" && <TeacherProfileForm storage={!!config?.storageConfigured} />}
    </div>
  );
}

function TeacherProfileForm({ storage }: { storage: boolean }) {
  const subjectsAll = useSubjects();
  const [p, setP] = useState<Teacher | null>(null);
  const [headline, setHeadline] = useState("");
  const [bio, setBio] = useState("");
  const [subjects, setSubjects] = useState<string[]>([]);
  const [newSubject, setNewSubject] = useState("");
  const [price, setPrice] = useState("5000");
  const [exp, setExp] = useState("0");
  const [photo, setPhoto] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    api<{ profile: Teacher }>("/api/me/profile")
      .then(({ profile }) => {
        setP(profile);
        setHeadline(profile.headline);
        setBio(profile.bio);
        setSubjects(profile.subjects);
        setPrice(String(profile.price));
        setExp(String(profile.experienceYears));
        setPhoto(profile.photoUrl);
      })
      .catch((e) => setError(errorMessage(e)));
  }, []);

  const addSubject = (s: string) => {
    const v = s.trim();
    if (v && !subjects.includes(v) && subjects.length < 8) setSubjects([...subjects, v]);
    setNewSubject("");
  };

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("/api/me/profile", {
        method: "PUT",
        json: { headline, bio, subjects, price: Number(price), experienceYears: Number(exp) },
      });
      toast("Профиль преподавателя сохранён");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const onPhoto = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    if (f.size > 5 * 1024 * 1024) {
      toast("Фото должно быть не больше 5 МБ", "err");
      return;
    }
    const fd = new FormData();
    fd.append("file", f);
    setUploading(true);
    try {
      const r = await upload<{ photoUrl: string }>("/api/me/photo", fd);
      setPhoto(r.photoUrl);
      toast("Фото обновлено");
    } catch (err) {
      toast(errorMessage(err), "err");
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  if (!p && !error) return <Loading />;
  return (
    <form onSubmit={save} className="card space-y-4 p-5">
      <h2 className="font-semibold">Профиль преподавателя</h2>
      <div className="flex items-center gap-4">
        <Avatar name={p?.name ?? ""} src={photo} size={80} />
        <div>
          <input ref={fileRef} type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={onPhoto} />
          <button type="button" className="btn-secondary btn-sm" disabled={!storage || uploading} onClick={() => fileRef.current?.click()}>
            {uploading && <Spinner className="h-3 w-3" />} Загрузить фото
          </button>
          <p className="mt-1 text-xs text-muted">{storage ? "JPEG, PNG или WebP до 5 МБ" : "Хранилище файлов не настроено"}</p>
        </div>
      </div>
      <div>
        <label className="label" htmlFor="headline">Коротко о себе</label>
        <input id="headline" className="input" maxLength={120} placeholder="Например: математика для 5–11 классов" value={headline} onChange={(e) => setHeadline(e.target.value)} />
      </div>
      <div>
        <label className="label" htmlFor="bio">Описание</label>
        <textarea id="bio" className="input min-h-32" maxLength={3000} value={bio} onChange={(e) => setBio(e.target.value)} />
      </div>
      <div>
        <span className="label">Предметы</span>
        <div className="mb-2 flex flex-wrap gap-2">
          {subjects.map((s) => (
            <span key={s} className="chip gap-2 bg-brand-50 py-1 text-sm text-brand-800">
              {s}
              <button type="button" aria-label={`Убрать ${s}`} className="text-brand-400 hover:text-red-600" onClick={() => setSubjects(subjects.filter((x) => x !== s))}>
                ✕
              </button>
            </span>
          ))}
          {subjects.length === 0 && <span className="text-sm text-muted">Добавьте хотя бы один предмет</span>}
        </div>
        <div className="flex gap-2">
          <input
            className="input"
            list="subj-list"
            placeholder="Предмет"
            value={newSubject}
            maxLength={40}
            onChange={(e) => setNewSubject(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addSubject(newSubject);
              }
            }}
          />
          <datalist id="subj-list">
            {subjectsAll.map((s) => (
              <option key={s} value={s} />
            ))}
          </datalist>
          <button type="button" className="btn-secondary" onClick={() => addSubject(newSubject)}>
            Добавить
          </button>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="price">{`Стоимость 60 минут, ${CURRENCY.symbol}`}</label>
          <input id="price" type="number" min={0} step={500} className="input" required value={price} onChange={(e) => setPrice(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="exp">Опыт, лет</label>
          <input id="exp" type="number" min={0} max={70} className="input" value={exp} onChange={(e) => setExp(e.target.value)} />
        </div>
      </div>
      {error && <ErrorBox message={error} />}
      <button className="btn-primary" disabled={busy}>
        {busy && <Spinner className="h-4 w-4 text-white" />} Сохранить профиль
      </button>
    </form>
  );
}

export default function ProfilePage() {
  return (
    <Suspense>
      <ProfileInner />
    </Suspense>
  );
}
