// Identity and capability contract the frontend commits to for AUTH-017 (see
// docs/superpowers/specs/2026-07-18-ui-revamp-design.md, "Identity & capability contract").
// The backend resolves capabilities server-side; the frontend only ever reads booleans —
// no raw tokens, no role-name strings, and no tenant ID (implicit in the deployment).

// GET /v1/me
export interface Me {
  sub: string;
  displayName: string;
  email?: string;
  globalCapabilities: {
    isPlatformAdmin: boolean;
    canCreateStack: boolean;
  };
}

// Attached to every stack resource (list + detail) as `effectiveCapabilities`.
export interface StackCapabilities {
  canView: boolean;
  canOperate: boolean;
  canApprove: boolean;
  canManageAccess: boolean;
}
