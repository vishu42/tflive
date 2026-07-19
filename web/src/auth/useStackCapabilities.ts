import { useStackQuery } from "../api/queries";
import { tenantID } from "../config";
import type { StackCapabilities } from "./types";

export function useStackCapabilities(stackId: string): StackCapabilities | undefined {
  return useStackQuery(tenantID, stackId).data?.stack.effectiveCapabilities;
}
