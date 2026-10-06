import { useCallback, useEffect, useRef, useState } from "react";
import { api, APIError, clearAPIToken, hasConfiguredAPIToken, localAccessAvailable, onAPIUnauthorized } from "../api/client";
import type { WorkbenchIdentity } from "../api/types";

// The identity response is the only admission gate. A missing browser token
// does not imply either local authority or an authentication failure.
export function useWorkbenchAccess(enabled = true) {
  const [identity, setIdentity] = useState<WorkbenchIdentity | null>(null);
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const generation = useRef(0);

  const invalidate = useCallback((err: unknown) => {
    generation.current++;
    setIdentity(null);
    setConnecting(false);
    setError(err);
  }, []);

  const connect = useCallback(async (local = false) => {
    const current = ++generation.current;
    setIdentity(null);
    setError(null);
    setConnecting(true);
    try {
      if (local) {
        if (!localAccessAvailable() || hasConfiguredAPIToken()) {
          throw new APIError(403, "Local access is unavailable for this connection", "local_access_unavailable");
        }
        // Only this explicit user action removes the credential. Failed token
        // authentication never retries with an anonymous/local request.
        clearAPIToken();
      }
      const result = await api.workbenchIdentity();
      if (local && result.access_mode !== "local") {
        throw new APIError(403, "This service does not provide local access", "local_access_unavailable");
      }
      if (current === generation.current) setIdentity(result);
      return result;
    } catch (err) {
      if (current === generation.current) setError(err);
      throw err;
    } finally {
      if (current === generation.current) setConnecting(false);
    }
  }, []);

  useEffect(() => onAPIUnauthorized(invalidate), [invalidate]);

  useEffect(() => {
    if (enabled) void connect().catch(() => undefined);
    else setIdentity(null);
    return () => { generation.current++; };
  }, [connect, enabled]);

  return { identity, connecting, error, connect, invalidate };
}
