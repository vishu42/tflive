import type { Me } from "./types";

export const mockUsers: Record<string, Me> = {
  admin: {
    sub: "mock-admin",
    displayName: "Ada Admin",
    email: "ada@example.test",
    globalCapabilities: { isPlatformAdmin: true, canCreateStack: true }
  },
  operator: {
    sub: "mock-operator",
    displayName: "Otto Operator",
    email: "otto@example.test",
    globalCapabilities: { isPlatformAdmin: false, canCreateStack: true }
  },
  viewer: {
    sub: "mock-viewer",
    displayName: "Vera Viewer",
    email: "vera@example.test",
    globalCapabilities: { isPlatformAdmin: false, canCreateStack: false }
  }
};

export const defaultMockRole = "operator";

export function resolveMockUser(rawRole: string | undefined): Me {
  const role = rawRole?.trim() || defaultMockRole;
  const user = mockUsers[role];
  if (!user) {
    throw new Error(
      `Unknown VITE_TFLIVE_MOCK_USER_ROLE "${role}"; expected one of: ${Object.keys(mockUsers).join(", ")}`
    );
  }
  return user;
}
