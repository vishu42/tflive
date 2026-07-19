import { useMemo, useState } from "react";
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

  const value: AuthContextValue = {
    me: session.me,
    status: session.status,
    login: () => setSession({ me: mockUser, status: "authenticated" }),
    logout: () => setSession({ me: null, status: "unauthenticated" })
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
