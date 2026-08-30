// A sign-in that succeeds at the IdP but leaves the browser without a usable
// session cookie puts the app in a redirect loop: /v1/me answers 401, we
// navigate to login, the IdP sends us back, /v1/me answers 401 again. The two
// realistic causes are an ID token past the ~4096-byte cookie limit (a
// provider that stuffs group or role claims into it gets there easily) and a
// browser configured to block cookies. Neither reports an error to script.
//
// An in-memory guard cannot see this: every redirect reloads the page and
// resets it, so it stops one bounce and not a loop. sessionStorage survives
// the reload and is scoped to the one tab, so the count tracks a single run of
// attempts and a second tab starts clean.

const storageKey = "tflive.auth.loginAttempts";

// Redirects allowed before we stop and explain. Enough to absorb an expiry
// racing a page load, few enough that the flashing stops quickly.
export const maxLoginAttempts = 3;

// Used when sessionStorage is unavailable — Safari private browsing throws on
// access, and a browser blocking cookies may well block storage too. It resets
// on reload, so the loop guard degrades to the old in-memory behaviour rather
// than breaking sign-in outright.
let inMemoryAttempts = 0;

// One page load can decide to redirect from more than one place: the failing
// /v1/me request and the provider reacting to that same failure. A lap of the
// loop is one page load that ended at the login route, so only the first of
// those counts.
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

// True once the redirects have been spent, meaning another one would only
// restart the loop.
export function loginLoopDetected(): boolean {
  return readLoginAttempts() >= maxLoginAttempts;
}
