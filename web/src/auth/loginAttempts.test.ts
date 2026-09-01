// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  clearLoginAttempts,
  loginLoopDetected,
  maxLoginAttempts,
  readLoginAttempts,
  recordLoginAttempt
} from "./loginAttempts";

// The module keeps a per-page-load flag, so each test starts from a fresh copy.
async function freshModule() {
  vi.resetModules();
  return import("./loginAttempts");
}

describe("loginAttempts", () => {
  beforeEach(() => {
    sessionStorage.clear();
    clearLoginAttempts();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("starts at zero with the loop undetected", () => {
    expect(readLoginAttempts()).toBe(0);
    expect(loginLoopDetected()).toBe(false);
  });

  it("counts one attempt per page load, however many callers ask", () => {
    expect(recordLoginAttempt()).toBe(1);
    expect(recordLoginAttempt()).toBe(1);
    expect(readLoginAttempts()).toBe(1);
  });

  it("survives a page reload, which is what an in-memory guard cannot do", async () => {
    recordLoginAttempt();
    const reloaded = await freshModule();
    expect(reloaded.readLoginAttempts()).toBe(1);
    expect(reloaded.recordLoginAttempt()).toBe(2);
  });

  it("detects the loop once the attempts are spent", async () => {
    let module = await freshModule();
    for (let attempt = 0; attempt < maxLoginAttempts; attempt += 1) {
      expect(module.loginLoopDetected()).toBe(false);
      module.recordLoginAttempt();
      module = await freshModule();
    }
    expect(module.readLoginAttempts()).toBe(maxLoginAttempts);
    expect(module.loginLoopDetected()).toBe(true);
  });

  it("clears the count so a later sign-in starts fresh", () => {
    recordLoginAttempt();
    clearLoginAttempts();
    expect(readLoginAttempts()).toBe(0);
    expect(sessionStorage.getItem("tflive.auth.loginAttempts")).toBeNull();
  });

  it("ignores a corrupted stored value", () => {
    sessionStorage.setItem("tflive.auth.loginAttempts", "not-a-number");
    expect(readLoginAttempts()).toBe(0);
  });

  it("falls back to an in-memory count when sessionStorage throws", async () => {
    vi.stubGlobal("sessionStorage", {
      getItem: () => {
        throw new Error("access denied");
      },
      setItem: () => {
        throw new Error("access denied");
      },
      removeItem: () => {
        throw new Error("access denied");
      }
    });
    const module = await freshModule();
    expect(module.readLoginAttempts()).toBe(0);
    expect(module.recordLoginAttempt()).toBe(1);
    expect(module.readLoginAttempts()).toBe(1);
  });
});
