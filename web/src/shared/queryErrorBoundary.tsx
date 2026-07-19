// web/src/shared/queryErrorBoundary.tsx
import { useEffect } from "react";
import type { ReactNode } from "react";
import { ApiRequestError } from "../api/client";
import { useAuth } from "../auth/AuthContext";
import AccessDenied from "../app/AccessDenied";
import NotFound from "../app/NotFound";
import ServiceUnavailable from "../app/ServiceUnavailable";

export type QueryErrorStatus = 401 | 403 | 404 | 503;

const HANDLED_STATUSES: readonly QueryErrorStatus[] = [401, 403, 404, 503];

export function classifyQueryError(error: unknown): QueryErrorStatus | null {
  if (!(error instanceof ApiRequestError)) {
    return null;
  }
  return (HANDLED_STATUSES as readonly number[]).includes(error.status) ? (error.status as QueryErrorStatus) : null;
}

export function useQueryErrorBoundary(error: unknown): ReactNode | null {
  const { logout } = useAuth();
  const status = classifyQueryError(error);

  useEffect(() => {
    if (status === 401) {
      logout();
    }
  }, [status, logout]);

  switch (status) {
    case 401:
      return null;
    case 403:
      return <AccessDenied />;
    case 404:
      return <NotFound />;
    case 503:
      return <ServiceUnavailable />;
    default:
      return null;
  }
}
