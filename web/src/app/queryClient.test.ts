import { describe, expect, it } from "vitest";
import { nextPollDelayMs } from "../api/polling";
import { createQueryClient } from "./queryClient";

describe("createQueryClient", () => {
  it("retries failed queries a bounded number of times", () => {
    const queryClient = createQueryClient();
    const { retry } = queryClient.getDefaultOptions().queries ?? {};
    expect(retry).toBe(3);
  });

  it("reuses polling.ts's backoff curve as the retry delay", () => {
    const queryClient = createQueryClient();
    const { retryDelay } = queryClient.getDefaultOptions().queries ?? {};
    expect(typeof retryDelay).toBe("function");
    const delay = retryDelay as (failureCount: number, error: unknown) => number;
    expect(delay(0, new Error("x"))).toBe(nextPollDelayMs(0));
    expect(delay(1, new Error("x"))).toBe(nextPollDelayMs(1));
    expect(delay(10, new Error("x"))).toBe(nextPollDelayMs(10));
  });

  it("does not refetch on window focus", () => {
    const queryClient = createQueryClient();
    expect(queryClient.getDefaultOptions().queries?.refetchOnWindowFocus).toBe(false);
  });
});
