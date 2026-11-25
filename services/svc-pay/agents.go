// 代理商（分销）层级与级差分润。
//
// 代理商挂在服务商名下、归属服务商所在租户，按 parent_agent_no 成树（最多 maxAgentLevel 级）。
// rate_bps 是上级让给这条线（自己 + 所有下级）的费率，自上而下不增：
//
//	服务商返佣 >= 一级 >= 二级 >= ...
//
// 一笔订单沿商户的直属代理商向上走到一级，每一级实得 = 本级费率对应金额 − 紧邻下级费率对应金额。
// 各级之和等于一级那条线的金额，永远不超过服务商返佣——分润是从返佣里让出来的，不是另加的成本。
//
// 分润在下单时快照到订单（agent_shares），之后改费率、改归属、停用都不影响已有订单；
// 支付成功记 agent_share，退款按累计退款占比冲回 agent_share_refund（见 ledger.go）。
// 这是服务商对代理商的应付款：只记台账，线下打款后登记 agent_payout，不经渠道划拨。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

const (
	ledgerAgentShare       = "agent_share"
	ledgerAgentShareRefund = "agent_share_refund"
	ledgerAgentPayout      = "agent_payout"

	maxAgentLevel   = 5
	maxAgentRateBps = 1000 // 与服务商返佣费率同一上限
)

// agentKinds 代理商子账的科目；三者之和就是待结余额（可以为负：已结算后又发生退款）。
var agentKinds = []string{ledgerAgentShare, ledgerAgentShareRefund, ledgerAgentPayout}

// Agent pay_agent 一行。
type Agent struct {
	AgentNo       string `json:"agentNo"`
	AgentName     string `json:"agentName"`
	IsvNo         string `json:"isvNo"`
	ParentAgentNo string `json:"parentAgentNo"`
	Level         int    `json:"level"`
	RateBps       int    `json:"rateBps"`
	ContactName   string `json:"contactName"`
	ContactPhone  string `json:"contactPhone"`
	State         string `json:"state"`
	Remark        string `json:"remark"`
	TenantID      int    `json:"tenantId"`
	CreatedAt     string `json:"createdAt"`
}

// AgentShare 订单上某一级代理商的分润快照。RateBps 是这条线生效的费率（已按上级封顶）。
type AgentShare struct {
	AgentNo  string `json:"agentNo"`
	Level    int    `json:"level"`
	RateBps  int    `json:"rateBps"`
	Amount   int64  `json:"amount"`
	TenantID int    `json:"tenantId"`
}

const agentCols = `agent_no, agent_name, isv_no, parent_agent_no, level, rate_bps, contact_name, contact_phone,
	state, remark, tenant_id, created_at::text`

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.AgentNo, &a.AgentName, &a.IsvNo, &a.ParentAgentNo, &a.Level, &a.RateBps,
		&a.ContactName, &a.ContactPhone, &a.State, &a.Remark, &a.TenantID, &a.CreatedAt)
	return a, err
}

func loadAgent(ctx context.Context, q querier, agentNo string) (Agent, error) {
	return scanAgent(q.QueryRow(ctx, `SELECT `+agentCols+` FROM pay_agent WHERE agent_no=$1`, agentNo))
}

// loadAgentChain 从 agentNo 向上到一级，返回 [直属, …, 一级]。深度封顶，树被改坏成环也不会死循环。
func loadAgentChain(ctx context.Context, q querier, agentNo string) ([]Agent, error) {
	rows, err := q.Query(ctx, `WITH RECURSIVE chain AS (
			SELECT a.*, 1 AS depth FROM pay_agent a WHERE a.agent_no=$1
			UNION ALL
			SELECT p.*, c.depth+1 FROM pay_agent p JOIN chain c ON p.agent_no=c.parent_agent_no
			WHERE c.parent_agent_no<>'' AND c.depth<$2
		) SELECT `+agentCols+` FROM chain ORDER BY depth`, agentNo, maxAgentLevel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chain []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		chain = append(chain, a)
	}
	return chain, rows.Err()
}

