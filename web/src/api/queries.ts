import { useQuery } from "@tanstack/react-query";
import * as client from "./client";
import { queryKeys } from "./queryKeys";

export function useStacksQuery(tenantID: string) {
  return useQuery({
    queryKey: queryKeys.stacks(tenantID),
    queryFn: () => client.listStacks(tenantID)
  });
}

export function useTemplateRevisionsQuery(tenantID: string) {
  return useQuery({
    queryKey: queryKeys.templateRevisions(tenantID),
    queryFn: () => client.listTemplateRevisions(tenantID)
  });
}

export function useStackQuery(tenantID: string, stackID: string) {
  return useQuery({
    queryKey: queryKeys.stack(tenantID, stackID),
    queryFn: () => client.getStack(tenantID, stackID),
    enabled: stackID !== ""
  });
}

export function useTemplateRevisionVariablesQuery(tenantID: string, templateRevisionID: string) {
  return useQuery({
    queryKey: queryKeys.templateRevisionVariables(tenantID, templateRevisionID),
    queryFn: () => client.getTemplateRevisionVariables(tenantID, templateRevisionID),
    enabled: templateRevisionID !== ""
  });
}
