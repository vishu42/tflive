import type { User } from "oidc-client-ts";
import type { Me } from "./types";

export function convertUserToMe(user: User): Me {
  const profile = user.profile as Record<string, unknown>;
  const realmAccess = profile.realm_access as { roles?: string[] } | undefined;
  const roles = realmAccess?.roles ?? [];

  return {
    sub: profile.sub as string,
    displayName: (profile.preferred_username as string) ?? (profile.sub as string),
    email: profile.email as string | undefined,
    globalCapabilities: {
      isPlatformAdmin: roles.includes("platform-admin"),
      canCreateStack: roles.includes("stack-creator") || roles.includes("platform-admin"),
    },
  };
}
