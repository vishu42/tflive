import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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

export function useRegisterTemplateMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof client.registerTemplate>[1]) => client.registerTemplate(tenantID, body),
    onSuccess: (registration) => {
      queryClient.setQueryData(queryKeys.templateRegistration(tenantID, registration.id), registration);
    }
  });
}

export function useCreateStackMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof client.createStack>[1]) => client.createStack(tenantID, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks(tenantID) });
    }
  });
}

export function useAddTemplateToStackMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof client.addTemplateToStack>[2]) => client.addTemplateToStack(tenantID, stackID, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackID) });
    }
  });
}

export function useUpdateStackTemplateConfigMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { stackTemplateID: string; body: Parameters<typeof client.updateStackTemplateConfig>[2] }) =>
      client.updateStackTemplateConfig(tenantID, variables.stackTemplateID, variables.body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackID) });
    }
  });
}

export function useUpgradeStackTemplateMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { stackTemplateID: string; body: Parameters<typeof client.upgradeStackTemplate>[2] }) =>
      client.upgradeStackTemplate(tenantID, variables.stackTemplateID, variables.body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackID) });
    }
  });
}

export function useStartTemplateRunMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { stackTemplateID: string; body: Parameters<typeof client.startTemplateRun>[2] }) =>
      client.startTemplateRun(tenantID, variables.stackTemplateID, variables.body),
    onSuccess: (run) => {
      queryClient.setQueryData(queryKeys.templateRun(tenantID, run.id), run);
    }
  });
}

export function useApproveRunMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (runID: string) => client.approveRun(tenantID, runID),
    onSuccess: (_data, runID) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.templateRun(tenantID, runID) });
    }
  });
}

export function useCancelRunMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { runID: string; body: Parameters<typeof client.cancelRun>[2] }) =>
      client.cancelRun(tenantID, variables.runID, variables.body),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.templateRun(tenantID, variables.runID) });
    }
  });
}
