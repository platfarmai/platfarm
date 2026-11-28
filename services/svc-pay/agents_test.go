package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"platfarm/svc-pay/channel"
)

func chainOf(isvNo string, rates ...int) []Agent {
	// rates 自上而下给（一级在前），返回 planAgentShares 要的 [直属, …, 一级]。
	chain := make([]Agent, len(rates))
	for i, rate := range rates {
		chain[len(rates)-1-i] = Agent{AgentNo: fmt.Sprintf("G%d", i+1), IsvNo: isvNo, Level: i + 1, RateBps: rate, State: stateActive}
	}
	return chain
}

func sharesByAgent(shares []AgentShare) map[string]int64 {
	out := map[string]int64{}
	for _, s := range shares {
		out[s.AgentNo] = s.Amount
	}
	return out
}

func TestPlanAgentShares(t *testing.T) {
	// 返佣 38，三级 30/20/10：一万分的单各得 10，合计 30，剩下 8 留给服务商。
	got := sharesByAgent(planAgentShares(chainOf("ISV", 30, 20, 10), "ISV", 10000, 38))
	if got["G1"] != 10 || got["G2"] != 10 || got["G3"] != 10 || len(got) != 3 {
		t.Fatalf("级差: %v", got)
	}
	// 服务商返佣后来调低到 15：一级、二级都被封到 15，一级没有级差可拿。
	got = sharesByAgent(planAgentShares(chainOf("ISV", 30, 20, 10), "ISV", 10000, 15))
	if got["G1"] != 0 || got["G2"] != 5 || got["G3"] != 10 || len(got) != 2 {
		t.Fatalf("封顶: %v", got)
	}
	// 中间一级停用：它那份留在服务商，上下级的金额不变。
	chain := chainOf("ISV", 30, 20, 10)
	chain[1].State = stateDisabled
	got = sharesByAgent(planAgentShares(chain, "ISV", 10000, 38))
	if got["G1"] != 10 || got["G3"] != 10 || len(got) != 2 {
		t.Fatalf("停用: %v", got)
	}
	// 没有返佣、没有代理商、链上混进别的服务商的代理商：都不分。
	for name, shares := range map[string][]AgentShare{
		"无返佣":   planAgentShares(chainOf("ISV", 30, 20), "ISV", 10000, 0),
		"无代理商":  planAgentShares(nil, "ISV", 10000, 38),
		"别家服务商": planAgentShares(append(chainOf("ISV", 30), Agent{AgentNo: "X", IsvNo: "OTHER", Level: 1, RateBps: 30, State: stateActive}), "ISV", 10000, 38),
	} {
		if len(shares) != 0 {
			t.Errorf("%s: 不应分润 %v", name, shares)
		}
	}
	// 任意金额：各级都非负，合计恰好等于一级那条线的金额，也就不会超过返佣（逐级四舍五入不会多分出一分钱）。
	for _, rates := range [][]int{{30, 20, 10}, {38, 38, 38, 1}, {7, 3}, {25, 25, 24, 13, 1}} {
		for amount := int64(1); amount <= 3000; amount += 7 {
			var sum int64
			for _, s := range planAgentShares(chainOf("ISV", rates...), "ISV", amount, 38) {
				if s.Amount <= 0 {
					t.Fatalf("rates %v amount %d: 份额 %+v 不为正", rates, amount, s)
				}
				sum += s.Amount
			}
			if want := feeAmount(amount, rates[0]); sum != want || sum > feeAmount(amount, 38) {
				t.Fatalf("rates %v amount %d: 合计 %d, 应为 %d", rates, amount, sum, want)
			}
		}
	}
}

func TestAgentClawbackDue(t *testing.T) {
	// 100 分的分润，订单分三笔各退三分之一：33 + 34 + 33，全退后恰好冲回 100。
	const share, amount = 100, 100000
	var clawed, refunded int64
	for i, part := range []int64{33333, 33333, 33334} {
		refunded += part
		due := agentClawbackDue(share, refunded, amount, clawed)
		if want := []int64{33, 34, 33}[i]; due != want {
			t.Fatalf("第 %d 笔冲回 %d, 应为 %d", i+1, due, want)
		}
		clawed += due
	}
	if clawed != share {
		t.Fatalf("全额退款后冲回 %d, 应为 %d", clawed, share)
	}
	// 1 分的分润：逐笔四舍五入会一分都冲不回来，累计算在退过半时冲回。
	if agentClawbackDue(1, 333, 1000, 0) != 0 || agentClawbackDue(1, 666, 1000, 0) != 1 || agentClawbackDue(1, 1000, 1000, 1) != 0 {
		t.Fatal("1 分分润的累计冲回不对")
	}
	// 已经冲回得比应冲回的多（并发下的舍入）：不再冲，也不会冲成负数。
	if agentClawbackDue(100, 50000, 100000, 60) != 0 || agentClawbackDue(100, 100000, 100000, 100) != 0 {
		t.Fatal("不应重复冲回")
	}
}

