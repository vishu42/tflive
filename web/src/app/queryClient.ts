import { QueryClient } from "@tanstack/react-query";
import { nextPollDelayMs } from "../api/polling";

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: 3,
        retryDelay: nextPollDelayMs,
        refetchOnWindowFocus: false
      }
    }
  });
}
