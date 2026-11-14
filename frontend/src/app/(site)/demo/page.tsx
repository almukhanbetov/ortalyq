"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { api, ApiError, DemoState, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { DEMO_INTRO, demoTempNotice } from "@/lib/demo";
import { Spinner } from "@/components/ui";

export default function DemoPage() {
  const router = useRouter();
  const { config } = useAuth();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<"solo" | "pair" | null>(null);
  const [error, setError] = useState("");
  const minutes = config?.demo?.minutes ?? 15;
  const disabled = config?.demo && !config.demo.enabled ? "Пробный урок отключён на этом сервере" : "";

  const start = async (mode: "solo" | "pair") => {
    if (busy) return;
    setBusy(mode);
    setError("");
    try {
      const st = await api<DemoState>("/api/demo/rooms", { method: "POST", json: { mode, name } });
      router.push(`/demo/room/${st.id}`);
    } catch (e) {
      setError(e instanceof ApiError && e.status === 0 ? "Нет соединения с сервером. Проверьте интернет и попробуйте снова." : errorMessage(e));
      setBusy(null);
    }
  };

  const facts: { icon: string; text: string; warn?: boolean }[] = [
    { icon: "⏱", text: `${minutes} минут по времени сервера — остаток виден в классе, за 2 минуты придёт предупреждение` },
    { icon: "🆓", text: "Без регистрации и оплаты. Расписание, цены и бронирования не меняются" },
    { icon: "🗑️", text: "Материалы временные: доска, текст, код и чат удаляются после демонстрации" },
    { icon: "⏺", text: "Запись видео в пробном уроке отключена" },
    { icon: "🤖", text: "ИИ в пробном уроке не вызывается — показан заранее подготовленный «Пример результата»" },
  ];
  if (config && !config.videoConfigured) facts.push({ icon: "📵", text: "Видеосвязь на этом сервере не настроена — будут работать доска, текст, код и чат", warn: true });
  if (config && !config.codeRunnerConfigured) facts.push({ icon: "💻", text: "Исполнитель кода не настроен — код можно писать вместе, но не запускать", warn: true });
  else if (config?.demo && config.demo.codeRuns === 0) facts.push({ icon: "💻", text: "Запуск кода в пробном уроке отключён — код можно писать вместе", warn: true });
  else if (config?.demo) facts.push({ icon: "💻", text: `Код Python можно запустить до ${config.demo.codeRuns} раз на изолированном сервере` });

  return (
    <div className="mx-auto max-w-4xl space-y-8">
      <section className="rounded-3xl bg-gradient-to-br from-brand-600 via-brand-700 to-indigo-700 px-6 py-10 text-white sm:px-10">
        <span className="chip mb-3 bg-white/15 text-white">Пробный урок</span>
        <h1 className="text-2xl font-bold leading-tight sm:text-4xl">Попробуйте виртуальный класс</h1>
        <p className="mt-3 max-w-2xl text-base text-white/90 sm:text-lg" data-testid="demo-intro">
          {DEMO_INTRO}.
        </p>
      </section>

      <div className="flex gap-2 rounded-2xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900" role="note" data-testid="demo-temp-notice">
        <span aria-hidden>⏱</span>
        <span>{demoTempNotice(minutes)}.</span>
      </div>

      <section className="grid gap-4 md:grid-cols-2">
        <div className="card flex flex-col p-6">
          <div className="text-2xl" aria-hidden>
            👤
          </div>
          <h2 className="mt-2 text-lg font-semibold">Посмотреть самостоятельно</h2>
          <p className="mt-1 flex-1 text-sm text-muted">Отдельный класс только для вас. Вы в роли преподавателя: доступны все инструменты.</p>
          <button className="btn-primary mt-4 w-full py-3" onClick={() => start("solo")} disabled={!!busy || !!disabled} title={disabled || undefined} data-testid="demo-solo">
            {busy === "solo" && <Spinner className="h-4 w-4 text-white" />} Посмотреть самостоятельно
          </button>
        </div>
        <div className="card flex flex-col p-6">
          <div className="text-2xl" aria-hidden>
            👥
          </div>
          <h2 className="mt-2 text-lg font-semibold">Попробовать вдвоём</h2>
          <p className="mt-1 flex-1 text-sm text-muted">
            Отдельная комната и ссылка-приглашение для второго участника. Вы — «преподаватель», приглашённый — «ученик». Войти могут только двое.
          </p>
          <button className="btn-secondary mt-4 w-full py-3" onClick={() => start("pair")} disabled={!!busy || !!disabled} title={disabled || undefined} data-testid="demo-pair">
            {busy === "pair" && <Spinner className="h-4 w-4" />} Попробовать вдвоём
          </button>
        </div>
      </section>

      <div className="card space-y-3 p-5">
        <label className="label" htmlFor="demo-name">
          Как вас называть в классе <span className="font-normal text-muted">(необязательно)</span>
        </label>
        <input id="demo-name" className="input" maxLength={40} placeholder="Например, Айгерим" value={name} onChange={(e) => setName(e.target.value)} autoComplete="given-name" />
        {disabled && <p className="text-sm text-amber-800">{disabled}.</p>}
        {error && (
          <div className="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-800" role="alert" data-testid="demo-error">
            {error}
          </div>
        )}
      </div>

      <section className="card p-5">
        <h2 className="mb-3 font-semibold">Что важно знать</h2>
        <ul className="space-y-2 text-sm">
          {facts.map((f) => (
            <li key={f.text} className={`flex gap-2 ${f.warn ? "text-amber-900" : ""}`}>
              <span aria-hidden>{f.icon}</span>
              <span>{f.text}</span>
            </li>
          ))}
        </ul>
        <p className="mt-4 text-xs text-muted">
          Понравилось? <Link href="/teachers">Найдите преподавателя</Link> и запишитесь на настоящее занятие.
        </p>
      </section>
    </div>
  );
}
