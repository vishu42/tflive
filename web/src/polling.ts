import type { TemplateRegistrationStatus, TemplateRunStatus } from "./api/types";

export function isTerminalRegistrationStatus(status: TemplateRegistrationStatus): boolean {
  return status === "completed" || status === "invalid" || status === "failed";
}

export function isTerminalRunStatus(status: TemplateRunStatus): boolean {
  return status === "completed" || status === "failed" || status === "canceled";
}

export function nextPollDelayMs(failureCount: number): number {
  return Math.min(1500 + failureCount * 500, 5000);
}
