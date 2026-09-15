# 008 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl market.go: index client (file:// + https raw base), types
- [x] `pctl market search <q>` — grep id/title/desc
- [x] `pctl market info <id>[@ver]` — versions, permissions, digest
- [x] `pctl market install <id>[@ver]` — resolve → write plugin.yaml (image@digest) → runInstall gate; refuse missing digest
- [x] main.go: wire `market` subcommand; usage
- [x] platfarmai/market-index repo: README + example plugins/<id>/index.yaml
- [x] docs: consuming/market usage; ADR #23
- [x] QA: search/info/install via file:// index; unknown id + missing digest errors; commit/push
