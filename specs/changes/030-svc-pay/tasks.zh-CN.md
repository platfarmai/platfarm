# 030 任务清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

分期见提案。前一阶段验收通过才开始下一阶段。后续阶段不许改已有表的形态——加列、加表一律用新的迁移文件向前滚。

## M1 — 骨架 + 微信服务商支付/退款/通知（直清模式）

- [x] 按 go 模板约定建 `services/svc-pay/`：`auth.go`（pfauth）、`migrate.go`、`/healthz` `/readyz` 排空、`Dockerfile`、契约三件套
- [x] `service.yaml`：挂载 `/api/pay`，`public_routes` 放行 `POST /notify/*`，`open_api` scope `pay.order.create|pay.order.query|pay.refund.create`，`roles.vocabulary` reviewer/operator，`admin_ui`，`data.database: pf_svc_pay`，env `PAY_DATABASE_URL` `PAY_SECRET_KEY`
- [x] 迁移 `001_init.sql`：`pay_isv` `pay_merchant` `pay_app` `pay_channel_config` `pay_passage` `pay_order` `pay_refund` `pay_notify`，唯一键按提案
- [x] 密钥密封（AES-256-GCM、`PAY_SECRET_KEY`、dev 自动持久化），任何凭据入库前先密封
- [x] `channel/`：`PayChannel` 接口、SUCCESS/FAIL/WAITING/UNKNOWN 四态、注册表；`wxpay` 适配器实现服务商 v3 JSAPI + Native（可切换沙箱端点）
- [x] 订单流：下单（`(mch_no, mch_order_no)` 幂等）、CAS 流转、查单、关单；资金类接口加请求 HMAC + 随机串重放窗口
- [x] 回调：适配器内验签、CAS 推进、`pay_notify` outbox、服务内 worker 按阶梯投递（0/30秒/60秒/150秒/10分/30分/1小时）；svc-jobs 已加入 accept 白名单，改走 webhook 调度留待对账 cron
- [x] 退款流，`refund_state` 回写支付单
- [x] 管理台：订单 + 商户只读列表（Vue3 + pf-ui，hash 路由）
- [x] 接线：`deploy/initdb.sql`、`.env.example`、svc-jobs accept 白名单、`pctl sync`
- [ ] QA：沙箱支付 → 重放回调只成功一次；重复商户订单号返回原单；退款回写；通知到达 SUCCESS；契约三件套；无 scope 的 app token → 403

## M2 — 进件 + 审核

- [x] 迁移 `002_applyment.sql`：`pay_applyment` + `pay_applyment_media`（图片字节留存，审核员提交渠道无需文件读权限）
- [x] 规范化资料校验（`channel.Applyment.Problems()`）+ 证件号/账号密封，所有响应只回掩码
- [x] 图片经 svc-file 预签名上传（控制台 → `/admin/applyments/upload-url` 代理 → PUT），资料里只存 file id，字节用操作员自己的 token 拉取
- [x] 审核流：draft → reviewing → approved/rejected，reviewer 角色闸门查 svc-grants `/mine`（admin 直通），驳回写理由，提交渠道前可取消
- [x] `wxpay_partner` 的 `OnboardingChannel`：媒体上传（multipart，meta 签名）、applyment4sub 提交（敏感字段 RSA-OAEP + `Wechatpay-Serial`）、查询、状态折叠、`sub_mchid` 并入商户 `pay_channel_config`；`/admin/channel-config/wxpay/platform-cert` 拉取并解密平台证书
- [x] 轮询 worker（1m → 2m → … → 1h 退避）覆盖 submitted/channel_auditing/need_sign/need_verify
- [x] 管理台：审核队列（状态筛选、详情、通过/驳回/同步/取消、新建与修改资料含图片上传）
- [x] QA（桩渠道 + 真实 Postgres）：draft → finished 回填 sub_mchid；驳回 → 改资料 → 重提；非 reviewer 通过 → 403；轮询遵守 `next_poll_at`；finished 为终态；渠道提交报错保持 approved 可重试
- [ ] QA（真实微信）：applyment4sub 提交需要真实子商户证照，未执行（请求签名、APIv3 解密、响应验签已由 `wxpay/live_test.go` 真实联调证明）

## M3 — 分账 + 对账 + 返佣台账（直清模式）

- [ ] 迁移 `003_division_recon.sql`：`pay_division_receiver` `pay_division` `pay_ledger` `pay_recon_batch` `pay_recon_diff`
- [ ] 接收方绑定 + 分账执行，比例快照入库，按子商户被授予的上限校验比例
- [ ] 下单时手续费快照（`amount * rate_bps / 10000`，四舍五入）+ 返佣台账行
- [ ] 每日账单拉取（微信交易账单）+ 差异比对；cron 走 svc-jobs
- [ ] 管理台：对账差异视图
- [ ] QA：构造少一笔的账单产生一条差异；已分过账的订单再次分账无副作用

## M4 — 归集模式（支付宝直付通 + 微信收付通）

- [ ] 迁移 `004_controlled.sql`：`pay_withdrawal`、余额列/台账类型
- [ ] `alipay_zft` 适配器：直付通进件、交易、结算确认、分账、单笔转账、账单
- [ ] `wxpay_ecommerce` 适配器：收付通进件、支付、分账、资金提现
- [ ] 提现流 + 余额不变量（台账求和 == 报告余额）进测试
- [ ] QA：沙箱 结算确认 → 分账 → 提现 后余额归零；直清模式的 isv 调用提现返回 400
