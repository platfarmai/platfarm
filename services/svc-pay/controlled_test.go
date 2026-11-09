package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

var _ channel.SettleChannel = (*settleOnlyStub)(nil)

// openRouter 挂开放 API，签名由 callSigned 计算。
func openRouter() *gin.Engine {
	r := gin.New()
	v1 := r.Group(mount + "/v1")
	v1.POST("/orders/:mchOrderNo/settle", handleSettleOrder)
	v1.POST("/withdrawals", handleCreateWithdrawal)
	v1.GET("/withdrawals/:mchWithdrawNo", handleQueryWithdrawal)
	v1.GET("/balance", handleBalance)
	v1.POST("/divisions", handleCreateDivision)
	return r
}

func callSigned(t *testing.T, r *gin.Engine, appID, secret, method, path string, body any) resp {
	t.Helper()
	raw := ""
	if body != nil {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("n-%d", time.Now().UnixNano())
	req := httptest.NewRequest(method, path, strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pay-App-Id", appID)
	req.Header.Set("X-Pay-Timestamp", ts)
	req.Header.Set("X-Pay-Nonce", nonce)
	req.Header.Set("X-Pay-Signature", signPayload(secret, appID, ts, nonce, raw))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	out := resp{Code: w.Code, Body: map[string]any{}}
	_ = json.Unmarshal(w.Body.Bytes(), &out.Body)
	return out
}

func seedControlledOn(t *testing.T, ifCode, isvNo, mchNo, appID string) (App, string, Merchant) {
	t.Helper()
	seedISVAndMerchant(t, isvNo, mchNo, ifCode)
	seedMerchantParams(t, mchNo, ifCode, map[string]string{"sub_mchid": "1900009"})
	app, secret := seedApp(t, mchNo, appID)
	mch, err := loadMerchant(context.Background(), mchNo)
	if err != nil {
		t.Fatal(err)
	}
	return app, secret, mch
}

func seedControlled(t *testing.T, isvNo, mchNo, appID string) (App, string, Merchant) {
	return seedControlledOn(t, "stub_controlled", isvNo, mchNo, appID)
}

// seedControlledOrder 落一笔已成功的归集渠道支付单。
func seedControlledOrder(t *testing.T, ifCode, mchNo, isvNo, appID, id string, amount int64) Order {
	t.Helper()
	ctx := context.Background()
	o := seedSuccessOrder(t, mchNo, isvNo, appID, id, amount, 60, 20)
	if _, err := db.Exec(ctx, `UPDATE pay_order SET if_code=$2 WHERE pay_order_id=$1`, o.PayOrderID, ifCode); err != nil {
		t.Fatal(err)
	}
	o, _ = findOrder(ctx, o.PayOrderID)
	return o
}

func withdrawableBalance(t *testing.T, mchNo string) int64 {
	t.Helper()
	sum, err := ledgerSum(context.Background(), mchNo, "stub_controlled", withdrawableKinds)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func setCtrl(f func(c *controlledStub)) {
	ctrlStub.mu.Lock()
	defer ctrlStub.mu.Unlock()
	f(ctrlStub)
}

// 收付通形态：分账 → 确认结算（释放剩余并记可提现台账）→ 提现（发起即预占，失败/退票冲回）。
func TestControlled_EcommerceMoneyFlow(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, _, mch := seedControlled(t, "ISV-C", "M-CTL", "A-CTL")
	order := seedControlledOrder(t, "stub_controlled", "M-CTL", "ISV-C", "A-CTL", "P-CTL-1", 10000)

	// 分账在确认结算之前，且分账时不解冻（剩余资金统一由确认结算释放）。
	if _, err := db.Exec(ctx, `INSERT INTO pay_division_receiver
		(mch_no, if_code, receiver_type, account, name, relation_type, max_ratio_bps, default_ratio_bps, bind_state, state)
		VALUES ('M-CTL','stub_controlled','MERCHANT_ID','1900000110','','SUPPLIER',2000,0,'bound','active')`); err != nil {
		t.Fatal(err)
	}
	ctrlStub.stubChannel.mu.Lock()
	ctrlStub.stubChannel.shareResult = channel.DivisionResult{State: channel.StateSuccess, ChannelOrderNo: "CH-S",
		Receivers: []channel.DivisionReceiver{{Account: "1900000110", Amount: 1000, Result: "SUCCESS"}}}
	ctrlStub.stubChannel.mu.Unlock()
	d, dup, err := createDivision(ctx, app, mch, order, []divisionReq{{"1900000110", 1000}}, true, "test")
	if err != nil || dup || d.State != divisionSuccess || d.DivisionAmount != 1000 || d.UnfreezeUnsplit {
		t.Fatalf("division: %+v dup=%v err=%v", d, dup, err)
	}

	// 确认结算：释放 10000 - 1000 = 9000，记可提现台账。
	setCtrl(func(c *controlledStub) {
		c.settleRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ST-1"}
		c.settleCalls, c.settleNos = 0, nil
	})
	order, _ = findOrder(ctx, order.PayOrderID)
	s, dup, err := createSettle(ctx, mch, order, false)
	if err != nil || dup || s.State != settleSuccess || s.Amount != 9000 || s.SharedAmount != 1000 || s.CreditAmount != 9000 {
		t.Fatalf("settle: %+v dup=%v err=%v", s, dup, err)
	}
	ctrlStub.mu.Lock()
	settleNo := ctrlStub.settleNos[0]
	ctrlStub.mu.Unlock()
	if settleNo != s.SettleNo {
		t.Fatalf("channel idempotency key = %s, want the settle number %s", settleNo, s.SettleNo)
	}
	order, _ = findOrder(ctx, order.PayOrderID)
	if s2, dup, err := createSettle(ctx, mch, order, false); err != nil || !dup || s2.SettleNo != s.SettleNo {
		t.Fatalf("second settle: %+v dup=%v err=%v", s2, dup, err)
	}
	if _, _, err := createSettle(ctx, mch, order, true); err == nil {
		t.Fatal("ecommerce settle cannot hold for division")
	}
	// 确认结算（完结分账）之后剩余资金已解冻，不能再分账。
	lateApp, _, lateMch := seedControlled(t, "ISV-C", "M-CTL-B", "A-CTL-B")
	late := seedControlledOrder(t, "stub_controlled", "M-CTL-B", "ISV-C", "A-CTL-B", "P-CTL-1B", 3000)
	if _, _, err := createSettle(ctx, lateMch, late, false); err != nil {
		t.Fatal(err)
	}
	late, _ = findOrder(ctx, late.PayOrderID)
	if _, _, err := createDivision(ctx, lateApp, lateMch, late, nil, true, "test"); err == nil || !strings.Contains(err.Error(), "确认结算") {
		t.Fatalf("division after settle: %v", err)
	}
	if got := withdrawableBalance(t, "M-CTL"); got != 9000 {
		t.Fatalf("withdrawable ledger = %d, want 9000", got)
	}

	// 渠道可用只有 8000（如手续费从收入扣）：最多可提两者较小值，差额显示为漂移，不阻断。
	setCtrl(func(c *controlledStub) { c.available, c.withdrawCalls, c.withdrawNos = 8000, 0, nil })
	v, err := merchantBalance(ctx, mch, "stub_controlled")
	if err != nil || v.Ledger != 9000 || v.Available != 8000 || v.Withdrawable != 8000 || v.Drift != -1000 {
		t.Fatalf("balance view: %+v %v", v, err)
	}
	if _, _, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-OVER", 8001, "", "test"); !errors.Is(err, errOverBalance) {
		t.Fatalf("over balance: %v", err)
	}
	ctrlStub.mu.Lock()
	calls := ctrlStub.withdrawCalls
	ctrlStub.mu.Unlock()
	if calls != 0 {
		t.Fatal("over-balance withdrawal must not reach the channel")
	}

	// 并发两笔 5000、余额 9000：锁内预占，只能成一笔。
	setCtrl(func(c *controlledStub) {
		c.available = 9000
		c.withdrawRes = channel.Result{State: channel.StateWaiting, ChannelOrderNo: "CW"}
	})
	var wg sync.WaitGroup
	results := make([]Withdrawal, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = createWithdrawal(ctx, mch, "stub_controlled", fmt.Sprintf("WD-C%d", i), 5000, "", "test")
		}(i)
	}
	wg.Wait()
	var w Withdrawal
	accepted := 0
	for i, e := range errs {
		switch {
		case e == nil:
			accepted++
			w = results[i]
		case !errors.Is(e, errOverBalance):
			t.Fatalf("concurrent withdrawal %d: %v", i, e)
		}
	}
	if accepted != 1 || w.State != withdrawAccepted {
		t.Fatalf("concurrent withdrawals accepted = %d (%+v)", accepted, w)
	}
	if got := withdrawableBalance(t, "M-CTL"); got != 4000 {
		t.Fatalf("ledger after hold = %d, want 4000", got)
	}

	// 渠道终态成功：借方早已记过，台账不再变；重放也不变。
	setCtrl(func(c *controlledStub) {
		c.queryRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "CW"}
	})
	if w, err = syncWithdrawal(ctx, w); err != nil || w.State != withdrawSuccess {
		t.Fatalf("sync: %+v %v", w, err)
	}
	_, _ = applyWithdrawResult(ctx, w, channel.Result{State: channel.StateSuccess})
	if got := withdrawableBalance(t, "M-CTL"); got != 4000 {
		t.Fatalf("ledger after success = %d", got)
	}
	// 成功后银行退票（REFUND）：worker 复查发现，冲回借方。
	setCtrl(func(c *controlledStub) {
		c.queryRes = channel.Result{State: channel.StateFail, ErrCode: "REFUND", ErrMsg: "银行退票"}
	})
	if _, err := db.Exec(ctx, `UPDATE pay_withdrawal SET updated_at=now()-interval '7 hours' WHERE withdraw_no=$1`, w.WithdrawNo); err != nil {
		t.Fatal(err)
	}
	if err := sweepControlled(ctx); err != nil {
		t.Fatal(err)
	}
	if w, _ = findWithdraw(ctx, w.WithdrawNo); w.State != withdrawFailed {
		t.Fatalf("bank return not picked up: %s", w.State)
	}
	if got := withdrawableBalance(t, "M-CTL"); got != 9000 {
		t.Fatalf("ledger after bank return = %d, want 9000", got)
	}

	// 渠道直接拒绝：冲回，余额不变。
	setCtrl(func(c *controlledStub) {
		c.withdrawRes = channel.Result{State: channel.StateFail, ErrCode: "NOT_ENOUGH"}
	})
	if f, _, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-FAIL", 1000, "", "test"); err != nil || f.State != withdrawFailed {
		t.Fatalf("failed withdrawal: %+v %v", f, err)
	}
	if got := withdrawableBalance(t, "M-CTL"); got != 9000 {
		t.Fatalf("ledger after failed withdrawal = %d", got)
	}

	// 提交结果未知：额度继续占着；worker 用原提现单号重提。
	setCtrl(func(c *controlledStub) {
		c.withdrawRes, c.withdrawNos = channel.Result{State: channel.StateUnknown, ErrCode: "SYSTEM_ERROR"}, nil
	})
	u, _, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-UNK", 2000, "", "test")
	if err != nil || u.State != withdrawPending {
		t.Fatalf("unknown withdrawal: %+v %v", u, err)
	}
	if got := withdrawableBalance(t, "M-CTL"); got != 7000 {
		t.Fatalf("ledger with unknown withdrawal = %d, want 7000", got)
	}
	setCtrl(func(c *controlledStub) {
		c.withdrawRes = channel.Result{State: channel.StateWaiting, ChannelOrderNo: "CW-U"}
	})
	ageRow(t, "pay_withdrawal", "withdraw_no", u.WithdrawNo)
	if err := sweepControlled(ctx); err != nil {
		t.Fatal(err)
	}
	ctrlStub.mu.Lock()
	nos := append([]string(nil), ctrlStub.withdrawNos...)
	ctrlStub.mu.Unlock()
	if len(nos) != 2 || nos[0] != u.WithdrawNo || nos[1] != u.WithdrawNo {
		t.Fatalf("resubmission must reuse the withdrawal number: %v", nos)
	}
	if u, _ = findWithdraw(ctx, u.WithdrawNo); u.State != withdrawAccepted {
		t.Fatalf("after resubmit: %s", u.State)
	}

	// 结算后的退款从可用余额出：扣可提现台账。
	r, err := createRefund(ctx, Refund{RefundOrderID: "R-CTL-1", PayOrderID: order.PayOrderID, MchNo: order.MchNo, IsvNo: order.IsvNo,
		AppID: order.AppID, MchRefundNo: "MR-CTL-1", IfCode: order.IfCode, WayCode: order.WayCode, RefundAmount: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyRefundResult(ctx, r, channel.Result{State: channel.StateSuccess}); err != nil {
		t.Fatal(err)
	}
	if got := withdrawableBalance(t, "M-CTL"); got != 5000 {
		t.Fatalf("ledger after refund = %d, want 5000", got)
	}
}

func TestControlled_DirectModeRejectedAndSettleRecovery(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-D2", "M-DIR", "stub_channel")
	app, secret := seedApp(t, "M-DIR", "A-DIR")
	mch, _ := loadMerchant(ctx, "M-DIR")
	order := seedSuccessOrder(t, "M-DIR", "ISV-D2", "A-DIR", "P-DIR-1", 5000, 60, 20)

	var re reqError
	if _, _, err := createWithdrawal(ctx, mch, "stub_channel", "WD-D", 100, "", "test"); !errors.As(err, &re) {
		t.Fatalf("direct withdrawal: %v", err)
	}
	if _, _, err := createSettle(ctx, mch, order, false); !errors.As(err, &re) {
		t.Fatalf("direct settle: %v", err)
	}
	r := openRouter()
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/withdrawals",
		map[string]any{"ifCode": "stub_channel", "mchWithdrawNo": "WD-D2", "amount": 100}), 400)
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/"+order.MchOrderNo+"/settle", map[string]any{}), 400)
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/balance?ifCode=stub_channel", nil), 400)

	// 结算确定失败释放订单，可重试成功。
	_, _, mch2 := seedControlled(t, "ISV-C2", "M-CTL2", "A-CTL2")
	o := seedControlledOrder(t, "stub_controlled", "M-CTL2", "ISV-C2", "A-CTL2", "P-CTL-2", 4000)
	setCtrl(func(c *controlledStub) {
		c.settleRes = channel.Result{State: channel.StateFail, ErrCode: "RULE_LIMIT", ErrMsg: "未确认"}
	})
	s, _, err := createSettle(ctx, mch2, o, false)
	if err != nil || s.State != settleFailed {
		t.Fatalf("failed settle: %+v %v", s, err)
	}
	if o, _ = findOrder(ctx, o.PayOrderID); o.SettleState != orderSettleNone {
		t.Fatalf("failed settle must release order, got %s", o.SettleState)
	}
	setCtrl(func(c *controlledStub) {
		c.settleRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ST-2"}
	})
	if s, dup, err := createSettle(ctx, mch2, o, false); err != nil || dup || s.State != settleSuccess || s.Amount != 4000 {
		t.Fatalf("retry settle: %+v dup=%v err=%v", s, dup, err)
	}

	// 结果未知：结算单留在 pending，期间不能退款；worker 用原结算单号重提。
	o3 := seedControlledOrder(t, "stub_controlled", "M-CTL2", "ISV-C2", "A-CTL2", "P-CTL-3", 3000)
	setCtrl(func(c *controlledStub) {
		c.settleRes, c.settleNos = channel.Result{State: channel.StateUnknown, ErrCode: "SYSTEM_ERROR"}, nil
	})
	pending, _, err := createSettle(ctx, mch2, o3, false)
	if err != nil || pending.State != settlePending {
		t.Fatalf("unknown settle: %+v %v", pending, err)
	}
	if _, err := createRefund(ctx, Refund{RefundOrderID: "R-CTL-3", PayOrderID: o3.PayOrderID, MchNo: o3.MchNo, AppID: o3.AppID,
		MchRefundNo: "MR-CTL-3", IfCode: o3.IfCode, WayCode: o3.WayCode, RefundAmount: 100}); err == nil || !strings.Contains(err.Error(), "确认结算") {
		t.Fatalf("refund during pending settle: %v", err)
	}
	setCtrl(func(c *controlledStub) {
		c.settleRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ST-3"}
	})
	ageRow(t, "pay_settle", "settle_no", pending.SettleNo)
	if err := sweepControlled(ctx); err != nil {
		t.Fatal(err)
	}
	ctrlStub.mu.Lock()
	nos := append([]string(nil), ctrlStub.settleNos...)
	ctrlStub.mu.Unlock()
	if len(nos) != 2 || nos[0] != pending.SettleNo || nos[1] != pending.SettleNo {
		t.Fatalf("settle resubmission must reuse the settle number: %v", nos)
	}
	if got, _ := findSettle(ctx, pending.SettleNo); got.State != settleSuccess {
		t.Fatalf("after resubmit: %s", got.State)
	}

	// 退款在途时不能确认结算（结算金额要按已完结的退款算）。
	o4 := seedControlledOrder(t, "stub_controlled", "M-CTL2", "ISV-C2", "A-CTL2", "P-CTL-4", 3000)
	if _, err := createRefund(ctx, Refund{RefundOrderID: "R-CTL-4", PayOrderID: o4.PayOrderID, MchNo: o4.MchNo, AppID: o4.AppID,
		MchRefundNo: "MR-CTL-4", IfCode: o4.IfCode, WayCode: o4.WayCode, RefundAmount: 500}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createSettle(ctx, mch2, o4, false); err == nil || !strings.Contains(err.Error(), "退款") {
		t.Fatalf("settle during refund: %v", err)
	}
}