// planAgentShares 级差分润（纯函数）。chain 是 [直属, …, 一级]。
//   - 生效费率自上而下封顶：一级不超过订单的返佣费率，每一级不超过它的上级。
//     建代理商时已经校验过，这里再封一次是因为之后服务商返佣或上级费率可能被调低。
//   - 每一级实得 = 本级金额 − 紧邻下级金额。各级金额都是 feeAmount(订单金额, 费率)，费率不增所以差值不为负，
//     合计恰好等于一级的金额，不会因为逐级四舍五入多分出去一分钱。
//   - 停用的代理商不记分润，它那一份留在服务商返佣里，不会转给上下级。
//
// 链上有代理商不属于订单的服务商时整单不分润：那是数据被改坏了，宁可不分也不能分错人。
func planAgentShares(chain []Agent, isvNo string, amount int64, rebateBps int) []AgentShare {
	if len(chain) == 0 || isvNo == "" || amount <= 0 || rebateBps <= 0 {
		return nil
	}
	for _, a := range chain {
		if a.IsvNo != isvNo {
			return nil
		}
	}
	rates := make([]int, len(chain))
	pools := make([]int64, len(chain))
	limit := rebateBps
	for i := len(chain) - 1; i >= 0; i-- {
		rate := chain[i].RateBps
		if rate > limit {
			rate = limit
		}
		if rate < 0 {
			rate = 0
		}
		limit = rate
		rates[i], pools[i] = rate, feeAmount(amount, rate)
	}
	var out []AgentShare
	for i := len(chain) - 1; i >= 0; i-- {
		own := pools[i]
		if i > 0 {
			own -= pools[i-1]
		}
		if own <= 0 || chain[i].State != stateActive {
			continue
		}
		out = append(out, AgentShare{AgentNo: chain[i].AgentNo, Level: chain[i].Level, RateBps: rates[i],
			Amount: own, TenantID: chain[i].TenantID})
	}
	return out
}

// orderAgentPlan 下单时算出这笔订单的分润快照：直属代理商、合计、逐级明细。
// 商户没有归属代理商、服务商没有返佣时都是空。
func orderAgentPlan(ctx context.Context, mch Merchant, amount int64, rebateBps int) (string, int64, []AgentShare, error) {
	if mch.AgentNo == "" || mch.IsvNo == "" {
		return "", 0, nil, nil
	}
	chain, err := loadAgentChain(ctx, db, mch.AgentNo)
	if err != nil {
		return "", 0, nil, err
	}
	// 链断了（代理商不存在）或没走到一级（层级超限 / 成环）：不分润，留痕待查。
	if len(chain) == 0 || chain[len(chain)-1].ParentAgentNo != "" {
		log.Printf(`{"event":"agent_chain_broken","mchNo":%q,"agentNo":%q}`, mch.MchNo, mch.AgentNo)
		return "", 0, nil, nil
	}
	shares := planAgentShares(chain, mch.IsvNo, amount, rebateBps)
	var total int64
	for _, s := range shares {
		total += s.Amount
	}
	return mch.AgentNo, total, shares, nil
}

// agentShareEntries 支付成功：逐级记分润（贷方，归属代理商，租户跟代理商走）。
// mch_no 留空：这些行不是商户的账，来源订单由 ref_id 追溯。
func agentShareEntries(o Order) []LedgerEntry {
	var out []LedgerEntry
	for _, s := range o.AgentShares {
		out = append(out, LedgerEntry{IsvNo: o.IsvNo, IfCode: o.IfCode, Kind: ledgerAgentShare,
			RefType: "order", RefID: o.PayOrderID, RefSub: s.AgentNo, AgentNo: s.AgentNo, Amount: s.Amount,
			Memo: fmt.Sprintf("%d 级代理分润", s.Level), TenantID: s.TenantID})
	}
	return out
}

