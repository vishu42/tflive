import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { KeyRound, Loader2, LogIn } from "lucide-react";
import { ApiRequestError, authMethods, signInWithPassword, ssoLoginURL } from "../api/client";
import { queryKeys } from "../api/queryKeys";
import { clearLoginAttempts, loginLoopDetected, recordLoginAttempt } from "./loginAttempts";

/** Where a sign-in with no return_to lands. The index route redirects onward. */
const defaultReturnTo = "/";

// Any base works: the question is whether the string stays relative to
// whatever it is resolved against, and a protocol-relative one escapes every
// base equally. Using a fixed one keeps the check independent of where the app
// happens to be served from.
const relativeBase = "https://return-to.invalid";

// return_to decides where the browser goes with a fresh session, so it is an
// open redirect until it is bounded. Only an in-app path is allowed, and
// nothing under /v1, which is the API — /v1/auth/login in particular would
// restart sign-in the moment it succeeded.
//
// The leading slash is not enough on its own to keep a path on this origin.
// `//host` is protocol-relative, and so is `/\host`: the URL parser folds a
// backslash into a slash for http(s), so the browser reads it as a host too.
// Rather than enumerate the spellings, this resolves the string with the same
// parser location.assign() will use and asks where it landed — an origin that
// survives that round trip cannot be talked out of the app.
//
// The server applies the same rule to the copy it receives (authn.SafeReturnTo).
// Both check, because each is reachable without the other: this one guards the
// client-side navigation after a password sign-in, which never reaches the
// server at all.
export function safeReturnTo(raw: string | null): string {
  if (!raw || !raw.startsWith("/")) return defaultReturnTo;
  // The parser drops control characters before it decides what the string
  // means, so a value carrying them navigates somewhere other than the value
  // that was checked. Refuse them rather than validate one string and hand
  // location.assign() a different one.
  if (/[\u0000-\u001f\u007f]/.test(raw)) return defaultReturnTo;

  let parsed: URL;
  try {
    parsed = new URL(raw, relativeBase);
  } catch {
    return defaultReturnTo;
  }
  if (parsed.origin !== relativeBase) return defaultReturnTo;

  // A traversal segment makes the path something other than it looks like, and
  // the /v1 test below reads the path as written. `/stacks/../v1/auth/login` is
  // the case that joins the two.
  const rawPath = raw.split(/[?#]/, 1)[0];
  if (rawPath.split("/").some((segment) => segment === "." || segment === "..")) {
    return defaultReturnTo;
  }

  if (parsed.pathname === "/v1" || parsed.pathname.startsWith("/v1/")) return defaultReturnTo;
  return raw;
}

function errorMessageFor(error: unknown): string {
  if (error instanceof ApiRequestError && error.status === 401) {
    // The server answers the same way for an unknown username and a wrong
    // password, and so does this: naming which one was wrong would undo it.
    return "Incorrect username or password.";
  }
  return "Sign-in is unavailable right now. Try again in a moment.";
}

/**
 * The sign-in screen, and the only place the app asks for a password.
 *
 * It sits outside SessionProvider: everything under "/" needs a session to
 * render, and this is the screen you see because you do not have one.
 *
 * The password form is unconditional. Local accounts always exist — root is one
 * and cannot be locked out — so there is always something to sign in with, and
 * rendering the form while /v1/auth/methods is still in flight keeps the first
 * paint from being an empty card. SSO is the part that varies, so its button
 * waits for the answer rather than being guessed at.
 */
export default function SignInScreen() {
  const [searchParams] = useSearchParams();
  const returnTo = safeReturnTo(searchParams.get("return_to"));

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const { data: methods } = useQuery({
    queryKey: queryKeys.authMethods,
    queryFn: authMethods,
    // A deployment does not gain or lose a provider while someone is looking at
    // this screen, and a failed answer must not retry behind a form that is
    // already usable without it.
    staleTime: Infinity,
    retry: false
  });

  // Reached only after sign-ins that the server accepted and the browser then
  // failed to remember. Each attempt below is recorded on a 204, so a spent
  // count means the credentials were right every time and the cookie never
  // survived the trip.
  const cookiesBlocked = loginLoopDetected();

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await signInWithPassword(username, password);
      recordLoginAttempt();
      // A full navigation, not a router push. The session cookie arrived on the
      // response above and the app's caches were built without one, so a reload
      // is what starts the signed-in app from a clean slate — and it is also
      // the trip that proves the cookie stuck.
      globalThis.location.assign(returnTo);
    } catch (caught) {
      setError(errorMessageFor(caught));
      setBusy(false);
    }
  };

  const handleSSO = () => {
    recordLoginAttempt();
    globalThis.location.assign(ssoLoginURL(returnTo));
  };

  const handleRetry = () => {
    clearLoginAttempts();
    globalThis.location.reload();
  };

  if (cookiesBlocked) {
    return (
      <main className="signin-page">
        <section className="panel signin-card" data-testid="signin-cookies-blocked">
          <h1 className="signin-title">We could not keep you signed in</h1>
          <p className="muted signin-lede">
            Your username and password were accepted, but this browser did not hold on to the
            session, so every page load started it over. That happens when cookies are blocked for
            this site — by browser settings, an extension, or a privacy mode that clears them
            between page loads.
          </p>
          <p className="muted signin-lede">
            Allow cookies for this site and try again. If it keeps failing, contact your
            administrator.
          </p>
          <button type="button" className="primary-button signin-submit" onClick={handleRetry} data-testid="signin-cookies-retry">
            Try again
          </button>
        </section>
      </main>
    );
  }

  return (
    <main className="signin-page">
      <section className="panel signin-card">
        <header className="signin-header">
          <p className="signin-wordmark">tflive</p>
          <h1 className="signin-title">Sign in</h1>
        </header>

        {error && (
          <div className="alert" role="alert" data-testid="signin-error">
            {error}
          </div>
        )}

        <form className="signin-form" onSubmit={handleSubmit}>
          <label htmlFor="signin-username">
            Username
            <input
              id="signin-username"
              name="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="username"
              autoFocus
              required
            />
          </label>
          <label htmlFor="signin-password">
            Password
            <input
              id="signin-password"
              name="password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
              required
            />
          </label>
          <button type="submit" className="primary-button signin-submit" disabled={busy}>
            {busy ? <Loader2 size={16} className="spin" /> : <LogIn size={16} />}
            Sign in
          </button>
        </form>

        {methods?.oidc && (
          <>
            <p className="signin-divider">or</p>
            <button
              type="button"
              className="secondary-button signin-submit"
              onClick={handleSSO}
              data-testid="signin-sso"
            >
              <KeyRound size={16} />
              Continue with single sign-on
            </button>
          </>
        )}
      </section>
    </main>
  );
}
