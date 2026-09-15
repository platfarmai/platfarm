# 008 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl market.go：索引客户端（file:// + https raw base）、类型
- [x] `pctl market search <q>`——搜 id/标题/描述
- [x] `pctl market info <id>[@ver]`——版本、权限、digest
- [x] `pctl market install <id>[@ver]`——解析→写 plugin.yaml（image@digest）→走 runInstall 闸门；缺 digest 拒绝
- [x] main.go：接 `market` 子命令；usage
- [x] platfarmai/market-index 仓：README + 示例 plugins/<id>/index.yaml
- [x] 文档：市场使用；ADR #23
- [x] QA：file:// 索引 search/info/install；未知 id + 缺 digest 报错；提交推送
