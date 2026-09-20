# 012: Plugin signature verification (cosign, M1.5)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Marketplace M1 (specs/008) pins plugins by digest but does not verify authenticity. Add cosign signature verification so `pctl market install` only accepts images signed by a trusted publisher. cosign is an external tool; pctl stays a single Go binary and shells out to it.

## Design

### Index: signature reference

Index entries gain a `signature` block:

```yaml
versions:
  - version: 1.2.0
    image: ghcr.io/vendor/translate
    digest: sha256:ab12...
    signature:
      mode: keyless           # keyless | key
      identity: "https://github.com/vendor/.github/workflows/release.yml@refs/tags/v1.2.0"
      issuer: "https://token.actions.githubusercontent.com"
      # mode: key → publicKey: <PEM or path>
```

### pctl: verify on install

`pctl market install`:
1. Resolve version → `image@digest` (unchanged)
2. If entry has `signature`: run `cosign verify` against `image@digest`
   - keyless: `cosign verify --certificate-identity <id> --certificate-oidc-issuer <issuer>`
   - key: `cosign verify --key <pem>`
3. cosign missing on PATH → error with install hint (or `PF_COSIGN_MODE=warn` downgrades to a warning)
4. Verify fails → refuse install
5. No `signature` in entry → `PF_COSIGN_REQUIRED=true` refuses; default warns

`PF_COSIGN` env overrides the cosign binary path.

### Invariants

- Verification runs against the **digest**, not a tag (digest already required in M1).
- pctl adds no Go crypto deps; cosign is optional at runtime (warn mode) but recommended.
- Signing/publishing (`pctl publish` + CI signing) stays M2+ — this spec is consume-side verify only.

## Blast radius

`pctl` (market install verify step + config env), market-index (optional `signature` in entries), docs. No platform runtime change.

## Acceptance

```bash
# entry with valid signature + cosign present → install proceeds
# tampered/unsigned image with signature required → cosign verify fails → refuse
# cosign not installed, PF_COSIGN_MODE=warn → warn + proceed; default → error
# entry without signature, PF_COSIGN_REQUIRED=true → refuse; default → warn
```
