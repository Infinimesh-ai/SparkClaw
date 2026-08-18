import { useEffect, useRef, useState } from "react";

type AsyncActionOptions = {
  clearError?: () => void;
  onError?: (error: unknown) => void;
};

export function useAsyncAction({ clearError, onError }: AsyncActionOptions = {}) {
  const [busy, setBusy] = useState("");
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  async function run<T>(action: string, task: () => Promise<T>): Promise<T | undefined> {
    if (busy) return undefined;
    setBusy(action);
    clearError?.();
    try {
      return await task();
    } catch (error) {
      if (mountedRef.current) onError?.(error);
      return undefined;
    } finally {
      if (mountedRef.current) setBusy("");
    }
  }

  return { busy, run };
}
