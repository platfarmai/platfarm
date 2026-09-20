# 012 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl market：索引 `signature{mode,identity,issuer,publicKey}` 字段
- [x] pctl：cosign 探测（PATH / PF_COSIGN）+ 安装时 `cosign verify`（keyless + key）
- [x] pctl：配置 PF_COSIGN_MODE（enforce|warn）+ PF_COSIGN_REQUIRED
- [x] 对 image@digest 验签；失败 → 拒绝安装
- [x] 文档 ADR #27；market-index 示例签名条目
- [x] QA：有效签名通过 / 篡改拒绝 / cosign 缺失 warn|error / 未签强制拒绝