// agentClawbackDue 这次退款该冲回的分润（纯函数）：按订单累计已退金额算出应冲回的总额，减去之前已冲回的。
// 按累计算而不是每笔退款各自四舍五入：三笔各退三分之一时，逐笔算可能每笔都舍成 0，
// 订单全退了代理商却还留着分润；累计算保证全额退款时恰好冲回全部，且永远不会多冲。
func agentClawbackDue(share, refundedTotal, orderAmount, alreadyClawed int64) int64 {
	due := proportional(share, refundedTotal, orderAmount) - alreadyClawed
	if rest := share - alreadyClawed; due > rest {
		due = rest
	}
	if due < 0 {
		return 0
	}
	return due
}

// agentShareRefundEntries 退款成功：逐级冲回分润（借方）。调用时这笔退款已经是 success。
// 同一订单的退款终态在事务里串行（rollupRefund 先更新了订单行），累计值读到的是已提交的结果。
func agentShareRefundEntries(ctx context.Context, q querier, o Order, r Refund) ([]LedgerEntry, error) {
	if len(o.AgentShares) == 0 {
		return nil, nil
	}
	refunded, err := refundedAmount(ctx, q, o.PayOrderID)
	if err != nil {
		return nil, err
	}
	// 其他退款单已经冲回的；本单自己的不算，重放时算出同样的金额、由唯一键挡掉。
	rows, err := q.Query(ctx, `SELECT l.agent_no, COALESCE(SUM(-l.amount),0) FROM pay_ledger l
		JOIN pay_refund f ON f.refund_order_id=l.ref_id
		WHERE l.kind=$1 AND l.ref_type='refund' AND f.pay_order_id=$2 AND l.ref_id<>$3
		GROUP BY l.agent_no`, ledgerAgentShareRefund, o.PayOrderID, r.RefundOrderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clawed := map[string]int64{}
	for rows.Next() {
		var no string
		var sum int64
		if err := rows.Scan(&no, &sum); err != nil {
			return nil, err
		}
		clawed[no] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []LedgerEntry
	for _, s := range o.AgentShares {
		due := agentClawbackDue(s.Amount, refunded, o.Amount, clawed[s.AgentNo])
		if due == 0 {
			continue
		}
		out = append(out, LedgerEntry{IsvNo: o.IsvNo, IfCode: o.IfCode, Kind: ledgerAgentShareRefund,
			RefType: "refund", RefID: r.RefundOrderID, RefSub: s.AgentNo, AgentNo: s.AgentNo, Amount: -due,
			Memo: fmt.Sprintf("退款冲回 %d 级代理分润", s.Level), TenantID: s.TenantID})
	}
	return out, nil
}

// agentBalance 代理商待结余额 = 分润 − 退款冲回 − 已结算。
func agentBalance(ctx context.Context, q querier, agentNo string) (int64, error) {
	var sum int64
	err := q.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM pay_ledger WHERE agent_no=$1 AND kind = ANY($2)`,
		agentNo, agentKinds).Scan(&sum)
	return sum, err
}

// ── 管理接口 ────────────────────────────────────────────────

// agentErr 带 HTTP 状态的业务错误：事务里的校验失败用它带出状态码，其余错误一律 500。
type agentErr struct {
	code int
	msg  string
}

func (e *agentErr) Error() string { return e.msg }

func agentFail(code int, format string, args ...any) error {
	return &agentErr{code: code, msg: fmt.Sprintf(format, args...)}
}

func writeAgentErr(c *gin.Context, err error, fallback string) {
	var ae *agentErr
	if errors.As(err, &ae) {
		c.JSON(ae.code, gin.H{"error": ae.msg})
		return
	}
	log.Printf("agent admin: %v", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": fallback})
}

// lockISVForAgents 锁住服务商行并返回它的租户与返佣费率。同一服务商下代理商的增改都先拿这把锁，
// 费率的上下级约束才不会被并发的两次修改一起绕过。分润从服务商的返佣里出，只有服务商所在租户能管。
func lockISVForAgents(ctx context.Context, tx pgx.Tx, claims *Claims, isvNo string) (tenant, rebateBps int, err error) {
	err = tx.QueryRow(ctx, `SELECT tenant_id, rebate_rate_bps FROM pay_isv WHERE isv_no=$1 FOR UPDATE`, isvNo).
		Scan(&tenant, &rebateBps)
	if notFound(err) {
		return 0, 0, agentFail(http.StatusNotFound, "服务商不存在")
	}
	if err != nil {
		return 0, 0, err
	}
	if !inTenant(claims, tenant) {
		return 0, 0, agentFail(http.StatusForbidden, "服务商不属于当前租户")
	}
	return tenant, rebateBps, nil
}

type createAgentIn struct {
	AgentNo       string `json:"agentNo"`
	AgentName     string `json:"agentName"`
	IsvNo         string `json:"isvNo"`
	ParentAgentNo string `json:"parentAgentNo"`
	RateBps       int    `json:"rateBps"`
	ContactName   string `json:"contactName"`
	ContactPhone  string `json:"contactPhone"`
	Remark        string `json:"remark"`
}

func createAgent(ctx context.Context, claims *Claims, in createAgentIn) (Agent, error) {
	// 只给了上级时，服务商跟上级走。
	if in.ParentAgentNo != "" {
		parent, err := loadAgent(ctx, db, in.ParentAgentNo)
		if notFound(err) {
			return Agent{}, agentFail(http.StatusNotFound, "上级代理商不存在")
		}
		if err != nil {
			return Agent{}, err
		}
		// 先看租户再比对服务商：别的租户拿不到“这个代理商号存在、挂在哪家服务商下”。
		if !inTenant(claims, parent.TenantID) {
			return Agent{}, agentFail(http.StatusForbidden, "上级代理商不属于当前租户")
		}
		if in.IsvNo == "" {
			in.IsvNo = parent.IsvNo
		}
		if in.IsvNo != parent.IsvNo {
			return Agent{}, agentFail(http.StatusBadRequest, "上级代理商不属于该服务商")
		}
	}
	if in.IsvNo == "" {
		return Agent{}, agentFail(http.StatusBadRequest, "isvNo 必填")
	}
	if in.AgentNo == "" {
		in.AgentNo = newOrderID("G")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return Agent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenant, limit, err := lockISVForAgents(ctx, tx, claims, in.IsvNo)
	if err != nil {
		return Agent{}, err
	}
	level, above := 1, "服务商返佣费率"
	if in.ParentAgentNo != "" {
		parent, err := loadAgent(ctx, tx, in.ParentAgentNo)
		if err != nil {
			return Agent{}, err
		}
		if parent.State != stateActive {
			return Agent{}, agentFail(http.StatusConflict, "上级代理商已停用")
		}
		if parent.Level >= maxAgentLevel {
			return Agent{}, agentFail(http.StatusBadRequest, "代理商最多 %d 级", maxAgentLevel)
		}
		level, limit, above = parent.Level+1, parent.RateBps, "上级代理商的费率"
	}
	if in.RateBps > limit {
		return Agent{}, agentFail(http.StatusBadRequest, "分润费率不能超过%s（%d）", above, limit)
	}
	a, err := scanAgent(tx.QueryRow(ctx, `INSERT INTO pay_agent
		(agent_no, agent_name, isv_no, parent_agent_no, level, rate_bps, contact_name, contact_phone, remark, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+agentCols,
		in.AgentNo, in.AgentName, in.IsvNo, in.ParentAgentNo, level, in.RateBps, in.ContactName, in.ContactPhone,
		in.Remark, tenant))
	if isUniqueViolation(err) {
		return Agent{}, agentFail(http.StatusConflict, "代理商号已存在")
	}
	if err != nil {
		return Agent{}, err
	}
	return a, tx.Commit(ctx)
}

// handleCreateAgent POST /admin/agents
func handleCreateAgent(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in createAgentIn
	if c.ShouldBindJSON(&in) != nil || in.AgentName == "" || (in.IsvNo == "" && in.ParentAgentNo == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "agentName 必填，isvNo 与 parentAgentNo 至少给一个"})
		return
	}
	if in.RateBps < 0 || in.RateBps > maxAgentRateBps {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("rateBps 须在 0..%d（万分位）", maxAgentRateBps)})
		return
	}
	a, err := createAgent(c.Request.Context(), claims, in)
	if err != nil {
		writeAgentErr(c, err, "创建失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": a})
}

