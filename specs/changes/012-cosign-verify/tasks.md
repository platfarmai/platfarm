# 012 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl market: index `signature{mode,identity,issuer,publicKey}` field
- [x] pctl: cosign detect (PATH / PF_COSIGN) + `cosign verify` on install (keyless + key)
- [x] pctl: config PF_COSIGN_MODE (enforce|warn) + PF_COSIGN_REQUIRED
- [x] verify against image@digest; fail → refuse install
- [x] docs ADR #27; market-index example signature entry
- [x] QA: signed ok / tampered refuse / cosign missing warn|error / unsigned required-refuse
