# 近期功能与改进

记录截至 `48c1dfd`（已推送 `origin/main`）的平台增量。设计细节仍以 `docs/architecture-v2.md` 和 `specs/changes/` 为准。

## 能力服务

- **svc-file**：预签名直传 MinIO，元数据与归属判定在服务内。公开文件用随机 token，不用自增 id。超过 1 小时仍无对象的元数据行由 sweeper 回收。JSON 响应默认 AES-GCM 加密，`/healthz`、`/readyz`、`/metrics`、`/me` 除外。
- **svc-notify**：邮件、webhook、站内信统一出口。邮件和 webhook 走 outbox，失败退避。未配 SMTP 时只记日志。
- **svc-jobs**：异步任务和 cron。回调只允许 http(s)，拒绝内网主机、裸 IP 和云元数据地址。service token 只附在平台服务名上。
- **svc-grants**：业务角色集中分配。服务只能查自己的成员，写操作要 admin。
- **svc-data**：可配置数据转发。管理页在 `/data/console`，数据接口留在 `/api`。

## 账号与身份

- 自助注册（`PF_SELF_REGISTER`）、TOTP、找回密码、邮箱验证。账号中心在 `/api/users/account/`。
- 注册按来源 IP 限流。邮箱写入前转小写，唯一索引与查询一致。
- 改角色、禁用、换租户会吊销旧 token。网关回查 Redis 里的吊销名单。
- 多租户：`tenants` 表、用户归属、token 里的 `tenantId`。文件、通知、任务、定时计划、角色分配按租户过滤。`tenant 0` 和平台 admin 仍可跨租户，避免藏起旧数据。
- 微信登录已接到 svc-oauth。`WECHAT_STUB=on` 可在本地走完 authorize → callback → exchange。没有 AppId 时不会打微信线上接口。
- 签名密钥按公钥指纹做 `kid`。`pctl rotate-keys` 把旧公钥放进 `.keys/jwks-extra`，JWKS 同时公布新旧钥。Kong 仍只认当前公钥，轮换后在途 token 过网关会失效，直到重启网关。

## 网关与插件

- 同一路由上的 calls 闸门、吊销检查和 SSO cookie 映射合并成一个 `pre-function`。分开挂会触发 Kong 的插件实体唯一性冲突，网关起不来。
- 声明了 `admin_routes` 的路径由网关检查 `role=admin`。
- 插件 `permissions.egress` 会生成 squid 白名单代理。插件网络仍是 `internal`。
- 安装前检查镜像 tag 或 digest，以及内存限制写法。
- 开放平台应用可设 `quota_per_day`。超额在下次领 token 时返回 429。用量来自 Loki，Loki 不可用时放行。

## 可观测、备份、发布

- observability profile：Loki、Promtail、Grafana、Prometheus。svc-file 暴露 `/metrics`。
- `pctl backup` / `restore`。`backup --schedule` 注册每日任务。`uninstall --purge` 前给插件库做快照。
- `pctl release list|deploy|rollback` 只记录第一方镜像 digest，不改正在跑的容器。

## SDK 与 UI

- 验签唯一实现：`sdk/go/pfauth`、`sdk/py/pfauth.py`、`sdk/php/pfauth.php`、`sdk/rust`。Go 第一方服务只留 gin 适配层。`pctl sync` 把 SDK 拷进服务构建目录，`services/*/sdk/` 不入库。
- 响应加密的浏览器解密在 `sdk/js`。账号中心登录后保存 `dataKey`，并用 `pfInstallFetch` 解开带 `X-PF-Encrypted` 的响应。
- pf-ui（`/platform/ui/v1/`）是令牌加原生 Web Components，不是 shadcn，也不是 Vue/React 组件库。管理壳、Vue 控制台、账号中心、demo 都引这一份。
- 令牌已改成 shadcn zinc 风格：中性底、细边框、8/12px 圆角。管理壳改为左侧栏。组件名和页面结构没换。

## 部署

- K8s 基座：`deploy/k8s/base/` 有 namespace、auth、Kong Deployment、Ingress，以及一份要手改 `SVC` 的服务模板。没有在集群上跑过，也没有生成每个服务的清单。
- Path B：`PF_SERVICES_DIR` 指向镜像部署目录里的清单。文档在 `docs/adding-a-service-on-path-b.md`。

## 还没做

- 插件市场 M2–M4，安装时的镜像漏洞扫描和强制 cosign。
- 现有 Go 服务以外的语言服务没有逐个迁到新 SDK。py/php/rust 只保证模板引用 SDK。
- 指标只接了 svc-file。Prometheus 容器在 profile 里，没有确认抓到数据。
- 响应加密只常态开启在 svc-file。用户管理、OpenAPI、控制台这些 Vue 页没有接 `pfInstallFetch`。
- 微信没有真实 AppId 的线上往返。
- K8s 清单未在集群验证。