type updateAgentIn struct {
	AgentName    *string `json:"agentName"`
	RateBps      *int    `json:"rateBps"`
	ContactName  *string `json:"contactName"`
	ContactPhone *string `json:"contactPhone"`
	Remark       *string `json:"remark"`
	State        *string `json:"state"`
}

// updateAgent 只改给出的字段。上级与服务商建好后不能改：挪动一棵子树会让层级和费率约束都要重算，
// 需要换线时新建代理商、把商户改挂过去，历史订单的快照不受影响。
func updateAgent(ctx context.Context, claims *Claims, agentNo string, in updateAgentIn) (Agent, error) {
	cur, err := loadAgent(ctx, db, agentNo)
	if notFound(err) {
		return Agent{}, agentFail(http.StatusNotFound, "代理商不存在")
	}
	if err != nil {
		return Agent{}, err
	}
	if !inTenant(claims, cur.TenantID) {
		return Agent{}, agentFail(http.StatusForbidden, "代理商不属于当前租户")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return Agent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, limit, err := lockISVForAgents(ctx, tx, claims, cur.IsvNo)
	if err != nil {
		return Agent{}, err
	}
	a, err := loadAgent(ctx, tx, agentNo)
	if err != nil {
		return Agent{}, err
	}
	if in.AgentName != nil {
		if *in.AgentName == "" {
			return Agent{}, agentFail(http.StatusBadRequest, "agentName 不能为空")
		}
		a.AgentName = *in.AgentName
	}
	if in.ContactName != nil {
		a.ContactName = *in.ContactName
	}
	if in.ContactPhone != nil {
		a.ContactPhone = *in.ContactPhone
	}
	if in.Remark != nil {
		a.Remark = *in.Remark
	}
	if in.State != nil {
		if *in.State != stateActive && *in.State != stateDisabled {
			return Agent{}, agentFail(http.StatusBadRequest, "state 只能是 active 或 disabled")
		}
		a.State = *in.State
	}
	if in.RateBps != nil && *in.RateBps != a.RateBps {
		rate, above := *in.RateBps, "服务商返佣费率"
		if rate < 0 || rate > maxAgentRateBps {
			return Agent{}, agentFail(http.StatusBadRequest, "rateBps 须在 0..%d（万分位）", maxAgentRateBps)
		}
		if a.ParentAgentNo != "" {
			parent, err := loadAgent(ctx, tx, a.ParentAgentNo)
			if err != nil {
				return Agent{}, err
			}
			limit, above = parent.RateBps, "上级代理商的费率"
		}
		if rate > limit {
			return Agent{}, agentFail(http.StatusBadRequest, "分润费率不能超过%s（%d）", above, limit)
		}
		var floor int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(rate_bps),0) FROM pay_agent WHERE parent_agent_no=$1`,
			agentNo).Scan(&floor); err != nil {
			return Agent{}, err
		}
		if rate < floor {
			return Agent{}, agentFail(http.StatusBadRequest, "分润费率不能低于下级代理商的费率（%d），请先调低下级", floor)
		}
		a.RateBps = rate
	}
	a, err = scanAgent(tx.QueryRow(ctx, `UPDATE pay_agent SET agent_name=$2, rate_bps=$3, contact_name=$4, contact_phone=$5,
		remark=$6, state=$7, updated_at=now() WHERE agent_no=$1 RETURNING `+agentCols,
		agentNo, a.AgentName, a.RateBps, a.ContactName, a.ContactPhone, a.Remark, a.State))
	if err != nil {
		return Agent{}, err
	}
	return a, tx.Commit(ctx)
}

// handleUpdateAgent PUT /admin/agents/:agentNo
func handleUpdateAgent(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in updateAgentIn
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体必须是合法 JSON"})
		return
	}
	a, err := updateAgent(c.Request.Context(), claims, c.Param("agentNo"), in)
	if err != nil {
		writeAgentErr(c, err, "保存失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": a})
}

// handleListAgents GET /admin/agents?isvNo=：代理商及其商户数、累计分润、退款冲回、已结算、待结余额。
func handleListAgents(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	scope, args := tenantScope(claims)
	sql := `SELECT ` + agentCols + `,
			(SELECT count(*) FROM pay_merchant m WHERE m.agent_no=pay_agent.agent_no),
			COALESCE(l.earned,0), COALESCE(l.clawed,0), COALESCE(l.paid,0)
		FROM pay_agent LEFT JOIN (
			SELECT agent_no AS no,
				SUM(amount)  FILTER (WHERE kind='` + ledgerAgentShare + `') AS earned,
				SUM(-amount) FILTER (WHERE kind='` + ledgerAgentShareRefund + `') AS clawed,
				SUM(-amount) FILTER (WHERE kind='` + ledgerAgentPayout + `') AS paid
			FROM pay_ledger WHERE agent_no<>'' GROUP BY agent_no
		) l ON l.no=pay_agent.agent_no
		WHERE ` + scope
	if isv := c.Query("isvNo"); isv != "" {
		args = append(args, isv)
		sql += fmt.Sprintf(` AND isv_no=$%d`, len(args))
	}
	sql += ` ORDER BY isv_no, level, created_at LIMIT 500`
	rows, err := db.Query(c.Request.Context(), sql, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	type agentItem struct {
		Agent
		MerchantCount int   `json:"merchantCount"`
		Earned        int64 `json:"earned"`
		Refunded      int64 `json:"refunded"`
		Paid          int64 `json:"paid"`
		Balance       int64 `json:"balance"`
	}
	items := []agentItem{}
	for rows.Next() {
		var it agentItem
		a := &it.Agent
		if rows.Scan(&a.AgentNo, &a.AgentName, &a.IsvNo, &a.ParentAgentNo, &a.Level, &a.RateBps, &a.ContactName,
			&a.ContactPhone, &a.State, &a.Remark, &a.TenantID, &a.CreatedAt,
			&it.MerchantCount, &it.Earned, &it.Refunded, &it.Paid) == nil {
			it.Balance = it.Earned - it.Refunded - it.Paid
			items = append(items, it)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// handleSetMerchantAgent PUT /admin/merchants/:mchNo/agent {agentNo}：把商户归到某个代理商名下，空串为解除。
// 只影响之后的新订单。归属决定服务商的返佣让给谁，所以由服务商所在租户决定，而不是商户所在租户。
func handleSetMerchantAgent(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in struct {
		AgentNo string `json:"agentNo"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体必须是合法 JSON"})
		return
	}
	ctx := c.Request.Context()
	mch, err := loadMerchant(ctx, c.Param("mchNo"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "商户不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if mch.IsvNo == "" {
		// 没挂服务商的商户不产生返佣，也就没有可分的润；这里按商户自己的租户把门。
		if denyOtherTenant(c, claims, mch.TenantID, "商户") {
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "商户没有挂服务商，不能归属代理商"})
		return
	}
	isvTenant, err := ownerTenant(ctx, "isv", mch.IsvNo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if denyOtherTenant(c, claims, isvTenant, "商户所属服务商") {
		return
	}
	if in.AgentNo != "" {
		a, err := loadAgent(ctx, db, in.AgentNo)
		if notFound(err) || (err == nil && a.IsvNo != mch.IsvNo) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "代理商不存在，或不属于商户所在的服务商"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}
		if a.State != stateActive {
			c.JSON(http.StatusConflict, gin.H{"error": "代理商已停用"})
			return
		}
	}
	if _, err := db.Exec(ctx, `UPDATE pay_merchant SET agent_no=$2, updated_at=now() WHERE mch_no=$1`,
		mch.MchNo, in.AgentNo); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	log.Printf(`{"event":"merchant_agent","mchNo":%q,"from":%q,"to":%q,"by":%q}`, mch.MchNo, mch.AgentNo, in.AgentNo, claims.Username)
	c.JSON(http.StatusOK, gin.H{"mchNo": mch.MchNo, "agentNo": in.AgentNo})
}

