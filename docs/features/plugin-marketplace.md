# Feature 设计：第三方插件市场（Plugin Marketplace）

> 状态：设计定稿，未排期（实施时转 specs/changes/003 提案）
> 前置依赖：specs/changes/002（插件沙箱体系，已落地）
> 一句话：**货架用 OCI、索引用 git、信任用签名、审查用既有闸门自动化**——市场不是新系统，是 pctl 的第五个动词加一个公共索引仓库。

---

## 1. 目标与非目标

**目标**：让外部开发者能发布插件、让任意 Platfarm 实例能安全地发现/安装/升级/吊销插件。市场在既有机制（digest 锁定、permissions 声明、install 闸门、凭据体系）之上只加三层：**分发、发现、信任**。

**非目标（首期）**：付费/抽成、发布方门户 UI、评分社区——预留钩子，不实现。

## 2. 总体架构

```
发布方                    市场                          消费方（Platfarm 实例）
pctl publish ──→ [OCI Registry] 镜像+签名 ←── pctl install market://translate@1.2.0
       │                    ↑ digest                         │
       └──提交清单──→ [市场索引] 元数据/权限/版本 ──解析──────┘
                          │
                     [审查管线] 扫描 + 沙箱试装 + 权限评级
                          │
                     [市场 Web] 发现 / 权限展示（只读渲染索引）
```

## 3. 分发底座：OCI Registry，不自建存储

插件交付物本来就是镜像——市场不发明存储，ghcr.io/Harbor 即货架；市场自身只持有**索引**（Helm/Homebrew 验证过的路线：仓库存包，索引存"什么包在哪、可不可信"）。

插件包 = 镜像（按 digest）+ plugin.yaml + openapi + 契约测试 + README。

**索引条目格式**（`market-index` 仓库 `plugins/<id>/<version>.yaml`）：

```yaml
id: svc-plugin-translate
version: 1.2.0
image: ghcr.io/vendor/translate@sha256:ab12...
signature: <cosign 签名引用>
publisher: vendor-a              # 对应已注册发布方公钥
manifest: { ... }                # 内嵌 plugin.yaml（市场审查的对象）
review:
  tier: verified                 # verified | community | unreviewed
  scanned: 2026-09-04
  permissionScore: low           # 权限自动评级，见 §5
```

## 4. MVP：Git 仓库即市场（winget/homebrew-tap 模式）

- `platfarm/market-index` 仓库：**PR 即上架申请，CI 即审查管线，merge 即发布**
- `pctl market search/info/install` 直接读该仓库（clone / raw 拉取）
- 收益：上架有 review、版本有 git 历史、吊销就是一个 revert、零运维——与"文件是唯一真相源"的平台哲学同构
- 升级成 API 服务的触发信号：索引条目上千 / 需要下载统计、评分、付费

## 5. 供应链安全（核心，其余都是皮）

| 机制 | 做法 | 依托现状 |
|---|---|---|
| **双重锁定** | 索引记 digest + cosign 签名；install 验签名→验 digest→才拉镜像 | digest 字段已有，新增 cosign 验证 |
| **发布方身份** | 注册制：发布方公钥入索引仓库；签名对不上 = 拒装 | 新增 |
| **自动审查** | 索引仓库 CI：清单校验（复用 pctl check）→ trivy 镜像扫描 → **沙箱试装**（隔离 Platfarm 实例跑 install + 契约测试 + 恶意演习探测——002 的 evil drill 自动化） | install 闸门已有，搬进 CI |
| **权限评级** | permissions 自动打分：无权限=green；needs_identity=yellow；calls+egress=orange。市场页像 App Store 展示权限清单 | permissions 已有 |
| **升级再审** | 新版本权限 diff ≠ 空 → 重新审查 + 安装端高亮确认 | upgrade 权限 diff 已有 |
| **吊销** | 索引标记 malicious → install 前查吊销表；`pctl market audit` 对照本地已装插件告警 | 新增 |

## 6. 命令面（pctl 自然延伸）

```bash
# 发布方
pctl publish ./my-plugin --registry ghcr.io/me
#   构建 → 推镜像 → cosign 签名 → 生成索引条目 → 向 market-index 开 PR

# 消费方
pctl market search translate
pctl market info svc-plugin-translate      # 权限清单 / 评级 / 版本
pctl install market://svc-plugin-translate@1.2.0
#   验签 → 解析 digest → 走既有 install 沙箱闸门（后半段零改动）
pctl market audit                          # 已装插件对照吊销表 / 新版本
```

`install` 只是多了"从市场解析来源"的前置步骤——这是 002 把 install 闸门做对的直接红利。

## 7. 商业化钩子（预留，不先做）

- **计量点天然存在**：每个插件有自己的 service token，网关按 `svc` claim 计数即用量账单原始数据
- **License**：付费插件的 license key 走 `.env.plugins/<id>.env` 注入，插件启动自验——复用凭据通道
- 抽成/订阅等有真实发布方后再议

## 8. 实施切分（届时转 specs/changes/003）

| 阶段 | 内容 | 估算 |
|---|---|---|
| M1 | cosign 验签进 install + market-index 仓库 + `market search/info/install` | 2~3 天 |
| M2 | 索引仓库 CI 审查管线（trivy + 沙箱试装自动化） | 2 天 |
| M3 | 市场 Web（只读渲染索引）+ 下载统计 | 按需 |
| M4 | API 服务化 + 发布方门户 + 计量 | 有生态信号才做 |

## 9. 决策记录（并入 architecture-v2 ADR 时取号）

| 决策 | 理由 | 重估时机 |
|---|---|---|
| 分发用 OCI Registry，市场只存索引 | 镜像本来就是交付物；不自建存储 | — |
| MVP 索引 = git 仓库（PR 上架 / CI 审查 / revert 吊销） | 与文件真相源哲学同构，零运维 | 条目上千或需付费/统计 |
| 信任 = cosign 签名 + digest 双重锁定 + 发布方公钥注册 | 供应链完整性是市场的生存底线 | — |
| 审查 = 既有 install 闸门 + evil drill 的 CI 自动化 | 复用 002 已验证的沙箱验收 | — |
