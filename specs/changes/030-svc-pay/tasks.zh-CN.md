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

- [x] 迁移 `003_division_recon.sql`：`pay_division_receiver` `pay_division`（`pay_order_id` 上 `state<>'failed'` 的部分唯一索引）`pay_ledger`（`(kind, ref_type, ref_id, ref_sub)` 唯一 = 重放安全）`pay_recon_batch` `pay_recon_diff`；`pay_order.division_state/rebate_*`、`pay_isv.rebate_rate_bps` 向前加列
- [x] 接收方绑定（`/admin/division/receivers` → 微信 `profitsharing/receivers/add`，名称 RSA-OAEP）+ 分账执行（`POST /v1/divisions`，scope `pay.division.create`），比例快照入库，逐个按授权上限校验、合计按商户参数 `max_division_ratio_bps`（默认 30%）封顶，金额向下取整；下单时 `channelExtra.profitSharing` 带上 `settle_info.profit_sharing`
- [x] 下单时手续费 + 返佣快照（四舍五入）→ 成功记 `fee`/`rebate`，退款成功按比例冲回 `fee_refund`/`rebate_refund`，分账成功逐接收方记 `sharing`；`onOrderTerminal`/`onRefundTerminal` 统一三条终态路径（顺带修了 M1 的漏洞：`Pay` 同步返回 SUCCESS 时没有入通知队列）
- [x] 每日账单拉取（`GET /v3/bill/tradebill` → 签名下载 → 按表头名解析 CSV，反引号字段，剔除汇总行）+ `diffBill`（missing_local / missing_channel / amount_mismatch / state_mismatch，支付 + 退款）；`/internal/recon/daily` 给 svc-jobs cron（服务 token），`/admin/recon/schedule` 用管理员自己的 token 注册计划（svc-jobs 的计划接口只认 admin）
- [x] 管理台：分账·台账（接收方、分账单、台账）+ 对账（手动跑、批次、差异、标记处理、注册计划）
- [x] QA（桩渠道 + 真实 Postgres）：构造少一笔的账单恰好产生一条 `missing_channel` 差异；已分过账的订单再分返回原单、渠道 `Share` 只调一次；分账失败释放订单可重试；台账重放不新增行；对账批次幂等（除非 `force`）；`no_bill` / `failed` 状态

## M4 — 归集模式（支付宝直付通 + 微信收付通）

- [x] 迁移 `004_controlled.sql`：`pay_settle`、`pay_withdrawal`、`pay_order.settle_state`；台账类型 `settle` / `withdrawal` / `withdrawal_reversal`（M3 已定义，本阶段入账）
- [x] `alipay_zft` 适配器：直付通进件、交易（分账冻结）、结算确认、分账、单笔转账、账单
- [x] `wxpay_ecommerce` 适配器：收付通进件、普通/合单支付（声明分账冻结）、分账与完结分账、预约提现、余额
- [x] 提现流 + 余额不变量（台账求和 == 渠道报告余额）在提现前核对；不一致直接拒绝，不调用渠道
- [x] QA（桩渠道 + 真实 Postgres）：结算确认 → 分账 → 提现后余额归零；直清模式调用提现/结算/余额返回 400；提现幂等；超额与账实不符被拒绝；已成功提现被渠道退回记 `withdrawal_reversal`；两个渠道的伪服务器测试

### M4 审计修正（与上面不一致处以此为准）

- [x] `alipay_zft` 没有提现、没有平台侧余额：确认结算即结算到二级商户进件时的默认结算账户。移除 `uni.transfer` / `bill.balance.query`（它们动的是平台自己的钱）。结算金额 = 交易金额 − 已退款（支付宝规则，不扣分账）；先结算后分账，只有 `holdForDivision` 时才在结算时带 `royalty_freeze`，最后一笔分账带 `royalty_finish`；下单不再对每笔订单冻结
- [x] `wxpay_ecommerce`：分账在确认结算之前（分账不解冻；确认结算 = 完结分账释放剩余，`NOT_ENOUGH` 视为已释放）；下线 `WX_COMBINE`，待按子单建模（此前子单金额与收款子商户取自调用方输入）
- [x] 提现规则取代“台账 == 渠道余额”的严格不变量：按商户+渠道加 advisory lock，发起即记借方（预占），失败或银行退票冲回；可提 = min(台账, 渠道可用 − 渠道尚未确认的提交)；两边差额作为漂移展示，不阻断。迁移 `007_controlled_money.sql`
- [x] 结算、提现结果与台账同事务落地；结果未知的结算/提现用原单号重提；成功的提现 10 天内复查（银行退票）；退款在途时不能结算、结算中不能退款；结算后的退款扣可提现台账
- [ ] 修正后的直付通“结算 → 分账”与收付通“完结分账 / 提现”在沙箱或真实渠道上的验证

## M5 — 渠道商能力（提案原先列在“不做”里的代理商层级，现已纳入）

- [x] 开放 API 产品化：应用/通道/渠道参数自助配置、`sandbox` 渠道（仅 `PAY_SANDBOX=on` 时注册，`POST /v1/orders/:mchOrderNo/mock` 只认沙箱订单）、退款查询、对接文档 `docs/pay-open-api.zh-CN.md`
- [x] 迁移 `009_agents.sql`：`pay_agent`（挂在服务商下，`parent_agent_no` 成树，最多 5 级）、`pay_merchant.agent_no`、`pay_order.agent_no/agent_amount/agent_shares`、`pay_ledger.agent_no`
- [x] 级差分润：`rate_bps` 是让给整条线的费率，`服务商返佣 >= 一级 >= 二级 >= … >= 0`；某级实得 = 本级金额 − 紧邻下级金额，合计恒等于一级那条线的金额，不超过返佣。下单时快照，之后改费率、改归属不影响已有订单
- [x] 台账：支付成功记 `agent_share`，退款按累计已退金额冲回 `agent_share_refund`（分多笔退完恰好冲回全部，不多冲），线下结算后登记 `agent_payout`（不超过待结余额，按结算单号幂等）。分润只记账，不经渠道划拨；代理商子账不混进商户/服务商台账合计
- [x] 管理端：`/admin/agents`（建、改、列表含汇总）、`/admin/agents/:agentNo/payouts`、`/admin/merchants/:mchNo/agent`；归属与费率由服务商所在租户决定。开放 API 的订单不带分润字段，管理端订单只对分润所属租户展示明细
- [x] 管理台「代理商·分润」页：层级树、本级实得、待结余额、登记结算、分润明细、商户归属
- [x] QA（真实 Postgres + sandbox 渠道）：级差与封顶、层级与费率校验、下单 → 支付 → 分三笔退完 → 结算的全流程、终态重放不重复记账、租户隔离
- [ ] 代理商自己登录查看分润（目前只有服务商/平台运营能看）
- [ ] 分润自动打款（目前线下结算后人工登记）
