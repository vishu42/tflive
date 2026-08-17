#!/usr/bin/env node
/**
 * Drive the tflive web app through a real Keycloak login in headless Chrome,
 * so auth-gated screens can be inspected without a human taking screenshots.
 *
 * Why this exists: every screen except /styleguide sits behind OidcAuthProvider,
 * which always performs a real OIDC redirect — there is no dev bypass, and the
 * VITE_TFLIVE_MOCK_USER_ROLE flag mentioned in .env.example is stale. Deep links
 * such as /stacks/<id>/access re-enter the OIDC flow and land back on /stacks,
 * so navigation is done by clicking through the SPA rather than by URL.
 *
 * Setup:
 *   cd web && npm run dev                       # must be port 5173: the
 *                                               # Keycloak client only registers
 *                                               # 5173 as a redirect_uri
 *   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
 *     --headless=new --disable-gpu --remote-debugging-port=9222 \
 *     --user-data-dir=/tmp/tflive-chrome --window-size=1512,950 about:blank &
 *
 * Credentials come from the environment; nothing is hardcoded:
 *   export TFLIVE_USER="$KEYCLOAK_PLATFORM_ADMIN_USERNAME"
 *   export TFLIVE_PASS="$KEYCLOAK_PLATFORM_ADMIN_PASSWORD"
 *
 * Usage:
 *   node scripts/drive-web.mjs --shot out.png --click "dev" --click "Access"
 *   node scripts/drive-web.mjs --click Templates --probe 'document.title'
 *   node scripts/drive-web.mjs --fields --click "dev" --click Environment
 *
 * Flags:
 *   --click <label>   click the <a>/<button> whose text matches (repeatable,
 *                     applied in order, 3.5s settle between each)
 *   --shot <file>     write a full-page PNG
 *   --probe <expr>    evaluate a JS expression in the page and print the result
 *   --fields          print every input/select/textarea width vs its parent,
 *                     which is how field-stretch regressions get caught
 *   --port <n>        devtools port (default 9222)
 */
import { writeFileSync } from "node:fs";

const argv = process.argv.slice(2);
const clicks = [];
let shot = null, probe = null, fields = false, port = 9222;
for (let i = 0; i < argv.length; i++) {
  const next = () => argv[++i];
  if (argv[i] === "--click") clicks.push(next());
  else if (argv[i] === "--shot") shot = next();
  else if (argv[i] === "--probe") probe = next();
  else if (argv[i] === "--fields") fields = true;
  else if (argv[i] === "--port") port = Number(next());
}

const USER = process.env.TFLIVE_USER;
const PASS = process.env.TFLIVE_PASS;
if (!USER || !PASS) {
  console.error("TFLIVE_USER and TFLIVE_PASS must be set (see the header of this file).");
  process.exit(2);
}

const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json().catch(() => null);
const page = list?.find((t) => t.type === "page");
if (!page) {
  console.error(`No Chrome page target on :${port}. Start Chrome with --remote-debugging-port=${port}.`);
  process.exit(2);
}

const ws = new WebSocket(page.webSocketDebuggerUrl);
let id = 0;
const pending = new Map();
const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const msgId = ++id;
    pending.set(msgId, { resolve, reject });
    ws.send(JSON.stringify({ id: msgId, method, params }));
  });
ws.addEventListener("message", (event) => {
  const msg = JSON.parse(event.data);
  const slot = msg.id && pending.get(msg.id);
  if (!slot) return;
  pending.delete(msg.id);
  msg.error ? slot.reject(new Error(JSON.stringify(msg.error))) : slot.resolve(msg.result);
});
const evaluate = async (expression) =>
  (await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true })).result.value;
const settle = (ms) => new Promise((r) => setTimeout(r, ms));

await new Promise((r) => ws.addEventListener("open", r));
await send("Page.enable");
await send("Runtime.enable");
await send("Page.navigate", { url: "http://127.0.0.1:5173/stacks" });
await settle(6000);

// Keycloak's login page is server-rendered, so setting .value and submitting the
// form is enough — no React synthetic-event plumbing needed.
if (await evaluate("!!document.querySelector('#username')")) {
  await evaluate(`(() => {
    const u = document.querySelector('#username'), p = document.querySelector('#password');
    const set = (el, v) => {
      Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value').set.call(el, v);
      el.dispatchEvent(new Event('input', { bubbles: true }));
    };
    set(u, ${JSON.stringify(USER)});
    set(p, ${JSON.stringify(PASS)});
    (document.querySelector('#kc-form-login') || u.form).submit();
  })()`);
  await settle(8000);
}
console.error("signed in at:", await evaluate("location.pathname"));

for (const label of clicks) {
  const result = await evaluate(`(() => {
    const all = Array.from(document.querySelectorAll('a, button'));
    const exact = all.find((x) => x.textContent.trim() === ${JSON.stringify(label)});
    const prefix = all.find((x) => x.textContent.trim().startsWith(${JSON.stringify(label)}));
    const el = exact || prefix;
    if (!el) return 'NOT FOUND: ' + ${JSON.stringify(label)};
    el.click();
    return 'clicked: ' + ${JSON.stringify(label)};
  })()`);
  console.error(result);
  if (result.startsWith("NOT FOUND")) { ws.close(); process.exit(1); }
  await settle(3500);
}
console.error("at:", await evaluate("location.pathname"));

if (fields) {
  console.log(await evaluate(`JSON.stringify(
    Array.from(document.querySelectorAll('input, select, textarea')).map((el) => ({
      tag: el.tagName.toLowerCase(),
      width: Math.round(el.getBoundingClientRect().width),
      parentWidth: Math.round(el.parentElement.getBoundingClientRect().width),
      label: (el.getAttribute('aria-label') || el.placeholder || el.id || '').slice(0, 30)
    })), null, 1)`));
}
if (probe) console.log(await evaluate(probe));
if (shot) {
  const png = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
  writeFileSync(shot, Buffer.from(png.data, "base64"));
  console.error("screenshot ->", shot);
}

ws.close();
process.exit(0);
