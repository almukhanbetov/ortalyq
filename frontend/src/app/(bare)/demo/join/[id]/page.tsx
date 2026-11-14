"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { use, useEffect, useState } from "react";
import { api, ApiError, DemoState, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { DEMO_INTRO, demoTempNotice } from "@/lib/demo";
import { Spinner } from "@/components/ui";

// Вход второго участника по ссылке-приглашению /demo/join/<id>#<код>. Код передаётся во фрагменте адреса:
// он не попадает в журналы сервера и заголовок Referer. Приглашение принимается только по нажатию кнопки —
// предпросмотр ссылки в мессенджере не займёт место второго участника.
export default function DemoJoinPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const router = useRouter();
  const { config } = useAuth();
  const [token, setToken] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);

  useEffect(() => {
    const read = () => {
      setToken(decodeURIComponent(window.location.hash.replace(/^#/, "")));
      setError(null);
    };
    read();
    // Ссылку могут вставить в ту же вкладку повторно — меняется только фрагмент адреса.
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, []);

  const accept = async () => {
    if (!token || busy) return;
    setBusy(true);
    setError(null);
    try {
      await api<DemoState>(`/api/demo/rooms/${id}/accept`, { method: "POST", json: { token, name } });
      router.replace(`/demo/room/${id}`);
    } catch (e) {
      setError({
        status: e instanceof ApiError ? e.status : 0,
        message: e instanceof ApiError && e.status === 0 ? "Нет соединения с сервером. Проверьте интернет и попробуйте снова." : errorMessage(e),
      });
      setBusy(false);
    }
  };

  const final = error && [404, 409, 410].includes(error.status);
  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <div className="card w-full max-w-md space-y-4 p-6">
        <div>
          <span className="chip bg-brand-50 text-brand-800">Пробный урок</span>
          <h1 className="mt-2 text-xl font-bold">Вас пригласили на пробный урок</h1>
          <p className="mt-2 text-sm text-muted">{DEMO_INTRO}.</p>
        </div>
        <ul className="space-y-1 text-sm text-muted">
          <li>• Вы войдёте как «ученик», без регистрации.</li>
          <li>• Запись видео отключена.</li>
        </ul>
        <p className="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900" role="note" data-testid="demo-temp-notice">
          {demoTempNotice(config?.demo?.minutes ?? 15)}.
        </p>
        {token === "" && (
          <div className="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-800" role="alert">
            В ссылке нет кода приглашения. Попросите прислать ссылку целиком.
          </div>
        )}
        {!final && token !== "" && (
          <>
            <div>
              <label className="label" htmlFor="demo-guest-name">
                Ваше имя <span className="font-normal text-muted">(необязательно)</span>
              </label>
              <input id="demo-guest-name" className="input" maxLength={40} value={name} onChange={(e) => setName(e.target.value)} placeholder="Например, Данияр" />
            </div>
            <button className="btn-primary w-full py-3" onClick={accept} disabled={busy || token === null} data-testid="demo-accept">
              {busy && <Spinner className="h-4 w-4 text-white" />} Присоединиться
            </button>
          </>
        )}
        {error && (
          <div className="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-800" role="alert" data-testid="demo-join-error">
            {error.message}
          </div>
        )}
        <div className="flex flex-wrap gap-2 border-t border-line pt-4">
          <Link href="/demo" className={final ? "btn-primary flex-1" : "btn-ghost btn-sm"}>
            Начать свою демонстрацию
          </Link>
        </div>
      </div>
    </div>
  );
}
