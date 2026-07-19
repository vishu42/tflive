import { useQuery } from "@tanstack/react-query";
import * as client from "./client";
import { queryKeys } from "./queryKeys";
import { isTerminalRegistrationStatus, isTerminalRunStatus } from "../polling";
import type { TemplateRegistration, TemplateRegistrationStatus, TemplateRun, TemplateRunStatus } from "./types";

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

export const POLL_INTERVAL_MS = 1500;

export function registrationRefetchInterval(status: TemplateRegistrationStatus | undefined): number | false {
  return status && isTerminalRegistrationStatus(status) ? false : POLL_INTERVAL_MS;
}

export function runRefetchInterval(status: TemplateRunStatus | undefined, poll: boolean): number | false {
  if (!poll) {
    return false;
  }
  return status && isTerminalRunStatus(status) ? false : POLL_INTERVAL_MS;
}

export function useTemplateRegistrationQuery(tenantID: string, registrationID: string) {
  return useQuery({
    queryKey: queryKeys.templateRegistration(tenantID, registrationID),
    queryFn: () => client.getTemplateRegistration(tenantID, registrationID),
    enabled: registrationID !== "",
    refetchInterval: (query: { state: { data?: TemplateRegistration } }) =>
      registrationRefetchInterval(query.state.data?.status)
  });
}

export function useTemplateRunQuery(tenantID: string, runID: string, options: { poll: boolean }) {
  return useQuery({
    queryKey: queryKeys.templateRun(tenantID, runID),
    queryFn: () => client.getTemplateRun(tenantID, runID),
    enabled: runID !== "",
    refetchInterval: (query: { state: { data?: TemplateRun } }) =>
      runRefetchInterval(query.state.data?.status, options.poll)
  });
}

export function useTemplateRunLogsQuery(tenantID: string, runID: string, statusTag: string) {
  return useQuery({
    queryKey: queryKeys.templateRunLogs(tenantID, runID, statusTag),
    queryFn: () => client.listTemplateRunLogs(tenantID, runID),
    enabled: runID !== ""
  });
}

export function useTemplateRunLogQuery(tenantID: string, runID: string, phase: string, statusTag: string) {
  return useQuery({
    queryKey: queryKeys.templateRunLog(tenantID, runID, phase, statusTag),
    queryFn: () => client.getTemplateRunLog(tenantID, runID, phase),
    enabled: runID !== "" && phase !== ""
  });
}
