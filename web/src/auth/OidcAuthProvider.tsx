import { useCallback, useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { AuthContext } from "./AuthContext";
import { getUserManager } from "./userManager";
import { useMeQuery } from "./useMeQuery";

export default function OidcAuthProvider() {
  const [oidcResolved, setOidcResolved] = useState(false);
  const [status, setStatus] = useState<"loading" | "unauthenticated" | "error">("loading");
  const location = useLocation();
  const navigate = useNavigate();

  const { data: me, error: meError, isLoading: meLoading } = useMeQuery({ enabled: oidcResolved });

  useEffect(() => {
    const isCallbackPath = location.pathname === "/auth/callback";

    if (isCallbackPath) {
      const isSignoutCallback = location.search.includes("state=") && !location.search.includes("code=");

      if (isSignoutCallback) {
        getUserManager().signoutRedirectCallback()
          .then(() => {
            setStatus("unauthenticated");
            getUserManager().signinRedirect();
          })
          .catch(() => setStatus("error"));
      } else {
        getUserManager().signinRedirectCallback()
          .then((user) => {
            setOidcResolved(true);
            const target = (user.state as string) ?? "/stacks";
            navigate(target, { replace: true });
          })
          .catch(() => setStatus("error"));
      }
      return;
    }

    getUserManager().getUser()
      .then((user) => {
        if (user && !user.expired) {
          setOidcResolved(true);
        } else if (user?.expired && user.refresh_token) {
          getUserManager().signinSilent()
            .then((refreshedUser) => {
              if (refreshedUser) {
                setOidcResolved(true);
              } else {
                setStatus("unauthenticated");
                getUserManager().signinRedirect();
              }
            })
            .catch(() => {
              setStatus("unauthenticated");
              getUserManager().signinRedirect();
            });
        } else {
          setStatus("unauthenticated");
          getUserManager().signinRedirect();
        }
      })
      .catch(() => setStatus("error"));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const login = useCallback(() => {
    getUserManager().signinRedirect();
  }, []);

  const logout = useCallback(() => {
    getUserManager().signoutRedirect();
  }, []);

  if (meLoading) {
    return null;
  }

  if (status === "error") {
    return (
      <div data-testid="auth-error">
        <p>Authentication failed. The identity service may be unavailable.</p>
        <button type="button" onClick={login} data-testid="auth-retry-button">
          Retry
        </button>
      </div>
    );
  }

  if (meError) {
    login();
    return null;
  }

  if (!me) {
    return null;
  }

  return (
    <AuthContext.Provider value={{ me, status: "authenticated", login, logout }}>
      <Outlet />
    </AuthContext.Provider>
  );
}
