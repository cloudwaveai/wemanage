import React, { createContext, useContext, useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, setCsrfToken } from "../lib/api";
import type { Me } from "../lib/types";

interface AuthCtx {
  me: Me | null;
  loading: boolean;
  error: unknown;
  refetch: () => void;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthCtx>({
  me: null,
  loading: true,
  error: null,
  refetch: () => {},
  logout: async () => {},
});

export function useAuth() {
  return useContext(Ctx);
}

async function fetchMe(): Promise<Me | null> {
  try {
    const me = await api.get<Me>("/api/me");
    setCsrfToken(me.csrf);
    return me;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return null;
    throw e;
  }
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const qc = useQueryClient();
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["me"],
    queryFn: fetchMe,
    retry: false,
  });

  useEffect(() => {
    if (data?.csrf) setCsrfToken(data.csrf);
  }, [data?.csrf]);

  const logout = async () => {
    try {
      await api.post("/api/auth/logout");
    } catch {
      /* ignore */
    }
    // Inside Feishu the login page signs in by itself; not right after a logout.
    try {
      sessionStorage.setItem("aiseat_logged_out", "1");
    } catch {
      /* storage unavailable */
    }
    qc.clear();
    await refetch();
  };

  return (
    <Ctx.Provider
      value={{
        me: data ?? null,
        loading: isLoading,
        error,
        refetch: () => {
          void refetch();
        },
        logout,
      }}
    >
      {children}
    </Ctx.Provider>
  );
}