// recordAgentPayout 登记一笔已在线下完成的分润结算（借方）。不超过待结余额；
// 同一 payoutNo 重复登记不会记两次。锁代理商行，两笔并发的登记不会一起超额。
func recordAgentPayout(ctx context.Context, a Agent, payoutNo string, amount int64, memo string) (dup bool, balance int64, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	if err := tx.QueryRow(ctx, `SELECT agent_no FROM pay_agent WHERE agent_no=$1 FOR UPDATE`, a.AgentNo).Scan(&locked); err != nil {
		return false, 0, err
	}
	balance, err = agentBalance(ctx, tx, a.AgentNo)
	if err != nil {
		return false, 0, err
	}
	var recorded int64
	err = tx.QueryRow(ctx, `SELECT -amount FROM pay_ledger WHERE kind=$1 AND ref_type=$1 AND ref_id=$2 AND ref_sub=$3`,
		ledgerAgentPayout, payoutNo, a.AgentNo).Scan(&recorded)
	switch {
	case err == nil && recorded == amount:
		return true, balance, nil
	case err == nil:
		return false, balance, agentFail(http.StatusConflict, "结算单号 %s 已登记过，金额不同", payoutNo)
	case !notFound(err):
		return false, balance, err
	}
	if amount > balance {
		return false, balance, agentFail(http.StatusConflict, "超出待结余额（%d 分）", balance)
	}
	if _, err := postLedger(ctx, tx, []LedgerEntry{{IsvNo: a.IsvNo, Kind: ledgerAgentPayout, RefType: ledgerAgentPayout,
		RefID: payoutNo, RefSub: a.AgentNo, AgentNo: a.AgentNo, Amount: -amount, Memo: memo, TenantID: a.TenantID}}); err != nil {
		return false, balance, err
	}
	return false, balance - amount, tx.Commit(ctx)
}