func createTestAgent(t *testing.T, user string, body map[string]any) string {
	t.Helper()
	res := call(t, testRouter(), user, http.MethodPost, "/api/pay/admin/agents", body)
	mustCode(t, res, 200)
	return field(t, res, "agent", "agentNo").(string)
}

func agentRow(t *testing.T, user, agentNo string) map[string]any {
	t.Helper()
	res := call(t, testRouter(), user, http.MethodGet, "/api/pay/admin/agents", nil)
	mustCode(t, res, 200)
	for _, it := range res.Body["items"].([]any) {
		if m := it.(map[string]any); m["agentNo"] == agentNo {
			return m
		}
	}
	return nil
}

func TestAgents_HierarchyRules(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-AGH", "M-AGH", "stub_channel")
	seedISVAndMerchant(t, "ISV-AGH2", "M-AGH2", "stub_channel")
	if _, err := db.Exec(ctx, `UPDATE pay_isv SET rebate_rate_bps=30 WHERE isv_no IN ('ISV-AGH','ISV-AGH2')`); err != nil {
		t.Fatal(err)
	}
	r := testRouter()
	post := func(body map[string]any) resp {
		return call(t, r, "op", http.MethodPost, "/api/pay/admin/agents", body)
	}

	mustCode(t, post(map[string]any{"agentName": "没有归属"}), 400)
	mustCode(t, post(map[string]any{"agentName": "x", "isvNo": "ISV-NOPE"}), 404)
	mustCode(t, post(map[string]any{"agentName": "x", "isvNo": "ISV-AGH", "rateBps": -1}), 400)
	// 一级不能超过服务商返佣。
	mustCode(t, post(map[string]any{"agentName": "x", "isvNo": "ISV-AGH", "rateBps": 31}), 400)
	l1 := createTestAgent(t, "op", map[string]any{"agentNo": "G-AGH-1", "agentName": "一级", "isvNo": "ISV-AGH", "rateBps": 30})
	mustCode(t, post(map[string]any{"agentNo": "G-AGH-1", "agentName": "重号", "isvNo": "ISV-AGH"}), 409)
	// 下级不能超过上级；服务商跟上级走，给了别家服务商就拒绝。
	mustCode(t, post(map[string]any{"agentName": "x", "parentAgentNo": l1, "rateBps": 31}), 400)
	mustCode(t, post(map[string]any{"agentName": "x", "parentAgentNo": l1, "isvNo": "ISV-AGH2", "rateBps": 1}), 400)
	mustCode(t, post(map[string]any{"agentName": "x", "parentAgentNo": "G-NOPE"}), 404)
	l2 := createTestAgent(t, "op", map[string]any{"agentNo": "G-AGH-2", "agentName": "二级", "parentAgentNo": l1, "rateBps": 20})
	l3 := createTestAgent(t, "op", map[string]any{"agentNo": "G-AGH-3", "agentName": "三级", "parentAgentNo": l2, "rateBps": 10})
	if a, _ := loadAgent(ctx, db, l3); a.Level != 3 || a.IsvNo != "ISV-AGH" || a.ParentAgentNo != l2 {
		t.Fatalf("三级: %+v", a)
	}
	chain, err := loadAgentChain(ctx, db, l3)
	if err != nil || len(chain) != 3 || chain[0].AgentNo != l3 || chain[2].AgentNo != l1 {
		t.Fatalf("chain: %+v %v", chain, err)
	}

	// 改费率：夹在上级与下级之间。
	put := func(no string, body map[string]any) resp {
		return call(t, r, "op", http.MethodPut, "/api/pay/admin/agents/"+no, body)
	}
	mustCode(t, put(l2, map[string]any{"rateBps": 31}), 400)
	mustCode(t, put(l2, map[string]any{"rateBps": 9}), 400)
	mustCode(t, put(l1, map[string]any{"rateBps": 19}), 400)
	mustCode(t, put(l2, map[string]any{"rateBps": 15, "remark": "调过"}), 200)
	mustCode(t, put("G-NOPE", map[string]any{"rateBps": 1}), 404)
	mustCode(t, put(l2, map[string]any{"state": "gone"}), 400)
	mustCode(t, put(l2, map[string]any{"agentName": ""}), 400)
	if a, _ := loadAgent(ctx, db, l2); a.RateBps != 15 || a.Remark != "调过" || a.AgentName != "二级" {
		t.Fatalf("改后: %+v", a)
	}

	// 层级上限。
	parent := l3
	for level := 4; level <= maxAgentLevel; level++ {
		parent = createTestAgent(t, "op", map[string]any{"agentName": fmt.Sprintf("%d 级", level), "parentAgentNo": parent, "rateBps": 1})
	}
	deep := post(map[string]any{"agentName": "太深", "parentAgentNo": parent, "rateBps": 1})
	if msg, _ := deep.Body["error"].(string); deep.Code != 400 || !strings.Contains(msg, "最多") {
		t.Fatalf("层级上限: %d %v", deep.Code, deep.Body)
	}

	// 商户归属：只能归到自己服务商下、启用中的代理商。
	assign := func(mchNo, agentNo string) resp {
		return call(t, r, "op", http.MethodPut, "/api/pay/admin/merchants/"+mchNo+"/agent", map[string]any{"agentNo": agentNo})
	}
	mustCode(t, assign("M-NOPE", l3), 404)
	mustCode(t, assign("M-AGH2", l3), 400)
	mustCode(t, assign("M-AGH", "G-NOPE"), 400)
	mustCode(t, assign("M-AGH", l3), 200)
	if m, _ := loadMerchant(ctx, "M-AGH"); m.AgentNo != l3 {
		t.Fatalf("归属: %+v", m)
	}
	if row := agentRow(t, "op", l3); row == nil || row["merchantCount"].(float64) != 1 {
		t.Fatalf("列表里的商户数: %v", row)
	}
	// 停用的代理商不能再接商户、不能再挂下级；已有归属不动。
	mustCode(t, put(l2, map[string]any{"state": stateDisabled}), 200)
	mustCode(t, assign("M-AGH", l2), 409)
	mustCode(t, post(map[string]any{"agentName": "x", "parentAgentNo": l2, "rateBps": 1}), 409)
	mustCode(t, assign("M-AGH", ""), 200)
	if m, _ := loadMerchant(ctx, "M-AGH"); m.AgentNo != "" {
		t.Fatalf("解除归属: %+v", m)
	}
	// 没挂服务商的商户没有返佣可分。
	if _, err := db.Exec(ctx, `INSERT INTO pay_merchant (mch_no, mch_name, mch_type) VALUES ('M-AGH-SOLO','solo',1) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	mustCode(t, assign("M-AGH-SOLO", l3), 400)
	// 没有 operator 角色的用户一律进不来。
	mustCode(t, call(t, r, "nobody", http.MethodGet, "/api/pay/admin/agents", nil), 403)
	mustCode(t, call(t, r, "nobody", http.MethodPost, "/api/pay/admin/agents", map[string]any{"agentName": "x", "isvNo": "ISV-AGH"}), 403)
}

func agentLedger(t *testing.T, agentNo string) (earned, clawed, paid int64) {
	t.Helper()
	err := db.QueryRow(context.Background(), `SELECT
		COALESCE(SUM(amount)  FILTER (WHERE kind='agent_share'),0),
		COALESCE(SUM(-amount) FILTER (WHERE kind='agent_share_refund'),0),
		COALESCE(SUM(-amount) FILTER (WHERE kind='agent_payout'),0)
		FROM pay_ledger WHERE agent_no=$1`, agentNo).Scan(&earned, &clawed, &paid)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// 下单 → 支付成功 → 分三笔退完 → 结算，全程走开放 API 与真实的终态路径。
// 用桩渠道而不是沙箱：沙箱不动真钱，不产生分润（见 TestSandbox_noMoneyNoLedger）。
func TestAgents_CommissionLifecycle(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	defer stub.resetLifecycle()
	app, secret := seedStubMerchant(t, "ISV-AGC", "M-AGC", "A-AGC")
	mustCode(t, call(t, testRouter(), "op", http.MethodPut, "/api/pay/admin/passages",
		map[string]any{"appId": app.AppID, "ifCode": "stub_channel", "wayCode": "STUB_PAY", "rateBps": 60}), 200)
	if _, err := db.Exec(ctx, `UPDATE pay_isv SET rebate_rate_bps=38 WHERE isv_no='ISV-AGC'`); err != nil {
		t.Fatal(err)
	}
	l1 := createTestAgent(t, "op", map[string]any{"agentNo": "G-AGC-1", "agentName": "一级", "isvNo": "ISV-AGC", "rateBps": 30})
	l2 := createTestAgent(t, "op", map[string]any{"agentNo": "G-AGC-2", "agentName": "二级", "parentAgentNo": l1, "rateBps": 20})
	l3 := createTestAgent(t, "op", map[string]any{"agentNo": "G-AGC-3", "agentName": "三级", "parentAgentNo": l2, "rateBps": 10})
	admin, partner := testRouter(), partnerRouter()
	mustCode(t, call(t, admin, "op", http.MethodPut, "/api/pay/admin/merchants/M-AGC/agent", map[string]any{"agentNo": l3}), 200)

	created := callSigned(t, partner, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders", map[string]any{
		"mchOrderNo": "AGC-1", "wayCode": "STUB_PAY", "amount": 100000, "subject": "分润"})
	mustCode(t, created, 200)
	// 商户拿到的订单里不带服务商与代理商之间的分润。
	orderJSON := created.Body["order"].(map[string]any)
	for _, key := range []string{"agentNo", "agentAmount", "agentShares"} {
		if _, leaked := orderJSON[key]; leaked {
			t.Fatalf("开放 API 泄露了 %s: %v", key, orderJSON)
		}
	}
	payOrderID := orderJSON["payOrderId"].(string)
	o, err := findOrder(ctx, payOrderID)
	if err != nil {
		t.Fatal(err)
	}
	// 10 万分的单：返佣 380，一级线 300 → 三级各得 100。
	if got := sharesByAgent(o.AgentShares); o.AgentNo != l3 || o.AgentAmount != 300 || o.RebateAmount != 380 ||
		got[l1] != 100 || got[l2] != 100 || got[l3] != 100 {
		t.Fatalf("快照: agent=%s amount=%d shares=%+v", o.AgentNo, o.AgentAmount, o.AgentShares)
	}
	// 没付款之前不记账。
	if earned, _, _ := agentLedger(t, l3); earned != 0 {
		t.Fatalf("未支付就记了分润: %d", earned)
	}
	// 下单之后再改费率、改归属：这笔单按快照走。
	mustCode(t, call(t, admin, "op", http.MethodPut, "/api/pay/admin/agents/"+l3, map[string]any{"rateBps": 1}), 200)
	mustCode(t, call(t, admin, "op", http.MethodPut, "/api/pay/admin/merchants/M-AGC/agent", map[string]any{"agentNo": ""}), 200)

	// 渠道查单确认已支付。
	setStub(func(s *stubChannel) {
		s.queryResult = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "T-AGC-1", Amount: 100000}
	})
	if paid, err := reconcileOrder(ctx, o); err != nil || paid.State != orderSuccess {
		t.Fatalf("支付确认: %s %v", paid.State, err)
	}
	onOrderTerminal(ctx, payOrderID, orderSuccess) // 重放不重复记
	for _, no := range []string{l1, l2, l3} {
		if earned, clawed, paid := agentLedger(t, no); earned != 100 || clawed != 0 || paid != 0 {
			t.Fatalf("%s 支付后: earned=%d clawed=%d paid=%d", no, earned, clawed, paid)
		}
	}
	// 分润不进商户的账，也不混进服务商的返佣。
	if all, _ := ledgerSum(ctx, "M-AGC", "", []string{}); all != -600+380 {
		t.Fatalf("商户台账合计 %d, 应只有手续费 -600 与返佣 380", all)
	}
	res := call(t, admin, "op", http.MethodGet, "/api/pay/admin/ledger?isvNo=ISV-AGC", nil)
	mustCode(t, res, 200)
	if res.Body["total"].(float64) != -600+380 {
		t.Fatalf("服务商台账里混进了代理商子账: %v", res.Body["total"])
	}
	res = call(t, admin, "op", http.MethodGet, "/api/pay/admin/ledger?agentNo="+l3, nil)
	mustCode(t, res, 200)
	if items := res.Body["items"].([]any); len(items) != 1 || res.Body["total"].(float64) != 100 ||
		items[0].(map[string]any)["refId"] != payOrderID {
		t.Fatalf("代理商子账: %v", res.Body)
	}
	// 管理端订单列表带出分润明细。
	res = call(t, admin, "op", http.MethodGet, "/api/pay/admin/orders", nil)
	mustCode(t, res, 200)
	var listed map[string]any
	for _, it := range res.Body["items"].([]any) {
		if m := it.(map[string]any); m["payOrderId"] == payOrderID {
			listed = m
		}
	}
	if listed == nil || listed["agentAmount"].(float64) != 300 || len(listed["agentShares"].([]any)) != 3 {
		t.Fatalf("管理端订单: %v", listed)
	}

	// 结算登记：不能超过待结余额；同一单号重复登记不重复记账，金额不同则拒绝。
	payout := func(agentNo string, body map[string]any) resp {
		return call(t, admin, "op", http.MethodPost, "/api/pay/admin/agents/"+agentNo+"/payouts", body)
	}
	mustCode(t, payout(l3, map[string]any{"amount": 0}), 400)
	mustCode(t, payout(l3, map[string]any{"amount": 101, "payoutNo": "PO-AGC-1"}), 409)
	mustCode(t, payout("G-NOPE", map[string]any{"amount": 1}), 404)
	paid := payout(l3, map[string]any{"amount": 100, "payoutNo": "PO-AGC-1", "memo": "十一月"})
	mustCode(t, paid, 200)
	if paid.Body["balance"].(float64) != 0 || paid.Body["idempotent"] != false {
		t.Fatalf("结算: %v", paid.Body)
	}
	again := payout(l3, map[string]any{"amount": 100, "payoutNo": "PO-AGC-1"})
	mustCode(t, again, 200)
	if again.Body["idempotent"] != true {
		t.Fatalf("重复登记: %v", again.Body)
	}
	mustCode(t, payout(l3, map[string]any{"amount": 50, "payoutNo": "PO-AGC-1"}), 409)
	// 同一个结算单号可以分别用在不同代理商身上。
	mustCode(t, payout(l2, map[string]any{"amount": 40, "payoutNo": "PO-AGC-1"}), 200)
	if _, _, paidOut := agentLedger(t, l3); paidOut != 100 {
		t.Fatalf("l3 已结算 %d, 应为 100", paidOut)
	}

	// 分三笔退完：每一级恰好冲回 100（逐笔四舍五入只会冲回 99）。
	for i, amount := range []int{33333, 33333, 33334} {
		rf := callSigned(t, partner, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
			map[string]any{"mchOrderNo": "AGC-1", "mchRefundNo": fmt.Sprintf("AGC-RF-%d", i+1), "refundAmount": amount})
		mustCode(t, rf, 200)
		if field(t, rf, "refund", "state") != refundSuccess {
			t.Fatalf("refund %d: %v", i+1, rf.Body)
		}
		if _, clawed, _ := agentLedger(t, l1); clawed != []int64{33, 67, 100}[i] {
			t.Fatalf("第 %d 笔退款后一级累计冲回 %d", i+1, clawed)
		}
	}
	// 退款终态重放不重复冲回。
	last, err := findRefundByMch(ctx, "M-AGC", "AGC-RF-3")
	if err != nil {
		t.Fatal(err)
	}
	if err := refundTerminalEffectsReplay(ctx, last); err != nil {
		t.Fatal(err)
	}
	for _, no := range []string{l1, l2, l3} {
		if earned, clawed, _ := agentLedger(t, no); earned != 100 || clawed != 100 {
			t.Fatalf("%s 全额退款后: earned=%d clawed=%d", no, earned, clawed)
		}
	}
	// 已经结算出去的分润被退款冲回：待结余额为负，留给下一期抵扣；负余额不能再结算。
	row := agentRow(t, "op", l3)
	if row == nil || row["earned"].(float64) != 100 || row["refunded"].(float64) != 100 ||
		row["paid"].(float64) != 100 || row["balance"].(float64) != -100 {
		t.Fatalf("l3 汇总: %v", row)
	}
	if row := agentRow(t, "op", l2); row["balance"].(float64) != -40 {
		t.Fatalf("l2 汇总: %v", row)
	}
	mustCode(t, payout(l3, map[string]any{"amount": 1}), 409)
}

// refundTerminalEffectsReplay 只重放退款成功的台账部分（rollup 与通知在别处已有覆盖）。
func refundTerminalEffectsReplay(ctx context.Context, r Refund) error {
	o, err := findOrder(ctx, r.PayOrderID)
	if err != nil {
		return err
	}
	entries, err := agentShareRefundEntries(ctx, db, o, r)
	if err != nil {
		return err
	}
	_, err = postLedger(ctx, nil, entries)
	return err
}

func TestAgents_NoAgentNoShares(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	// 商户没有归属代理商；归属的代理商被删坏（链断）：都照常下单，只是不分润。
	seedISVAndMerchant(t, "ISV-AGN", "M-AGN", "stub_channel")
	if _, err := db.Exec(ctx, `UPDATE pay_isv SET rebate_rate_bps=38 WHERE isv_no='ISV-AGN'`); err != nil {
		t.Fatal(err)
	}
	mch, _ := loadMerchant(ctx, "M-AGN")
	if no, total, shares, err := orderAgentPlan(ctx, mch, 10000, 38); err != nil || no != "" || total != 0 || shares != nil {
		t.Fatalf("无代理商: %s %d %v %v", no, total, shares, err)
	}
	mch.AgentNo = "G-GHOST"
	if no, total, shares, err := orderAgentPlan(ctx, mch, 10000, 38); err != nil || no != "" || total != 0 || shares != nil {
		t.Fatalf("链断: %s %d %v %v", no, total, shares, err)
	}
	// 没有分润的订单，支付与退款都不产生代理商子账。
	seedApp(t, "M-AGN", "A-AGN")
	o := seedSuccessOrder(t, "M-AGN", "ISV-AGN", "A-AGN", "P-AGN-1", 10000, 60, 38)
	onOrderTerminal(ctx, o.PayOrderID, orderSuccess)
	var n int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM pay_ledger WHERE ref_id=$1 AND agent_no<>''`, o.PayOrderID).Scan(&n)
	if n != 0 {
		t.Fatalf("无分润的单记了 %d 条代理商子账", n)
	}
}

