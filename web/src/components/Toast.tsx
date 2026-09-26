import React, { createContext, useCallback, useContext, useState } from "react";

type ToastKind = "success" | "error" | "info";
interface ToastItem {
  id: number;
  kind: ToastKind;
  text: string;
}

interface ToastCtx {
  toast: (text: string, kind?: ToastKind) => void;
}

const Ctx = createContext<ToastCtx>({ toast: () => {} });

export function useToast() {
  return useContext(Ctx);
}

let seq = 1;

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);

  const toast = useCallback((text: string, kind: ToastKind = "info") => {
    const id = seq++;
    setItems((cur) => [...cur, { id, kind, text }]);
    window.setTimeout(() => {
      setItems((cur) => cur.filter((t) => t.id !== id));
    }, 4000);
  }, []);

  return (
    <Ctx.Provider value={{ toast }}>
      {children}
      <div className="pointer-events-none fixed right-4 top-4 z-[100] flex flex-col gap-2">
        {items.map((t) => (
          <div
            key={t.id}
            className={
              "pointer-events-auto max-w-sm rounded-lg px-4 py-2.5 text-sm text-white shadow-lg " +
              (t.kind === "success"
                ? "bg-emerald-600"
                : t.kind === "error"
                ? "bg-rose-600"
                : "bg-slate-800")
            }
          >
            {t.text}
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}
