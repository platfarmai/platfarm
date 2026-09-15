# 008: 插件市场 M1（git 索引 + digest 安装）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

把第三方插件从"clone 仓库、pctl install 目录"升级为"从共享索引发现并安装"。M1 交付最小可用市场：git 索引仓 + digest 锁定的 `pctl market` 安装。设计见 [docs/features/plugin-marketplace.md](../../../docs/features/plugin-marketplace.md)。cosign 验签留到 M1.5。

## 方案（本次交付）

**索引仓** `platfarmai/market-index`：

```
plugins/<id>/index.yaml     # 一个插件的所有发布版本
```

```yaml
id: svc-translate
versions:
  - version: 1.2.0
    image: ghcr.io/vendor/translate
    digest: sha256:ab12...      # 必填——安装按 digest 锁定
    manifest: { ... }           # 内嵌 plugin.yaml（安装前展示权限）
    published: 2026-09-15
```

**pctl market** 命令：

| 命令 | 行为 |
|---|---|
| `pctl market search <q>` | 在索引里搜 id/标题/描述 |
| `pctl market info <id>[@ver]` | 展示版本、权限、digest |
| `pctl market install <id>[@ver]` | 解析版本 → 写 `plugin.yaml`（image+digest）到 `services/<id>` → 走既有 install 闸门 |

**索引来源**：`PF_MARKET_INDEX` 环境变量或 `--index`。支持 `file://` 本地路径、git URL 或 raw base URL。默认 `https://raw.githubusercontent.com/platfarmai/market-index/main`。

**复用安装链路**：`market install` 生成普通 `plugin.yaml`（trust: third-party、source.image+digest）后调既有 `runInstall`（校验→开库→凭据→起容器→契约测试）。无新沙箱代码。

### 延后（M1.5+）

- cosign 验签（双重锁定）
- `pctl publish`（构建→推镜像→开索引 PR）
- CI 审查管线（trivy + 沙箱试装）
- 市场 Web UI

### 不变式

- 索引里 digest **必填**——安装永远按 digest 锁定，绝不用浮动 tag。
- 安装前打印内嵌 manifest 的权限清单（App Store 式）。
- 不改沙箱/安装闸门；市场只是发现 + 拉取的前台。

## 影响面

`pctl`（新增 `market` 子命令 + 索引客户端）。新仓 `platfarmai/market-index`。文档。平台运行时不变。

## 验收标准

```bash
pctl market search translate
pctl market info svc-translate@1.2.0          # 显示 digest + 权限
PF_MARKET_INDEX=file://./_market pctl market install svc-translate@1.2.0
# → services/svc-translate/plugin.yaml 含 image@sha256；走 install 闸门
pctl market install svc-x                     # 未知 id → 清晰报错
# 索引条目缺 digest → 拒绝安装
```
