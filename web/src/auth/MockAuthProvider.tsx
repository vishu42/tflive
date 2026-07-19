import { useCallback, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { AuthContext } from "./AuthContext";
import type { AuthContextValue, AuthStatus } from "./AuthContext";
import { resolveMockUser } from "./mockUsers";
import type { Me } from "./types";

export default function MockAuthProvider({ children }: { children: ReactNode }) {
  const mockUser = useMemo(() => resolveMockUser(import.meta.env.VITE_TFLIVE_MOCK_USER_ROLE), []);
  const [session, setSession] = useState<{ me: Me | null; status: AuthStatus }>({
    me: mockUser,
    status: "authenticated"
  });

  const login = useCallback(() => setSession({ me: mockUser, status: "authenticated" }), [mockUser]);
  const logout = useCallback(() => setSession({ me: null, status: "unauthenticated" }), []);

  const value: AuthContextValue = {
    me: session.me,
    status: session.status,
    login,
    logout
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
