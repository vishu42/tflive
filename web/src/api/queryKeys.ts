export const queryKeys = {
  me: ["me"] as const,
  stacks: (tenantID: string) => ["stacks", tenantID] as const,
  templateRevisions: (tenantID: string) => ["templateRevisions", tenantID] as const,
  stack: (tenantID: string, stackID: string) => ["stack", tenantID, stackID] as const,
  templateRegistration: (tenantID: string, registrationID: string) =>
    ["templateRegistration", tenantID, registrationID] as const,
  templateRevisionVariables: (tenantID: string, templateRevisionID: string) =>
    ["templateRevisionVariables", tenantID, templateRevisionID] as const,
  templateRun: (tenantID: string, runID: string) => ["templateRun", tenantID, runID] as const,
  templateRunLogs: (tenantID: string, runID: string, statusTag: string) =>
    ["templateRunLogs", tenantID, runID, statusTag] as const,
  templateRunLog: (tenantID: string, runID: string, phase: string, statusTag: string) =>
    ["templateRunLog", tenantID, runID, phase, statusTag] as const,
  stackGrants: (tenantID: string, stackID: string) => ["stackGrants", tenantID, stackID] as const,
  userSearch: (tenantID: string, query: string) => ["userSearch", tenantID, query] as const
};
