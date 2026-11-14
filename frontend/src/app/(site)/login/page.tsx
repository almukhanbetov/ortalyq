"use client";

import Link from "next/link";
import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, errorMessage, User } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { ErrorBox, PasswordInput, Spinner } from "@/components/ui";

function safeNext(v: string | null) {
  return v && v.startsWith("/") && !v.startsWith("//") ? v : "/dashboard";
}

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const { setUser } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await api<{ user: User }>("/api/auth/login", { method: "POST", json: { email, password } });
      setUser(r.user);
      router.replace(safeNext(params.get("next")));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-md">
      <div className="card p-6 sm:p-8">
        <h1 className="text-2xl font-bold">Вход</h1>
        <p className="mt-1 text-sm text-muted">Рады видеть вас снова</p>
        <form onSubmit={submit} className="mt-6 space-y-4">
          <div>
            <label className="label" htmlFor="email">Email</label>
            <input id="email" className="input" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div>
            <label className="label" htmlFor="password">Пароль</label>
            <PasswordInput id="password" name="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          {error && <ErrorBox message={error} />}
          <button className="btn-primary w-full" disabled={busy}>
            {busy && <Spinner className="h-4 w-4 text-white" />} Войти
          </button>
        </form>
        <p className="mt-6 text-center text-sm text-muted">
          Нет аккаунта?{" "}
          <Link href={params.get("next") ? `/register?next=${encodeURIComponent(params.get("next")!)}` : "/register"} className="font-medium text-brand-700 hover:underline">
            Зарегистрироваться
          </Link>
        </p>
      </div>
      <p className="mt-4 text-center text-xs text-muted">
        Для локальной демонстрации используйте демо-аккаунты из README (пароль задаётся переменной DEMO_PASSWORD).
      </p>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense>
      <LoginForm />
    </Suspense>
  );
}
