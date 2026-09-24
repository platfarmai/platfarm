# 025: Gateway-level `calls` enforcement (Appendix H degradation ① closed)

## Motivation

`permissions.calls` was enforced only in the called service (accept list). A plugin's service token could *reach* any service route through the gateway and probe L3. The manifest promised gateway enforcement.

## Design

`pctl sync` attaches a route-level `pre-function` to every `auth.required` protected route: decode the JWT payload (signature still verified by the jwt plugin), and **if `tokenType == "service"`, require a scope equal to the target service id or prefixed `"<id>:"`** — i.e. the caller must have declared that target in `permissions.calls` (auth issues service-token scopes from `calls`). User access tokens and app tokens are unaffected.

Kong subtlety: same-name plugins run most-specific-instance-only, so the route-level Lua embeds the global SSO cookie→Bearer mapping (specs/006) as a superset — otherwise admin consoles would lose cookie login on those routes.

First-party service-to-service traffic on core-net (direct DNS) bypasses the gateway and is unaffected; the gate binds exactly the plugin path (plugins can only see the gateway).

## Blast radius

pctl sync.go (kong generation). Regression risk = every protected route — full `pctl check --e2e` (8 services) green after change.

## Acceptance

Verified: scope-less service token → gateway `/api/grants/...` ⇒ `403 "service token lacks a svc-grants scope"`; same token direct on core-net ⇒ served (L3 accept list still applies); user tokens across all services unaffected (contract trios green).
