import { useCallback, useEffect, useRef, useState } from "react";
import { Outlet } from "react-router-dom";
import { useIsMutating } from "@tanstack/react-query";
import { ApiRequestError, loginURL, logout as postLogout } from "../api/client";
import { AuthContext } from "./AuthContext";
import { clearLoginAttempts, loginLoopDetected, recordLoginAttempt } from "./loginAttempts";
import { useMeQuery } from "./useMeQuery";

// How long before expiry to re-authenticate. Long enough that the round trip
// completes with room to spare, short enough that it is rare.
const REAUTH_LEAD_MS = 60_000;
// How often to retry when re-authentication is deferred because work is in
// flight. Deferral delays re-auth; it never skips it.
const REAUTH_RETRY_MS = 5_000;

export default function SessionProvider() {
  const [status, setStatus] = useState<"loading" | "error" | "loop">("loading");
  const { data: me, error: meError, isLoading } = useMeQuery();
  const pendingMutations = useIsMutating();
  const navigated = useRef(false);
  // The live mutation count, read from a ref so the expiry timer below does not
  // restart every time a mutation starts or settles.
  const pendingMutationsRef = useRef(pendingMutations);
  pendingMutationsRef.current = pendingMutations;

  const login = useCallback(() => {
    if (navigated.current) return;
    // Every redirect that comes back still unauthenticated is a lap of the
    // loop. Stop and explain rather than bouncing the browser forever.
    if (loginLoopDetected()) {
      setStatus("loop");
      return;
    }
    navigated.current = true;
    recordLoginAttempt();
    globalThis.location.assign(loginURL());
  }, []);

  // Reaching /v1/me with an identity proves the cookie survived the round
  // trip, so the attempts spent getting here no longer count against us.
  useEffect(() => {
    if (me) clearLoginAttempts();
  }, [me]);

  const retryLogin = useCallback(() => {
    clearLoginAttempts();
    setStatus("loading");
    navigated.current = true;
    recordLoginAttempt();
    globalThis.location.assign(loginURL());
  }, []);

  const logout = useCallback(() => {
    postLogout();
  }, []);

  useEffect(() => {
    if (!meError) return;
    if (meError instanceof ApiRequestError && meError.status === 401) {
      login();
      return;
    }
    setStatus("error");
  }, [meError, login]);

  // Re-authenticate proactively, at a moment of our choosing. This is a
  // convenience, not a control: the API rejects an expired token whatever the
  // browser believes, so clock skew or a suspended laptop degrades to the 401
  // path rather than to unauthorised access.
  useEffect(() => {
    if (!me?.sessionExpiresAt) return;

    const fireAt = new Date(me.sessionExpiresAt).getTime() - REAUTH_LEAD_MS;
    if (Number.isNaN(fireAt)) return;

    let timer: ReturnType<typeof setTimeout>;
    const attempt = () => {
      if (pendingMutationsRef.current > 0 || document.querySelector("[data-unsaved='true']")) {
        timer = setTimeout(attempt, REAUTH_RETRY_MS);
        return;
      }
      login();
    };
    timer = setTimeout(attempt, Math.max(0, fireAt - Date.now()));
    return () => clearTimeout(timer);
  }, [me?.sessionExpiresAt, login]);

  if (status === "loop") {
    return (
      <div data-testid="auth-loop-error">
        <h1>We could not keep you signed in</h1>
        <p>
          Sign-in worked, but this browser did not hold on to the session, so every page load
          started it over. That happens when cookies are blocked for this site, or when the sign-in
          token is too large to store — usually because the account carries a lot of group or role
          claims.
        </p>
        <p>
          Allow cookies for this site and try again. If it keeps failing, contact your
          administrator.
        </p>
        <button type="button" onClick={retryLogin} data-testid="auth-loop-retry-button">
          Try again
        </button>
      </div>
    );
  }

  if (isLoading) return null;

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

  if (meError || !me) return null;

  return (
    <AuthContext.Provider value={{ me, status: "authenticated", login, logout }}>
      <Outlet />
    </AuthContext.Provider>
  );
}
