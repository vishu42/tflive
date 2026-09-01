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
export const REAUTH_RETRY_MS = 5_000;
// How long re-authentication may be deferred while work is in flight. Past
// this, the session is closer to expiring than the deferral is to helping:
// waiting longer only guarantees the 401 path takes the same navigation with
// no warning at all.
export const REAUTH_DEFER_LIMIT_MS = 120_000;

export default function SessionProvider() {
  const [status, setStatus] = useState<"loading" | "error" | "loop">("loading");
  const { data: me, error: meError, isLoading, refetch: refetchMe } = useMeQuery();
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

  // The error screen below is reached only by a failure that is *not* a 401 —
  // /v1/me answering 500 because a dependency it calls is down, say. The
  // session is fine, so a trip to the IdP cannot help: it would spend a login
  // attempt, come back to the same failure, and after three presses trip the
  // loop guard into telling the user their browser is blocking cookies, which
  // is a diagnosis of something that is not happening. Retry means retry.
  const retryMe = useCallback(() => {
    void refetchMe();
  }, [refetchMe]);

  useEffect(() => {
    if (!meError) {
      // A retry that worked clears the error screen. Only "error" is reset:
      // "loop" is a terminal diagnosis with its own button, and clobbering it
      // here would put the user back on the redirect merry-go-round.
      setStatus((current) => (current === "error" ? "loading" : current));
      return;
    }
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
    let deferringSince: number | null = null;
    // attempt awaits a refetch, and React only ever runs a given effect
    // instance's cleanup once. If that cleanup fires while attempt is
    // suspended on the await — unmount, or a dependency change — the
    // continuation would otherwise resume into a component that is already
    // gone, rearming a timer nothing will ever clear again or navigating
    // after teardown. cancelled is checked right after the await so that
    // resumption is a no-op once cleanup has run.
    let cancelled = false;
    const attempt = async () => {
      const busy = pendingMutationsRef.current > 0 || document.querySelector("[data-unsaved='true']");
      if (busy) {
        deferringSince ??= Date.now();
        if (Date.now() - deferringSince < REAUTH_DEFER_LIMIT_MS) {
          timer = setTimeout(attempt, REAUTH_RETRY_MS);
          return;
        }
      }
      // The snapshot that armed this timer can be stale: the server slides
      // the idle bound on every authenticated request, including this one, so
      // sessionExpiresAt from the last render may already be well behind the
      // server's own bound. Refetch and trust that instead of navigating an
      // active user (and the browser's one round trip to the IdP) away from a
      // session that has not actually ended.
      const { data: refreshed } = await refetchMe();
      if (cancelled) return;
      const nextFireAt = refreshed?.sessionExpiresAt
        ? new Date(refreshed.sessionExpiresAt).getTime() - REAUTH_LEAD_MS
        : NaN;
      if (!Number.isNaN(nextFireAt) && nextFireAt > Date.now()) {
        timer = setTimeout(attempt, nextFireAt - Date.now());
        return;
      }
      login();
    };
    timer = setTimeout(attempt, Math.max(0, fireAt - Date.now()));
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [me?.sessionExpiresAt, login, refetchMe]);

  if (status === "loop") {
    return (
      <div data-testid="auth-loop-error">
        <h1>We could not keep you signed in</h1>
        <p>
          Sign-in worked, but this browser did not hold on to the session, so every page load
          started it over. That happens when cookies are blocked for this site — by browser
          settings, an extension, or a privacy mode that clears them between page loads.
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
        <button type="button" onClick={retryMe} data-testid="auth-retry-button">
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
