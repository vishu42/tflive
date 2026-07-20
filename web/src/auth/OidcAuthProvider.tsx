import { useCallback, useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { AuthContext } from "./AuthContext";
import type { AuthStatus } from "./AuthContext";
import type { Me } from "./types";
import { getUserManager } from "./userManager";
import { convertUserToMe } from "./convertUser";

export default function OidcAuthProvider() {
  const [me, setMe] = useState<Me | null>(null);
  const [status, setStatus] = useState<AuthStatus>("loading");
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    const isCallbackPath = location.pathname === "/auth/callback";

    if (isCallbackPath) {
      const isSignoutCallback = location.search.includes("state=") && !location.search.includes("code=");

      if (isSignoutCallback) {
        getUserManager().signoutRedirectCallback()
          .then(() => {
            setMe(null);
            setStatus("unauthenticated");
            getUserManager().signinRedirect();
          })
          .catch(() => setStatus("error"));
      } else {
        getUserManager().signinRedirectCallback()
          .then((user) => {
            setMe(convertUserToMe(user));
            setStatus("authenticated");
            // Navigate to the original requested route (or / as fallback).
            // oidc-client-ts stores the original URL in session; we use / as default.
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
          setMe(convertUserToMe(user));
          setStatus("authenticated");
        } else if (user?.expired && user.refresh_token) {
          getUserManager().signinSilent()
            .then((refreshedUser) => {
              if (refreshedUser) {
                setMe(convertUserToMe(refreshedUser));
                setStatus("authenticated");
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

  if (status === "loading") {
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

  return (
    <AuthContext.Provider value={{ me, status, login, logout }}>
      <Outlet />
    </AuthContext.Provider>
  );
}
