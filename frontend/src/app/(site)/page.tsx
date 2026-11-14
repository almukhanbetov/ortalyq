"use client";

import Link from "next/link";
import { useAuth } from "@/lib/auth";

const steps = [
  { icon: "🔎", title: "Найдите преподавателя", text: "Фильтры по предмету и стоимости, отзывы учеников и описание подхода." },
  { icon: "🗓️", title: "Запишитесь на время", text: "Свободные окна преподавателя в вашем часовом поясе. Двойная запись исключена." },
  { icon: "🎥", title: "Занимайтесь в видеоклассе", text: "Видео и звук, демонстрация экрана, чат и общая интерактивная доска." },
  { icon: "⏺️", title: "Пересматривайте урок", text: "Запись с согласия обоих участников, доступна только вам и преподавателю." },
  { icon: "📝", title: "Выполняйте задания", text: "Домашняя работа, ответ текстом или файлом, комментарий преподавателя." },
];

export default function Home() {
  const { user } = useAuth();
  return (
    <div className="space-y-14">
      <section className="relative overflow-hidden rounded-3xl bg-gradient-to-br from-brand-600 via-brand-700 to-indigo-700 px-6 py-12 text-white sm:px-12 sm:py-16">
        <div className="absolute -right-24 -top-24 h-72 w-72 rounded-full bg-white/10 blur-2xl" />
        <div className="absolute -bottom-32 right-24 h-72 w-72 rounded-full bg-fuchsia-400/20 blur-3xl" />
        <div className="relative max-w-2xl">
          <span className="chip mb-4 bg-white/15 text-white">Онлайн-занятия один на один</span>
          <h1 className="text-3xl font-bold leading-tight tracking-tight sm:text-5xl">
            Учитесь с преподавателем — на одной доске, из любого места
          </h1>
          <p className="mt-4 text-base text-white/85 sm:text-lg">
            Видеокласс с интерактивной доской, записью урока и домашними заданиями. Всё в одном окне браузера — без установки программ.
          </p>
          <div className="mt-8 flex flex-wrap gap-3">
            <Link href="/demo" className="btn bg-amber-300 px-6 py-3 text-ink shadow-lg hover:bg-amber-200" data-testid="home-try-demo">
              ▶ Попробовать урок
            </Link>
            <Link href="/teachers" className="btn bg-white px-6 py-3 text-brand-700 hover:bg-brand-50">
              Найти преподавателя
            </Link>
            {user ? (
              <Link href="/dashboard" className="btn border border-white/40 px-6 py-3 text-white hover:bg-white/10">
                Перейти в кабинет
              </Link>
            ) : (
              <Link href="/register?role=teacher" className="btn border border-white/40 px-6 py-3 text-white hover:bg-white/10">
                Я преподаватель
              </Link>
            )}
          </div>
        </div>
      </section>

      <section className="card flex flex-col gap-4 p-6 sm:flex-row sm:items-center">
        <div className="text-3xl" aria-hidden>
          🧪
        </div>
        <div className="flex-1">
          <h2 className="text-lg font-semibold">Пробный урок — 15 минут без регистрации</h2>
          <p className="mt-1 text-sm text-muted">
            Видеосвязь, общая доска, совместный код и учебная мастерская в отдельном классе. Самостоятельно или вдвоём по ссылке. Бесплатно, без бронирования.
          </p>
        </div>
        <Link href="/demo" className="btn-primary shrink-0">
          Попробовать урок
        </Link>
      </section>

      <section>
        <h2 className="mb-6 text-xl font-bold sm:text-2xl">Как это работает</h2>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
          {steps.map((s, i) => (
            <div key={s.title} className="card p-5">
              <div className="mb-3 flex items-center justify-between">
                <span className="text-2xl" aria-hidden>
                  {s.icon}
                </span>
                <span className="text-xs font-semibold text-brand-400">0{i + 1}</span>
              </div>
              <div className="font-semibold">{s.title}</div>
              <p className="mt-1 text-sm text-muted">{s.text}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="grid gap-4 md:grid-cols-2">
        <div className="card p-6">
          <h3 className="text-lg font-semibold">Ученикам</h3>
          <p className="mt-2 text-sm text-muted">
            Выберите преподавателя в каталоге или опубликуйте заявку на доске объявлений — преподаватели сами откликнутся.
          </p>
          <Link href="/board" className="btn-secondary mt-4">
            Доска объявлений
          </Link>
        </div>
        <div className="card p-6">
          <h3 className="text-lg font-semibold">Преподавателям</h3>
          <p className="mt-2 text-sm text-muted">
            Заполните профиль, задайте удобные интервалы — ученики запишутся сами. Материалы, записи и задания хранятся у каждого урока.
          </p>
          <Link href={user ? "/availability" : "/register?role=teacher"} className="btn-secondary mt-4">
            {user ? "Настроить расписание" : "Стать преподавателем"}
          </Link>
        </div>
      </section>
    </div>
  );
}
