# 024: Plugin egress domain allowlist (Appendix H degradation ③ closed)

## Motivation

`permissions.egress` was declared but never executed: plugin networks are `internal: true`, so plugins had all-or-nothing (nothing). A translate-via-DeepL style plugin simply could not work.

## Design

Per plugin with non-empty `permissions.egress`, `pctl sync` generates:

- `gateway/egress-<id>.conf` — squid allowlist (`dstdomain` entries; overlapping `x.com`/`.x.com` deduped to the dotted form — squid rejects overlaps).
- Compose sidecar `egress-<id>` (`ubuntu/squid`) dual-homed on `net-plugin-<x>` + new non-internal `egress-net`.
- Plugin container gets `HTTP(S)_PROXY=http://egress-<id>:3128` + `NO_PROXY=gateway,plugin-pg,localhost` env.

The plugin network stays `internal` — the *only* way out is the proxy, and the proxy only relays allowlisted domains (80/443, CONNECT to 443 only).

## Blast radius

pctl (sync.go + compose.go) and generated files. Plugins without `egress` unchanged.

## Acceptance

Verified with a drill plugin (`egress: [.example.com]`): via proxy `http://example.com` → 200 body; `httpbin.org` → squid 403; without proxy → DNS dead (internal). Honest boundary note in H.4 replaced by this mechanism.
