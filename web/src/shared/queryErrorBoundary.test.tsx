// web/src/shared/queryErrorBoundary.test.tsx
// @vitest-environment jsdom
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { AuthContext } from "../auth/AuthContext";
import type { AuthContextValue } from "../auth/AuthContext";
import MockAuthProvider from "../auth/MockAuthProvider";
import { useAuth } from "../auth/AuthContext";
import { ApiRequestError } from "../api/client";
import { classifyQueryError, useQueryErrorBoundary } from "./queryErrorBoundary";
import AccessDenied from "../app/AccessDenied";
import NotFound from "../app/NotFound";
import ServiceUnavailable from "../app/ServiceUnavailable";

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "s1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides
  };
}

function wrapper(auth: AuthContextValue) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <AuthContext.Provider value={auth}>{children}</AuthContext.Provider>;
  };
}

describe("classifyQueryError", () => {
  it.each([401, 403, 404, 503] as const)("classifies a %d ApiRequestError", (status) => {
    expect(classifyQueryError(new ApiRequestError(status, "code", "message"))).toBe(status);
  });

  it("returns null for an unhandled status", () => {
    expect(classifyQueryError(new ApiRequestError(500, "server_error", "boom"))).toBeNull();
  });

  it("returns null for non-ApiRequestError values", () => {
    expect(classifyQueryError(new Error("boom"))).toBeNull();
    expect(classifyQueryError(undefined)).toBeNull();
    expect(classifyQueryError(null)).toBeNull();
  });
});

describe("useQueryErrorBoundary", () => {
  it("returns null and does not call logout for a non-error", () => {
    const logout = vi.fn();
    const { result } = renderHook(() => useQueryErrorBoundary(undefined), { wrapper: wrapper(authValue({ logout })) });

    expect(result.current).toBeNull();
    expect(logout).not.toHaveBeenCalled();
  });

  it("renders AccessDenied for a 403 without calling logout", () => {
    const logout = vi.fn();
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(403, "forbidden", "nope")), {
      wrapper: wrapper(authValue({ logout }))
    });

    // toMatchObject (not toEqual) here: an element created during this hook's render carries a
    // non-null `_owner` fiber reference, while one created in the test body does not — toEqual
    // would fail on that internal field alone even though type/props match.
    expect(result.current).toMatchObject({ type: AccessDenied, props: {} });
    expect(logout).not.toHaveBeenCalled();
  });

  it("renders NotFound for a 404", () => {
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(404, "not_found", "nope")), {
      wrapper: wrapper(authValue())
    });

    expect(result.current).toMatchObject({ type: NotFound, props: {} });
  });

  it("renders ServiceUnavailable for a 503", () => {
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(503, "unavailable", "down")), {
      wrapper: wrapper(authValue())
    });

    expect(result.current).toMatchObject({ type: ServiceUnavailable, props: {} });
  });

  it("returns null for an unrecognized status without calling logout", () => {
    const logout = vi.fn();
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(500, "server_error", "boom")), {
      wrapper: wrapper(authValue({ logout }))
    });

    expect(result.current).toBeNull();
    expect(logout).not.toHaveBeenCalled();
  });

  it("triggers logout exactly once for a 401, even across re-renders with the same error", async () => {
    const logout = vi.fn();
    const { result, rerender } = renderHook(({ error }: { error: unknown }) => useQueryErrorBoundary(error), {
      initialProps: { error: new ApiRequestError(401, "unauthorized", "expired") as unknown },
      wrapper: wrapper(authValue({ logout }))
    });

    await waitFor(() => expect(logout).toHaveBeenCalledTimes(1));
    expect(result.current).toBeNull();

    rerender({ error: new ApiRequestError(401, "unauthorized", "expired") });
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("end-to-end with the real MockAuthProvider: a 401 flips status to unauthenticated without looping", async () => {
    function useHarness(error: unknown) {
      const auth = useAuth();
      const boundary = useQueryErrorBoundary(error);
      return { auth, boundary };
    }

    const realWrapper = ({ children }: { children: ReactNode }) => <MockAuthProvider>{children}</MockAuthProvider>;
    const { result } = renderHook(() => useHarness(new ApiRequestError(401, "unauthorized", "expired")), {
      wrapper: realWrapper
    });

    await waitFor(() => expect(result.current.auth.status).toBe("unauthenticated"));
    expect(result.current.auth.me).toBeNull();
    expect(result.current.boundary).toBeNull();
  });
});
