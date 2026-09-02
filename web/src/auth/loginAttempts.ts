// A sign-in the server accepts but that leaves the browser without a usable
// session cookie puts the app in a loop: /v1/me answers 401, we send the
// visitor to the sign-in screen, they sign in, /v1/me answers 401 again. The
// realistic cause is a browser configured to block cookies for this site —
// the session cookie is a 43-character opaque reference, not a token, so it
// cannot run into a size limit. Blocking reports no error to script.
//
// Only a completed authentication counts: a 204 from the password form, or the
// trip to the IdP that an SSO button starts. Merely landing on the sign-in
// screen is not an attempt — with a password form the visitor lands there
// whenever they are not signed in, and counting that would end in blaming
// cookies for someone who has simply not typed a password yet.
//
// An in-memory guard cannot see this: every redirect reloads the page and
// resets it, so it stops one bounce and not a loop. sessionStorage survives
// the reload and is scoped to the one tab, so the count tracks a single run of
// attempts and a second tab starts clean.

const storageKey = "tflive.auth.loginAttempts";

// Sign-ins allowed before we stop and explain. Enough to absorb an expiry
// racing a page load, few enough that the user is not made to retype a
// password that was never going to stick.
export const maxLoginAttempts = 3;

// Used when sessionStorage is unavailable — Safari private browsing throws on
// access, and a browser blocking cookies may well block storage too. It resets
// on reload, so the loop guard degrades to the old in-memory behaviour rather
// than breaking sign-in outright.
let inMemoryAttempts = 0;

// One page load can complete at most one sign-in, and a double-submitted form
// must not spend two of the allowance. Only the first record per page load
// counts.
let recordedThisPageLoad = false;

function storage(): Storage | null {
  try {
    const candidate = globalThis.sessionStorage;
    // Touch it: presence is not access, and a blocked store throws on use.
    candidate.getItem(storageKey);
    return candidate;
  } catch {
    return null;
  }
}

export function readLoginAttempts(): number {
  const store = storage();
  if (!store) return inMemoryAttempts;
  const parsed = Number.parseInt(store.getItem(storageKey) ?? "", 10);
  return Number.isNaN(parsed) || parsed < 0 ? 0 : parsed;
}

export function recordLoginAttempt(): number {
  if (recordedThisPageLoad) return readLoginAttempts();
  recordedThisPageLoad = true;
  const next = readLoginAttempts() + 1;
  inMemoryAttempts = next;
  const store = storage();
  if (store) {
    try {
      store.setItem(storageKey, String(next));
    } catch {
      // Quota or a store that accepts reads and refuses writes. The in-memory
      // count still holds for the life of this page.
    }
  }
  return next;
}

export function clearLoginAttempts(): void {
  inMemoryAttempts = 0;
  recordedThisPageLoad = false;
  const store = storage();
  if (!store) return;
  try {
    store.removeItem(storageKey);
  } catch {
    // Nothing to do: the count is advisory and already cleared in memory.
  }
}

// True once the allowance is spent, meaning another sign-in would only restart
// the loop.
export function loginLoopDetected(): boolean {
  return readLoginAttempts() >= maxLoginAttempts;
}
