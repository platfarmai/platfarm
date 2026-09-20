# 012: 插件签名验证（cosign，M1.5）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

市场 M1（specs/008）按 digest 锁定但不验真伪。加 cosign 签名验证,使 `pctl market install` 只接受可信发布方签名的镜像。cosign 是外部工具;pctl 保持单 Go 二进制,调外部 CLI。

## 方案

### 索引：签名引用

索引条目新增 `signature` 块：

```yaml
versions:
  - version: 1.2.0
    image: ghcr.io/vendor/translate
    digest: sha256:ab12...
    signature:
      mode: keyless           # keyless | key
      identity: "https://github.com/vendor/.github/workflows/release.yml@refs/tags/v1.2.0"
      issuer: "https://token.actions.githubusercontent.com"
      # mode: key → publicKey: <PEM 或路径>
```

### pctl：安装时验签

`pctl market install`：
1. 解析版本 → `image@digest`（不变）
2. 条目有 `signature` → 对 `image@digest` 跑 `cosign verify`
   - keyless：`cosign verify --certificate-identity <id> --certificate-oidc-issuer <issuer>`
   - key：`cosign verify --key <pem>`
3. PATH 无 cosign → 报错并给安装提示（`PF_COSIGN_MODE=warn` 降级为警告）
4. 验签失败 → 拒绝安装
5. 条目无 `signature` → `PF_COSIGN_REQUIRED=true` 拒绝；默认警告

`PF_COSIGN` 覆盖 cosign 二进制路径。

### 不变式

- 对 **digest** 验签,不对 tag（M1 已强制 digest）。
- pctl 不加 Go 加密依赖;cosign 运行时可选（warn 模式）但推荐。
- 签名/发布（`pctl publish` + CI 签名）留 M2+;本 spec 只做消费侧验签。

## 影响面

`pctl`、market-index（条目可选 `signature`）、文档。平台运行时不变。

## 验收标准

```bash
# 有效签名 + cosign 存在 → 安装继续
# 篡改/未签镜像 + 要求签名 → cosign verify 失败 → 拒绝
# 未装 cosign，PF_COSIGN_MODE=warn → 警告+继续；默认 → 报错
# 条目无签名，PF_COSIGN_REQUIRED=true → 拒绝；默认 → 警告
```
