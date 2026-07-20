import { Outlet } from "react-router-dom";
import { AuthContext } from "../AuthContext";

export default function OidcAuthProvider() {
  return (
    <AuthContext.Provider
      value={{
        me: {
          sub: "test",
          displayName: "Test",
          email: "test@example.com",
          tenantID: "tenant_123",
          globalCapabilities: { isPlatformAdmin: false, canCreateStack: true },
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
