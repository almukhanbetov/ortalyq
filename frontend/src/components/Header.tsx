"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useState } from "react";
import { useAuth } from "@/lib/auth";

const roleLabel = { student: "Ученик", teacher: "Преподаватель", admin: "Администратор" } as const;

export function Logo() {
  return (
    <Link href="/" className="flex items-center gap-2 font-bold tracking-tight text-ink">
      <span className="flex h-8 w-8 items-center justify-center rounded-xl bg-gradient-to-br from-brand-500 to-brand-700 text-sm text-white">
        O
      </span>
      <span>ORTALYQ</span>
    </Link>
  );
}

export function Header() {
  const { user, loading, logout } = useAuth();
  const path = usePathname();
  const router = useRouter();
  const [open, setOpen] = useState(false);

  const links: { href: string; label: string }[] = [
    { href: "/teachers", label: "Преподаватели" },
    { href: "/board", label: "Объявления" },
    { href: "/demo", label: "Пробный урок" },
  ];
  if (user) {
    links.push({ href: "/dashboard", label: "Кабинет" });
    if (user.role !== "admin") links.push({ href: "/lessons", label: "Мои занятия" });
    if (user.role === "teacher") links.push({ href: "/availability", label: "Расписание" });
    if (user.role === "admin") links.push({ href: "/admin", label: "Модерация" });
  }

  const onLogout = async () => {
    await logout();
    setOpen(false);
    router.push("/");
  };

  const active = (href: string) => path === href || path.startsWith(href + "/");

  return (
    <header className="sticky top-0 z-40 border-b border-line bg-white/85 backdrop-blur">
      <div className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-4 px-4 sm:px-6">
        <Logo />
        <nav className="hidden items-center gap-1 md:flex">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              className={`rounded-lg px-3 py-2 text-sm font-medium transition ${
                active(l.href) ? "bg-brand-50 text-brand-700" : "text-muted hover:text-ink"
              }`}
            >
              {l.label}
            </Link>
          ))}
        </nav>
        <div className="hidden items-center gap-2 md:flex">
          {!active("/demo") && (
            <Link href="/demo" className="btn-secondary btn-sm border-brand-300 text-brand-700" data-testid="header-try-demo">
              ▶ Попробовать урок
            </Link>
          )}
          {loading ? null : user ? (
            <>
              <Link href="/profile" className="text-right text-sm leading-tight hover:text-brand-700">
                <div className="font-medium">{user.name}</div>
                <div className="text-xs text-muted">{roleLabel[user.role]}</div>
              </Link>
              <button className="btn-ghost btn-sm" onClick={onLogout}>
                Выйти
              </button>
            </>
          ) : (
            <>
              <Link href="/login" className="btn-ghost">
                Войти
              </Link>
              <Link href="/register" className="btn-primary">
                Регистрация
              </Link>
            </>
          )}
        </div>
        <Link href="/demo" className="btn-secondary btn-sm ml-auto px-3 md:hidden" data-testid="header-try-demo-mobile">
          ▶ Попробовать
        </Link>
        <button
          className="btn-ghost -mr-2 md:hidden"
          aria-label={open ? "Закрыть меню" : "Открыть меню"}
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            {open ? <path d="M6 6l12 12M18 6L6 18" /> : <path d="M4 7h16M4 12h16M4 17h16" />}
          </svg>
        </button>
      </div>
      {open && (
        <div className="border-t border-line bg-white px-4 pb-4 md:hidden">
          <nav className="flex flex-col py-2">
            {links.map((l) => (
              <Link
                key={l.href}
                href={l.href}
                onClick={() => setOpen(false)}
                className={`rounded-lg px-3 py-3 text-sm font-medium ${active(l.href) ? "bg-brand-50 text-brand-700" : "text-ink"}`}
              >
                {l.label}
              </Link>
            ))}
            {user && (
              <Link href="/profile" onClick={() => setOpen(false)} className="rounded-lg px-3 py-3 text-sm font-medium">
                Профиль
              </Link>
            )}
          </nav>
          {user ? (
            <div className="flex items-center justify-between border-t border-line pt-3">
              <div className="text-sm">
                <div className="font-medium">{user.name}</div>
                <div className="text-xs text-muted">{roleLabel[user.role]}</div>
              </div>
              <button className="btn-secondary btn-sm" onClick={onLogout}>
                Выйти
              </button>
            </div>
          ) : (
            <div className="flex gap-2 border-t border-line pt-3">
              <Link href="/login" className="btn-secondary flex-1" onClick={() => setOpen(false)}>
                Войти
              </Link>
              <Link href="/register" className="btn-primary flex-1" onClick={() => setOpen(false)}>
                Регистрация
              </Link>
            </div>
          )}
        </div>
      )}
    </header>
  );
}
