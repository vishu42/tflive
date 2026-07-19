import { describe, expect, it } from "vitest";
import { defaultMockRole, mockUsers, resolveMockUser } from "./mockUsers";

describe("resolveMockUser", () => {
  it("returns the default role's user when no role is configured", () => {
    expect(resolveMockUser(undefined)).toBe(mockUsers[defaultMockRole]);
    expect(resolveMockUser("")).toBe(mockUsers[defaultMockRole]);
    expect(resolveMockUser("   ")).toBe(mockUsers[defaultMockRole]);
  });

  it("returns the requested role's user", () => {
    expect(resolveMockUser("admin")).toBe(mockUsers.admin);
    expect(resolveMockUser(" viewer ")).toBe(mockUsers.viewer);
  });

  it("throws for an unknown role", () => {
    expect(() => resolveMockUser("superuser")).toThrow(
      'Unknown VITE_TFLIVE_MOCK_USER_ROLE "superuser"; expected one of: admin, operator, viewer'
    );
  });
});
