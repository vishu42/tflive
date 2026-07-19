// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { AuthContext, useAuth } from "./AuthContext";
import type { AuthContextValue } from "./AuthContext";

describe("useAuth", () => {
  it("throws when used outside an AuthProvider", () => {
    expect(() => renderHook(() => useAuth())).toThrow("useAuth must be used within an AuthProvider");
  });

  it("returns the value supplied by the nearest AuthContext.Provider", () => {
    const value: AuthContextValue = {
      me: { sub: "s1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
      status: "authenticated",
      login: () => {},
      logout: () => {}
    };
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
    );

    const { result } = renderHook(() => useAuth(), { wrapper });

    expect(result.current).toBe(value);
  });
});