// handleAgentPayout POST /admin/agents/:agentNo/payouts {amount, payoutNo, memo}
func handleAgentPayout(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in struct {
		Amount   int64  `json:"amount"`
		PayoutNo string `json:"payoutNo"`
		Memo     string `json:"memo"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount 必填且为正（分）"})
		return
	}
	if len(in.PayoutNo) > 64 || len(in.Memo) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payoutNo 最长 64，memo 最长 200"})
		return
	}
	ctx := c.Request.Context()
	a, err := loadAgent(ctx, db, c.Param("agentNo"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "代理商不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if denyOtherTenant(c, claims, a.TenantID, "代理商") {
		return
	}
	if in.PayoutNo == "" {
		in.PayoutNo = newOrderID("AP")
	}
	memo := or(in.Memo, "分润结算") + "（登记人 " + claims.Username + "）"
	dup, balance, err := recordAgentPayout(ctx, a, in.PayoutNo, in.Amount, memo)
	if err != nil {
		writeAgentErr(c, err, "登记失败")
		return
	}
	if !dup {
		log.Printf(`{"event":"agent_payout","agentNo":%q,"payoutNo":%q,"amount":%d,"by":%q}`, a.AgentNo, in.PayoutNo, in.Amount, claims.Username)
	}
	c.JSON(http.StatusOK, gin.H{"payoutNo": in.PayoutNo, "amount": in.Amount, "balance": balance, "idempotent": dup})
}

func registerAgentRoutes(admin *gin.RouterGroup) {
	admin.POST("/agents", handleCreateAgent)
	admin.GET("/agents", handleListAgents)
	admin.PUT("/agents/:agentNo", handleUpdateAgent)
	admin.POST("/agents/:agentNo/payouts", handleAgentPayout)
	admin.PUT("/merchants/:mchNo/agent", handleSetMerchantAgent)
}