func TestAgents_TenantIsolation(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	testUsers["t303"] = &Claims{TokenType: "access", UserId: 13, Username: "t303", Role: "user", TenantId: 303}
	testGrants[13] = map[string]bool{roleOperator: true, roleReviewer: true}
	defer func() { delete(testUsers, "t303"); delete(testGrants, 13) }()

	// 租户 404 的服务商、代理商、商户；租户 303 的商户挂在平台（租户 0）服务商下。
	seedISVAndMerchant(t, "ISV-AG404", "M-AG404", "stub_channel")
	seedISVAndMerchant(t, "ISV-AGPLAT", "M-AG303", "stub_channel")
	for _, sql := range []string{
		`UPDATE pay_isv SET tenant_id=404, rebate_rate_bps=30 WHERE isv_no='ISV-AG404'`,
		`UPDATE pay_merchant SET tenant_id=404 WHERE mch_no='M-AG404'`,
		`UPDATE pay_isv SET rebate_rate_bps=30 WHERE isv_no='ISV-AGPLAT'`,
		`UPDATE pay_merchant SET tenant_id=303 WHERE mch_no='M-AG303'`,
	} {
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	// 平台管理员代建：代理商归属服务商所在的租户，而不是操作人的租户。
	victim := createTestAgent(t, "admin", map[string]any{"agentNo": "G-AG404", "agentName": "victim", "isvNo": "ISV-AG404", "rateBps": 20})
	if a, _ := loadAgent(ctx, db, victim); a.TenantID != 404 {
		t.Fatalf("代理商租户 = %d, 应跟服务商走 404", a.TenantID)
	}
	plat := createTestAgent(t, "admin", map[string]any{"agentNo": "G-AGPLAT", "agentName": "平台代理", "isvNo": "ISV-AGPLAT", "rateBps": 20})
	mustCode(t, call(t, testRouter(), "admin", http.MethodPut, "/api/pay/admin/merchants/M-AG303/agent", map[string]any{"agentNo": plat}), 200)
	if _, err := postLedger(ctx, nil, []LedgerEntry{{IsvNo: "ISV-AG404", Kind: ledgerAgentShare, RefType: "order", RefID: "P-AG404-1",
		RefSub: victim, AgentNo: victim, Amount: 500, TenantID: 404}}); err != nil {
		t.Fatal(err)
	}

	r := testRouter()
	deny := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/pay/admin/agents", map[string]any{"agentName": "x", "isvNo": "ISV-AG404", "rateBps": 1}},
		{http.MethodPost, "/api/pay/admin/agents", map[string]any{"agentName": "x", "parentAgentNo": victim, "rateBps": 1}},
		{http.MethodPut, "/api/pay/admin/agents/" + victim, map[string]any{"rateBps": 1, "agentName": "hijacked"}},
		{http.MethodPost, "/api/pay/admin/agents/" + victim + "/payouts", map[string]any{"amount": 500}},
		{http.MethodPut, "/api/pay/admin/merchants/M-AG404/agent", map[string]any{"agentNo": ""}},
		// 自己租户的商户挂在平台服务商下：返佣让给谁由平台决定，租户自己不能改。
		{http.MethodPost, "/api/pay/admin/agents", map[string]any{"agentName": "x", "isvNo": "ISV-AGPLAT", "rateBps": 30}},
		{http.MethodPut, "/api/pay/admin/merchants/M-AG303/agent", map[string]any{"agentNo": ""}},
	}
	for _, tc := range deny {
		res := call(t, r, "t303", tc.method, tc.path, tc.body)
		if msg, _ := res.Body["error"].(string); res.Code != http.StatusForbidden || !strings.Contains(msg, "租户") {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, res.Code, res.Body)
		}
	}
	if a, _ := loadAgent(ctx, db, victim); a.AgentName != "victim" || a.RateBps != 20 {
		t.Fatalf("别的租户的代理商被改了: %+v", a)
	}
	if m, _ := loadMerchant(ctx, "M-AG303"); m.AgentNo != plat {
		t.Fatalf("归属被租户自己改掉了: %+v", m)
	}
	if _, _, paid := agentLedger(t, victim); paid != 0 {
		t.Fatalf("别的租户登记了结算: %d", paid)
	}
	// 列表与子账都看不到别的租户的代理商。
	if row := agentRow(t, "t303", victim); row != nil {
		t.Fatalf("列表泄露了别的租户的代理商: %v", row)
	}
	if row := agentRow(t, "t303", plat); row != nil {
		t.Fatalf("列表泄露了平台的代理商: %v", row)
	}
	res := call(t, r, "t303", http.MethodGet, "/api/pay/admin/ledger?agentNo="+victim, nil)
	mustCode(t, res, 200)
	if len(res.Body["items"].([]any)) != 0 {
		t.Fatalf("子账泄露: %v", res.Body)
	}
	if row := agentRow(t, "admin", victim); row == nil || row["earned"].(float64) != 500 {
		t.Fatalf("平台管理员应能看到: %v", row)
	}

	// 租户 303 的订单分给了平台的代理商：租户自己的管理端看得到订单，看不到分润明细。
	o := Order{PayOrderID: "P-AG303-1", MchNo: "M-AG303", IsvNo: "ISV-AGPLAT", AppID: "A-AG303", MchType: 2, MchOrderNo: "M-P-AG303-1",
		IfCode: "stub_channel", WayCode: "STUB_PAY", Amount: 10000, Currency: "CNY", Subject: "t", TenantID: 303,
		RebateRateBps: 30, RebateAmount: 30, AgentNo: plat, AgentAmount: 20,
		AgentShares: []AgentShare{{AgentNo: plat, Level: 1, RateBps: 20, Amount: 20, TenantID: 0}}}
	seedApp(t, "M-AG303", "A-AG303")
	if _, dup, err := insertOrder(ctx, o); err != nil || dup {
		t.Fatalf("seed order: %v dup=%v", err, dup)
	}
	find := func(user string) map[string]any {
		res := call(t, r, user, http.MethodGet, "/api/pay/admin/orders", nil)
		mustCode(t, res, 200)
		for _, it := range res.Body["items"].([]any) {
			if m := it.(map[string]any); m["payOrderId"] == "P-AG303-1" {
				return m
			}
		}
		return nil
	}
	own := find("t303")
	if own == nil {
		t.Fatal("租户看不到自己的订单")
	}
	if _, leaked := own["agentShares"]; leaked || own["agentAmount"] != nil || own["agentNo"] != nil {
		t.Fatalf("租户看到了平台与代理商之间的分润: %v", own)
	}
	if full := find("admin"); full == nil || full["agentAmount"].(float64) != 20 {
		t.Fatalf("平台管理员应看到分润: %v", full)
	}
}
