"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, PublicConfig, User } from "./api";
import { browserTimezone } from "./format";

interface AuthState {
  user: User | null;
  loading: boolean;
  config: PublicConfig | null;
  tz: string;
  refresh: () => Promise<void>;
  setUser: (u: User | null) => void;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [config, setConfig] = useState<PublicConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [fallbackTz, setFallbackTz] = useState("Asia/Almaty");

  const refresh = useCallback(async () => {
    try {
      const r = await api<{ user: User | null }>("/api/auth/me");
      setUser(r.user);
    } catch {
      setUser(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setFallbackTz(browserTimezone());
    refresh();
    api<PublicConfig>("/api/config").then(setConfig).catch(() => setConfig(null));
  }, [refresh]);

  const logout = useCallback(async () => {
    try {
      await api("/api/auth/logout", { method: "POST" });
    } finally {
      setUser(null);
    }
  }, []);

  return (
    <Ctx.Provider value={{ user, loading, config, tz: user?.timezone || fallbackTz, refresh, setUser, logout }}>
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  const v = useContext(Ctx);
  if (!v) throw new Error("useAuth outside AuthProvider");
  return v;
}

/** Требует входа (и при необходимости роли); иначе перенаправляет на /login. */
export function useRequireUser(roles?: User["role"][]) {
  const auth = useAuth();
  const router = useRouter();
  useEffect(() => {
    if (auth.loading) return;
    if (!auth.user) {
      const next = typeof window !== "undefined" ? window.location.pathname + window.location.search : "/";
      router.replace(`/login?next=${encodeURIComponent(next)}`);
    } else if (roles && !roles.includes(auth.user.role)) {
      router.replace("/dashboard");
    }
  }, [auth.loading, auth.user, roles, router]);
  const allowed = !!auth.user && (!roles || roles.includes(auth.user.role));
  return { ...auth, ready: !auth.loading && allowed };
}
