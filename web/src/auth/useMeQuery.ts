import { useQuery } from "@tanstack/react-query";
import type { UseQueryOptions } from "@tanstack/react-query";
import { getMe } from "../api/client";
import { queryKeys } from "../api/queryKeys";
import type { Me } from "./types";

export function useMeQuery(options?: Partial<UseQueryOptions<Me>>) {
  return useQuery<Me>({
    queryKey: queryKeys.me,
    queryFn: getMe,
    staleTime: Infinity,
    retry: false,
    ...options,
  });
}
