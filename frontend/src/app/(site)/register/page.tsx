"use client";

import Link from "next/link";
import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, errorMessage, User } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { browserTimezone } from "@/lib/format";
import { ErrorBox, Spinner } from "@/components/ui";

function RegisterForm() {
  const router = useRouter();
  const params = useSearchParams();
  const { setUser } = useAuth();
  const [role, setRole] = useState<"student" | "teacher">(params.get("role") === "teacher" ? "teacher" : "student");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [tz, setTz] = useState("");
  useEffect(() => setTz(browserTimezone()), []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await api<{ user: User }>("/api/auth/register", {
        method: "POST",
        json: { name, email, password, role, timezone: tz || browserTimezone() },
      });
      setUser(r.user);
      router.replace(role === "teacher" ? "/profile?welcome=1" : "/teachers");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-md">
      <div className="card p-6 sm:p-8">
        <h1 className="text-2xl font-bold">Регистрация</h1>
        <form onSubmit={submit} className="mt-6 space-y-4">
          <div>
            <span className="label">Я —</span>
            <div className="grid grid-cols-2 gap-2" role="radiogroup">
              {(["student", "teacher"] as const).map((r) => (
                <button
                  key={r}
                  type="button"
                  role="radio"
                  aria-checked={role === r}
                  onClick={() => setRole(r)}
                  className={`rounded-xl border px-4 py-3 text-sm font-medium transition ${
                    role === r ? "border-brand-500 bg-brand-50 text-brand-800 ring-4 ring-brand-100" : "border-line bg-white text-muted hover:border-brand-300"
                  }`}
                >
                  {r === "student" ? "🎒 Ученик" : "🧑‍🏫 Преподаватель"}
                </button>
              ))}
            </div>
          </div>
          <div>
            <label className="label" htmlFor="name">Имя и фамилия</label>
            <input id="name" className="input" required minLength={2} maxLength={80} autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div>
            <label className="label" htmlFor="email">Email</label>
            <input id="email" className="input" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div>
            <label className="label" htmlFor="password">Пароль</label>
            <input id="password" className="input" type="password" autoComplete="new-password" required minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} />
            <p className="mt-1 text-xs text-muted">Не менее 8 символов</p>
          </div>
          {error && <ErrorBox message={error} />}
          <button className="btn-primary w-full" disabled={busy}>
            {busy && <Spinner className="h-4 w-4 text-white" />} Создать аккаунт
          </button>
          <p className="text-xs text-muted">Часовой пояс определится автоматически{tz ? `: ${tz}` : ""}. Его можно изменить в профиле.</p>
        </form>
        <p className="mt-6 text-center text-sm text-muted">
          Уже есть аккаунт?{" "}
          <Link href="/login" className="font-medium text-brand-700 hover:underline">
            Войти
          </Link>
        </p>
      </div>
    </div>
  );
}

export default function RegisterPage() {
  return (
    <Suspense>
      <RegisterForm />
    </Suspense>
  );
}
