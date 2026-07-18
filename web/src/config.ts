const localTenantID = "tenant_123";
const tenantIDPattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;

export function resolveTenantID(rawTenantID: string | undefined, development: boolean): string {
  const value = rawTenantID?.trim() ?? "";
  if (value === "") {
    if (development) {
      return localTenantID;
    }
    throw new Error("VITE_TFLIVE_TENANT_ID is required");
  }
  if (!tenantIDPattern.test(value)) {
    throw new Error(
      "VITE_TFLIVE_TENANT_ID must start with an ASCII alphanumeric character, contain only ASCII alphanumerics, underscore, or hyphen, and be at most 128 characters"
    );
  }
  return value;
}

export const tenantID = resolveTenantID(
  import.meta.env.VITE_TFLIVE_TENANT_ID,
  import.meta.env.DEV
);
