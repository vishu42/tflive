import { describe, expect, it } from "vitest";
import { isTerminalRegistrationStatus, isTerminalRunStatus, nextPollDelayMs } from "./polling";

describe("polling helpers", () => {
  it("identifies terminal registration states", () => {
    expect(isTerminalRegistrationStatus("completed")).toBe(true);
    expect(isTerminalRegistrationStatus("invalid")).toBe(true);
    expect(isTerminalRegistrationStatus("failed")).toBe(true);
    expect(isTerminalRegistrationStatus("running")).toBe(false);
  });

  it("identifies terminal run states", () => {
    expect(isTerminalRunStatus("completed")).toBe(true);
    expect(isTerminalRunStatus("failed")).toBe(true);
    expect(isTerminalRunStatus("canceled")).toBe(true);
    expect(isTerminalRunStatus("waiting_approval")).toBe(false);
  });

  it("backs off after repeated failures without exceeding five seconds", () => {
    expect(nextPollDelayMs(0)).toBe(1500);
    expect(nextPollDelayMs(1)).toBe(2000);
    expect(nextPollDelayMs(10)).toBe(5000);
  });
});
