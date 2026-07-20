import { describe, expect, it } from "vitest";
import { oidcConfig } from "./oidcConfig";
import { getUserManager } from "./userManager";

describe("getUserManager", () => {
  it("returns the same instance on repeated calls", () => {
    const first = getUserManager();
    const second = getUserManager();
    expect(first).toBe(second);
  });

  it("returns a UserManager with the configured authority", async () => {
    const manager = getUserManager();
    // settings are available on the instance
    expect(manager.settings.authority).toContain("tflive");
  });

  it("uses PKCE (response_type is code)", () => {
    const manager = getUserManager();
    expect(manager.settings.response_type).toBe("code");
  });

  it("enables refresh token usage", () => {
    expect(oidcConfig.useRefreshToken).toBe(true);
  });

  it("does not load user info from /userinfo endpoint", () => {
    const manager = getUserManager();
    expect(manager.settings.loadUserInfo).toBe(false);
  });
});
