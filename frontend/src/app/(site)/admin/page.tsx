"use client";

import { useCallback, useEffect, useState } from "react";
import { api, errorMessage, Listing } from "@/lib/api";
import { useRequireUser } from "@/lib/auth";
import { formatDate } from "@/lib/format";
import { DemoBadge, Empty, ErrorBox, Loading, PageHeader, Skeleton, toast } from "@/components/ui";

interface AdminUser {
  id: string;
  email: string;
  name: string;
  role: string;
  isDemo: boolean;
  createdAt: string;
}

export default function AdminPage() {
  const { ready, tz } = useRequireUser(["admin"]);
  const [onlyHidden, setOnlyHidden] = useState(false);
  const [items, setItems] = useState<Listing[] | null>(null);
  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setItems(null);
    try {
      const [l, u] = await Promise.all([
        api<{ listings: Listing[] }>(`/api/admin/listings${onlyHidden ? "?hidden=true" : ""}`),
        api<{ users: AdminUser[] }>("/api/admin/users"),
      ]);
      setItems(l.listings);
      setUsers(u.users);
    } catch (e) {
      setError(errorMessage(e));
      setItems([]);
    }
  }, [onlyHidden]);
  useEffect(() => {
    if (ready) load();
  }, [ready, load]);

  const toggle = async (l: Listing) => {
    try {
      await api(`/api/admin/listings/${l.id}/${l.hidden ? "unhide" : "hide"}`, { method: "POST", json: { reason: "Нарушение правил площадки" } });
      toast(l.hidden ? "Объявление снова видно" : "Объявление скрыто");
      load();
    } catch (e) {
      toast(errorMessage(e), "err");
    }
  };

  if (!ready) return <Loading />;
  return (
    <div className="space-y-8">
      <PageHeader title="Модерация" subtitle="Скрытые объявления не показываются на доске" />
      {error && <ErrorBox message={error} onRetry={load} />}
      <section>
        <label className="mb-4 inline-flex items-center gap-2 text-sm">
          <input type="checkbox" checked={onlyHidden} onChange={(e) => setOnlyHidden(e.target.checked)} className="h-4 w-4 accent-brand-600" />
          Только скрытые
        </label>
        {items === null ? (
          <Skeleton className="h-40" />
        ) : items.length === 0 ? (
          <Empty icon="✅" title="Объявлений нет" />
        ) : (
          <div className="card divide-y divide-line">
            {items.map((l) => (
              <div key={l.id} className="flex flex-col gap-2 p-4 sm:flex-row sm:items-center">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{l.title}</span>
                    {l.isDemo && <DemoBadge />}
                    {l.hidden && <span className="chip bg-red-100 text-red-700">Скрыто</span>}
                  </div>
                  <div className="text-xs text-muted">
                    {l.kind === "offer" ? "Предложение" : "Заявка"} · {l.subject} · {l.authorName} · {formatDate(l.createdAt, tz)}
                  </div>
                </div>
                <button className={l.hidden ? "btn-secondary btn-sm" : "btn-danger btn-sm"} onClick={() => toggle(l)}>
                  {l.hidden ? "Вернуть" : "Скрыть"}
                </button>
              </div>
            ))}
          </div>
        )}
      </section>
      <section>
        <h2 className="mb-3 text-lg font-semibold">Пользователи</h2>
        {users === null ? (
          <Skeleton className="h-32" />
        ) : (
          <div className="card overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead className="text-xs text-muted">
                <tr>
                  <th className="p-3">Имя</th>
                  <th className="p-3">Email</th>
                  <th className="p-3">Роль</th>
                  <th className="p-3">Создан</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {users.map((u) => (
                  <tr key={u.id}>
                    <td className="p-3">{u.name}</td>
                    <td className="p-3 text-muted">{u.email}</td>
                    <td className="p-3">{{ student: "Ученик", teacher: "Преподаватель", admin: "Администратор" }[u.role] ?? u.role}</td>
                    <td className="p-3 text-muted">{formatDate(u.createdAt, tz)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