// 直付通形态：结算前不能分账；结算金额 = 交易金额 − 已退款；需要分账时结算冻结；没有提现。
func TestControlled_ZFTSettleBeforeDivisionAndNoWithdrawal(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, secret, mch := seedControlledOn(t, "stub_zft", "ISV-Z", "M-ZFT", "A-ZFT")
	if _, err := db.Exec(ctx, `INSERT INTO pay_division_receiver
		(mch_no, if_code, receiver_type, account, relation_type, max_ratio_bps, default_ratio_bps, bind_state, state)
		VALUES ('M-ZFT','stub_zft','loginName','a@b.c','PARTNER',3000,1000,'bound','active')`); err != nil {
		t.Fatal(err)
	}
	zftStub.stubChannel.mu.Lock()
	zftStub.stubChannel.shareResult = channel.DivisionResult{State: channel.StateSuccess,
		Receivers: []channel.DivisionReceiver{{Account: "a@b.c", Amount: 1000, Result: "SUCCESS"}}}
	zftStub.stubChannel.mu.Unlock()
	zftStub.mu.Lock()
	zftStub.settleRes, zftStub.holds, zftStub.amounts = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ZS"}, nil, nil
	zftStub.mu.Unlock()

	// 结算前不支持分账；不冻结的结算之后也不能分账（资金已可被挪用）。
	o0 := seedControlledOrder(t, "stub_zft", "M-ZFT", "ISV-Z", "A-ZFT", "P-ZFT-0", 10000)
	if _, _, err := createDivision(ctx, app, mch, o0, nil, true, "test"); err == nil || !strings.Contains(err.Error(), "确认结算") {
		t.Fatalf("division before settle: %v", err)
	}
	s0, _, err := createSettle(ctx, mch, o0, false)
	if err != nil || s0.State != settleSuccess || s0.Amount != 10000 || s0.CreditAmount != 0 {
		t.Fatalf("zft settle: %+v %v", s0, err)
	}
	o0, _ = findOrder(ctx, o0.PayOrderID)
	if _, _, err := createDivision(ctx, app, mch, o0, nil, true, "test"); err == nil || !strings.Contains(err.Error(), "holdForDivision") {
		t.Fatalf("division after a non-holding settle: %v", err)
	}
	if sum, _ := ledgerSum(ctx, "M-ZFT", "stub_zft", []string{ledgerSettle}); sum != 0 {
		t.Fatalf("zft settle must not create a withdrawable balance, got %d", sum)
	}

	// 结算前退款 1000：结算金额 = 交易金额 − 已退款（支付宝的硬性规则），不扣分账。
	o := seedControlledOrder(t, "stub_zft", "M-ZFT", "ISV-Z", "A-ZFT", "P-ZFT-1", 10000)
	r, err := createRefund(ctx, Refund{RefundOrderID: "R-ZFT-1", PayOrderID: o.PayOrderID, MchNo: o.MchNo, AppID: o.AppID,
		MchRefundNo: "MR-ZFT-1", IfCode: o.IfCode, WayCode: o.WayCode, RefundAmount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyRefundResult(ctx, r, channel.Result{State: channel.StateSuccess}); err != nil {
		t.Fatal(err)
	}
	o, _ = findOrder(ctx, o.PayOrderID)
	s, _, err := createSettle(ctx, mch, o, false)
	if err != nil || s.State != settleSuccess || s.Amount != 9000 || s.RefundedAmount != 1000 {
		t.Fatalf("zft settle after refund: %+v %v", s, err)
	}

	// 结算时冻结，之后可以分账。
	o2 := seedControlledOrder(t, "stub_zft", "M-ZFT", "ISV-Z", "A-ZFT", "P-ZFT-2", 10000)
	res := callSigned(t, openRouter(), app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/"+o2.MchOrderNo+"/settle",
		map[string]any{"holdForDivision": true})
	mustCode(t, res, 200)
	o2, _ = findOrder(ctx, o2.PayOrderID)
	d, _, err := createDivision(ctx, app, mch, o2, nil, true, "test")
	if err != nil || d.State != divisionSuccess {
		t.Fatalf("division after holding settle: %+v %v", d, err)
	}
	zftStub.mu.Lock()
	holds, amounts := append([]bool(nil), zftStub.holds...), append([]int64(nil), zftStub.amounts...)
	zftStub.mu.Unlock()
	if len(holds) != 3 || holds[0] || holds[1] || !holds[2] || amounts[0] != 10000 || amounts[1] != 9000 || amounts[2] != 10000 {
		t.Fatalf("settle calls holds=%v amounts=%v", holds, amounts)
	}

	// 没有平台侧余额：提现、余额都是 400。
	var re reqError
	if _, _, err := createWithdrawal(ctx, mch, "stub_zft", "WD-Z", 100, "", "test"); !errors.As(err, &re) {
		t.Fatalf("zft withdrawal: %v", err)
	}
	mustCode(t, callSigned(t, openRouter(), app.AppID, secret, http.MethodGet, "/api/pay/v1/balance?ifCode=stub_zft", nil), 400)
}

// 台账写不进去时，提现单与状态都不落：预占与落单同事务，冲回与失败同事务。
func TestControlled_WithdrawalLedgerAtomic(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	_, _, mch := seedControlled(t, "ISV-CA", "M-CTLA", "A-CTLA")
	if _, err := postLedger(ctx, nil, []LedgerEntry{{MchNo: "M-CTLA", IfCode: "stub_controlled", Kind: ledgerSettle,
		RefType: "settle", RefID: "S-CTLA", Amount: 5000}}); err != nil {
		t.Fatal(err)
	}
	setCtrl(func(c *controlledStub) {
		c.available = 5000
		c.withdrawRes = channel.Result{State: channel.StateWaiting, ChannelOrderNo: "CW-A"}
		c.withdrawCalls = 0
	})
	reject := func(kind string) func() {
		if _, err := db.Exec(ctx, `CREATE OR REPLACE FUNCTION test_reject_kind() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF NEW.kind = '`+kind+`' AND NEW.mch_no = 'M-CTLA' THEN RAISE EXCEPTION 'injected ledger failure'; END IF; RETURN NEW; END $$;
			CREATE TRIGGER test_reject_kind BEFORE INSERT ON pay_ledger FOR EACH ROW EXECUTE FUNCTION test_reject_kind()`); err != nil {
			t.Fatal(err)
		}
		return func() {
			_, _ = db.Exec(ctx, `DROP TRIGGER test_reject_kind ON pay_ledger; DROP FUNCTION test_reject_kind()`)
		}
	}

	undo := reject(ledgerWithdrawal)
	if _, _, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-A1", 1000, "", "test"); err == nil {
		t.Fatal("hold failure must surface")
	}
	undo()
	if _, err := findWithdrawByMch(ctx, "M-CTLA", "WD-A1"); !notFound(err) {
		t.Fatalf("withdrawal row must not exist without its hold: %v", err)
	}
	ctrlStub.mu.Lock()
	calls := ctrlStub.withdrawCalls
	ctrlStub.mu.Unlock()
	if calls != 0 {
		t.Fatal("channel must not be called when the hold could not be recorded")
	}

	w, _, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-A2", 1000, "", "test")
	if err != nil || w.State != withdrawAccepted {
		t.Fatalf("withdrawal: %+v %v", w, err)
	}
	undo = reject(ledgerWithdrawalReversal)
	if _, err := applyWithdrawResult(ctx, w, channel.Result{State: channel.StateFail, ErrCode: "X"}); err == nil {
		t.Fatal("reversal failure must surface")
	}
	undo()
	if got, _ := findWithdraw(ctx, w.WithdrawNo); got.State != withdrawAccepted {
		t.Fatalf("state moved without its reversal: %s", got.State)
	}
	if _, err := applyWithdrawResult(ctx, w, channel.Result{State: channel.StateFail, ErrCode: "X"}); err != nil {
		t.Fatal(err)
	}
	if sum, _ := ledgerSum(ctx, "M-CTLA", "stub_controlled", withdrawableKinds); sum != 5000 {
		t.Fatalf("ledger after failed withdrawal = %d, want 5000", sum)
	}
}

func TestControlled_OpenAPIAndAdmin(t *testing.T) {
	requireDB(t)
	app, secret, mch := seedControlled(t, "ISV-C3", "M-CTL3", "A-CTL3")
	o := seedControlledOrder(t, "stub_controlled", "M-CTL3", "ISV-C3", "A-CTL3", "P-CTL-3A", 2000)
	setCtrl(func(c *controlledStub) {
		c.settleRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ST-3"}
		c.available = 2000
		c.withdrawRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "CW-3"}
	})

	r := openRouter()
	res := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/"+o.MchOrderNo+"/settle", map[string]any{})
	mustCode(t, res, 200)
	if res.Body["idempotent"] != false {
		t.Fatalf("settle resp = %v", res.Body)
	}
	res = callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/balance?ifCode=stub_controlled", nil)
	mustCode(t, res, 200)
	if res.Body["withdrawable"] != float64(2000) || res.Body["ledger"] != float64(2000) || res.Body["drift"] != float64(0) {
		t.Fatalf("balance = %v", res.Body)
	}
	res = callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/withdrawals",
		map[string]any{"ifCode": "stub_controlled", "mchWithdrawNo": "WD-API", "amount": 500})
	mustCode(t, res, 200)
	res = callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/withdrawals",
		map[string]any{"ifCode": "stub_controlled", "mchWithdrawNo": "WD-API-2", "amount": 1600})
	mustCode(t, res, 409)
	res = callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/withdrawals/WD-API", nil)
	mustCode(t, res, 200)
	w := res.Body["withdrawal"].(map[string]any)
	if w["state"] != withdrawSuccess || w["mchNo"] != mch.MchNo {
		t.Fatalf("query = %v", w)
	}

	admin := testRouter()
	mustCode(t, call(t, admin, "op", http.MethodGet, "/api/pay/admin/balance?mchNo=M-CTL3&ifCode=stub_controlled", nil), 200)
	mustCode(t, call(t, admin, "nobody", http.MethodGet, "/api/pay/admin/withdrawals?mchNo=M-CTL3", nil), 403)
	res = call(t, admin, "op", http.MethodGet, "/api/pay/admin/withdrawals?mchNo=M-CTL3", nil)
	mustCode(t, res, 200)
	if items := res.Body["items"].([]any); len(items) != 1 {
		t.Fatalf("withdrawals = %v", items)
	}
}
