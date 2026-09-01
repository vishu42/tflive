import { Outlet } from "react-router-dom";
import { AuthContext } from "../AuthContext";

export default function SessionProvider() {
  return (
    <AuthContext.Provider
      value={{
        me: {
          sub: "test",
          displayName: "Test",
          email: "test@example.com",
          tenantID: "tenant_123",
          globalCapabilities: { isPlatformAdmin: false, canCreateStack: true },
          sessionExpiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
        },
        status: "authenticated" as const,
        login: () => {},
        logout: () => {},
      }}
    >
      <Outlet />
    </AuthContext.Provider>
  );
}
