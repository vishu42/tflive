import { describe, expect, it } from "vitest";
import type { User } from "oidc-client-ts";
import { convertUserToMe } from "./convertUser";

function user(overrides: Partial<{
  sub: string;
  preferred_username: string;
  email: string;
  realmRoles: string[];
}> = {}): User {
  const { sub, preferred_username, email, realmRoles } = {
    sub: "user-abc-123",
    preferred_username: "ada",
    email: "ada@test.local",
    realmRoles: [],
    ...overrides,
  };
  return {
    id_token: "",
    session_state: null,
    access_token: "at",
    refresh_token: "rt",
    token_type: "Bearer",
    scope: "openid",
    profile: {
      sub,
      preferred_username,
      email,
      realm_access: { roles: realmRoles },
    } as Record<string, unknown>,
    expires_at: Date.now() / 1000 + 300,
    state: null,
  } as unknown as User;
}

describe("convertUserToMe", () => {
  it("maps sub from the token profile", () => {
    const result = convertUserToMe(user({ sub: "kc-001" }));
    expect(result.sub).toBe("kc-001");
  });

  it("uses preferred_username as displayName", () => {
    const result = convertUserToMe(user({ preferred_username: "alice" }));
    expect(result.displayName).toBe("alice");
  });

  it("falls back to sub when preferred_username is missing", () => {
    const u = user({ preferred_username: undefined as unknown as string, sub: "kc-no-name" });
    const result = convertUserToMe(u);
    expect(result.displayName).toBe("kc-no-name");
  });

  it("maps email from the token profile", () => {
    const result = convertUserToMe(user({ email: "alice@test.local" }));
    expect(result.email).toBe("alice@test.local");
  });

  it("resolves platform-admin capability when realm role is present", () => {
    const result = convertUserToMe(user({ realmRoles: ["platform-admin"] }));
    expect(result.globalCapabilities.isPlatformAdmin).toBe(true);
    expect(result.globalCapabilities.canCreateStack).toBe(true);
  });

  it("resolves stack-creator capability when only that realm role is present", () => {
    const result = convertUserToMe(user({ realmRoles: ["stack-creator"] }));
    expect(result.globalCapabilities.isPlatformAdmin).toBe(false);
    expect(result.globalCapabilities.canCreateStack).toBe(true);
  });

  it("returns false for all capabilities when no realm roles are present", () => {
    const result = convertUserToMe(user());
    expect(result.globalCapabilities.isPlatformAdmin).toBe(false);
    expect(result.globalCapabilities.canCreateStack).toBe(false);
  });
});
