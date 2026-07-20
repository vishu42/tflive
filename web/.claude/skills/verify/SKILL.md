---
name: verify
description: Build, launch, and drive the tflive web frontend to observe a change at runtime
---

# Verifying web/ changes at runtime

The frontend is a Vite SPA (`npm run dev` in `web/`, port 5173) that proxies
`/v1` and `/healthz` to a backend on `http://localhost:8081` (see
`vite.config.ts`). In dev mode the tenant defaults to `tenant_123`
(`src/config.ts`), and auth comes from `MockAuthProvider` (default role
"operator"; override with `VITE_TFLIVE_MOCK_USER_ROLE`).

The Go backend does not yet return `effectiveCapabilities` on stack responses
(AUTH-013/017 not landed), so capability-gated routes hang in a blank
"loading" state against the real API. To drive gated screens, run a stub
backend on 8081 that returns the AUTH-017 contract shape — a stack object
with `effectiveCapabilities: { canView, canOperate, canApprove,
canManageAccess }`. Endpoints the stack screens hit:

- `GET /v1/tenants/tenant_123/stacks` → `[stack]`
- `GET /v1/tenants/tenant_123/stacks/:id` → `{ stack, templates: [] }`
- `GET /v1/tenants/tenant_123/template-revisions` → `[]`

Recipe that works:

```bash
node stub-api.mjs &            # tiny http server on 127.0.0.1:8081, JSON above
(cd web && npm run dev) &      # vite on 127.0.0.1:5173
# drive with headless Chrome (installed at the standard macOS path):
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --headless=new --disable-gpu --virtual-time-budget=6000 \
  --dump-dom http://127.0.0.1:5173/stacks/stack_1        # or --screenshot=out.png --window-size=1100,700
```

Gotchas:

- `--virtual-time-budget=6000` is required; without it Chrome dumps the DOM
  before React renders.
- Vite's SPA history fallback serves deep links (`/stacks/stack_1/runs`)
  directly — no extra config needed.
- To test denied-capability states, restart the stub with different booleans;
  the frontend never computes permissions client-side.
