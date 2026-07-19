// @vitest-environment jsdom
import { renderHook, act } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useAuth } from "./AuthContext";
import MockAuthProvider from "./MockAuthProvider";
import { mockUsers } from "./mockUsers";

function wrapper({ children }: { children: ReactNode }) {
  return <MockAuthProvider>{children}</MockAuthProvider>;
}

describe("MockAuthProvider", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("starts authenticated with the default mock user", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });

    expect(result.current.status).toBe("authenticated");
    expect(result.current.me).toEqual(mockUsers.operator);
  });

  it("starts authenticated with the mock user configured via VITE_TFLIVE_MOCK_USER_ROLE", async () => {
    vi.stubEnv("VITE_TFLIVE_MOCK_USER_ROLE", "admin");
    vi.resetModules();
    const { useAuth: freshUseAuth } = await import("./AuthContext");
    const { default: FreshMockAuthProvider } = await import("./MockAuthProvider");
    const freshWrapper = ({ children }: { children: ReactNode }) => (
      <FreshMockAuthProvider>{children}</FreshMockAuthProvider>
    );

    const { result } = renderHook(() => freshUseAuth(), { wrapper: freshWrapper });

    expect(result.current.me).toEqual(mockUsers.admin);
  });

  it("logout clears the user and flips status to unauthenticated", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });

    act(() => result.current.logout());

    expect(result.current.status).toBe("unauthenticated");
    expect(result.current.me).toBeNull();
  });

  it("login restores the mock user after logout", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });

    act(() => result.current.logout());
    act(() => result.current.login());

    expect(result.current.status).toBe("authenticated");
    expect(result.current.me).toEqual(mockUsers.operator);
  });

  it("keeps login/logout stable across re-renders, including across a logout/login cycle", () => {
    const { result, rerender } = renderHook(() => useAuth(), { wrapper });

    const { login: loginBeforeRerender, logout: logoutBeforeRerender } = result.current;
    rerender();
    expect(result.current.login).toBe(loginBeforeRerender);
    expect(result.current.logout).toBe(logoutBeforeRerender);

    act(() => result.current.logout());
    expect(result.current.logout).toBe(logoutBeforeRerender);

    act(() => result.current.login());
    expect(result.current.login).toBe(loginBeforeRerender);
  });
});
