# Stack Creation Authorization Design

**Issue:** AUTH-011 / GitHub issue #13

Stack creation is authorized by the normalized Keycloak `platform-admin` and
`stack-creator` realm roles because a stack does not exist until after the
creation decision. Once persisted, the application writes a confirmed OpenFGA
`owner` relationship from the authenticated subject to the new stack.

The application returns `403` before persistence for callers without either
role. It returns OpenFGA's stable `503` dependency outcome when owner delivery
or confirmation fails after persistence. AUTH-012 owns durable retry and
reconciliation of that cross-datastore partial state. AUTH-013 owns all later
stack-scoped endpoint checks.

OpenFGA remains the authority for stack-scoped permissions. The backlog tracks
a future migration of global permissions into an OpenFGA tenant or platform
object; that would require model and provisioning changes and is outside this
issue.
