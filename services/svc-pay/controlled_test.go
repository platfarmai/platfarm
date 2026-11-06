package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

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

func seedControlled(t *testing.T, isvNo, mchNo, appID string) (App, string, Merchant) {
	t.Helper()
	seedISVAndMerchant(t, isvNo, mchNo, "stub_controlled")
	seedMerchantParams(t, mchNo, "stub_controlled", map[string]string{"sub_mchid": "1900009"})
	app, secret := seedApp(t, mchNo, appID)
	mch, err := loadMerchant(context.Background(), mchNo)
	if err != nil {
		t.Fatal(err)
	}
	return app, secret, mch
}

func TestControlled_SettleShareWithdrawBalancesToZero(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, _, mch := seedControlled(t, "ISV-C", "M-CTL", "A-CTL")

	order := seedSuccessOrder(t, "M-CTL", "ISV-C", "A-CTL", "P-CTL-1", 10000, 60, 20)
	if _, err := db.Exec(ctx, `UPDATE pay_order SET if_code='stub_controlled' WHERE pay_order_id=$1`, order.PayOrderID); err != nil {
		t.Fatal(err)
	}
	order, _ = findOrder(ctx, order.PayOrderID)

	// 分账 1000：sharing 记借方，不进可用余额。
	if _, err := db.Exec(ctx, `INSERT INTO pay_division_receiver
		(mch_no, if_code, receiver_type, account, name, relation_type, max_ratio_bps, default_ratio_bps, bind_state, state)
		VALUES ('M-CTL','stub_controlled','MERCHANT_ID','1900000110','','SUPPLIER',2000,0,'bound','active')`); err != nil {
		t.Fatal(err)
	}
	stubShare := channel.DivisionResult{State: channel.StateSuccess, ChannelOrderNo: "CH-S",
		Receivers: []channel.DivisionReceiver{{Account: "1900000110", Amount: 1000, Result: "SUCCESS"}}}
	ctrlStub.stubChannel.mu.Lock()
	ctrlStub.stubChannel.shareResult = stubShare
	ctrlStub.stubChannel.mu.Unlock()
	d, dup, err := createDivision(ctx, app, mch, order, []divisionReq{{"1900000110", 1000}}, false, "test")
	if err != nil || dup || d.State != divisionSuccess || d.DivisionAmount != 1000 {
		t.Fatalf("division: %+v dup=%v err=%v", d, dup, err)
	}

	// 确认结算：金额 = 10000 - 1000 = 9000，记 settle 贷方。
	ctrlStub.mu.Lock()
	ctrlStub.settleRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ST-1"}
	ctrlStub.settleCalls = 0
	ctrlStub.mu.Unlock()
	s, dup, err := createSettle(ctx, mch, order)
	if err != nil || dup || s.State != settleSuccess || s.Amount != 9000 || s.SharedAmount != 1000 {
		t.Fatalf("settle: %+v dup=%v err=%v", s, dup, err)
	}
	order, _ = findOrder(ctx, order.PayOrderID)
	if order.SettleState != orderSettleDone {
		t.Fatalf("settle_state = %s", order.SettleState)
	}
	// 再确认 = 原单，不再调渠道。
	s2, dup, err := createSettle(ctx, mch, order)
	if err != nil || !dup || s2.SettleNo != s.SettleNo {
		t.Fatalf("second settle: %+v dup=%v err=%v", s2, dup, err)
	}
	ctrlStub.mu.Lock()
	if ctrlStub.settleCalls != 1 {
		t.Fatalf("SettleConfirm called %d times", ctrlStub.settleCalls)
	}
	ctrlStub.mu.Unlock()

	sum, _ := ledgerSum(ctx, "M-CTL", "stub_controlled", []string{ledgerSettle, ledgerWithdrawal, ledgerWithdrawalReversal})
	if sum != 9000 {
		t.Fatalf("ledger balance = %d, want 9000", sum)
	}

	// 渠道报的余额与台账一致才能提现。先不一致：拒绝且不调渠道。
	ctrlStub.mu.Lock()
	ctrlStub.available = 8000
	ctrlStub.withdrawCalls = 0
	ctrlStub.mu.Unlock()
	if _, _, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-1", 1000, "", "test"); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("mismatch should reject: %v", err)
	}
	ctrlStub.mu.Lock()
	if ctrlStub.withdrawCalls != 0 {
		t.Fatal("mismatch must not call the channel")
	}
	// 超额。
	ctrlStub.available = 9000
	ctrlStub.mu.Unlock()
	if _, _, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-2", 9001, "", "test"); err == nil || !strings.Contains(err.Error(), "超过可用余额") {
		t.Fatalf("over balance: %v", err)
	}

	// 提现 9000：渠道受理 → accepted；查询成功 → 余额归零。
	ctrlStub.mu.Lock()
	ctrlStub.withdrawRes = channel.Result{State: channel.StateWaiting, ChannelOrderNo: "CW-1"}
	ctrlStub.mu.Unlock()
	w, dup, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-3", 9000, "结算", "test")
	if err != nil || dup || w.State != withdrawAccepted || w.ChannelOrderNo != "CW-1" {
		t.Fatalf("withdraw: %+v dup=%v err=%v", w, dup, err)
	}
	// 幂等：同一商户单号返回原单。
	w2, dup, err := createWithdrawal(ctx, mch, "stub_controlled", "WD-3", 9000, "结算", "test")
	if err != nil || !dup || w2.WithdrawNo != w.WithdrawNo {
		t.Fatalf("idempotent: %+v dup=%v err=%v", w2, dup, err)
	}
	ctrlStub.mu.Lock()
	calls := ctrlStub.withdrawCalls
	ctrlStub.queryRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "CW-1"}
	ctrlStub.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Withdraw called %d times", calls)
	}
	w, err = syncWithdrawal(ctx, w)
	if err != nil || w.State != withdrawSuccess {
		t.Fatalf("sync: %+v %v", w, err)
	}
	sum, _ = ledgerSum(ctx, "M-CTL", "stub_controlled", []string{ledgerSettle, ledgerWithdrawal, ledgerWithdrawalReversal})
	if sum != 0 {
		t.Fatalf("balance after withdrawal = %d, want 0", sum)
	}
	// 重放不重复记账。
	_, _ = applyWithdrawResult(ctx, w, channel.Result{State: channel.StateSuccess, ChannelOrderNo: "CW-1"})
	sum, _ = ledgerSum(ctx, "M-CTL", "stub_controlled", []string{ledgerSettle, ledgerWithdrawal, ledgerWithdrawalReversal})
	if sum != 0 {
		t.Fatalf("replay changed balance: %d", sum)
	}

	// 渠道把已成功的提现退回：冲回贷方，余额回到 9000。
	w, err = applyWithdrawResult(ctx, w, channel.Result{State: channel.StateFail, ErrCode: "REFUND", ErrMsg: "银行退回"})
	if err != nil || w.State != withdrawFailed {
		t.Fatalf("reversal: %+v %v", w, err)
	}
	sum, _ = ledgerSum(ctx, "M-CTL", "stub_controlled", []string{ledgerSettle, ledgerWithdrawal, ledgerWithdrawalReversal})
	if sum != 9000 {
		t.Fatalf("balance after reversal = %d, want 9000", sum)
	}
}

func TestControlled_DirectModeRejectedAndSettleRetry(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-D2", "M-DIR", "stub_channel")
	app, secret := seedApp(t, "M-DIR", "A-DIR")
	mch, _ := loadMerchant(ctx, "M-DIR")
	order := seedSuccessOrder(t, "M-DIR", "ISV-D2", "A-DIR", "P-DIR-1", 5000, 60, 20)

	if _, _, err := createWithdrawal(ctx, mch, "stub_channel", "WD-D", 100, "", "test"); err == nil || !strings.Contains(err.Error(), "直清") {
		t.Fatalf("direct withdrawal: %v", err)
	}
	if _, _, err := createSettle(ctx, mch, order); err == nil || !strings.Contains(err.Error(), "直清") {
		t.Fatalf("direct settle: %v", err)
	}

	r := openRouter()
	res := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/withdrawals",
		map[string]any{"ifCode": "stub_channel", "mchWithdrawNo": "WD-D2", "amount": 100})
	mustCode(t, res, 400)
	res = callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/"+order.MchOrderNo+"/settle", map[string]any{})
	mustCode(t, res, 400)
	res = callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/balance?ifCode=stub_channel", nil)
	mustCode(t, res, 400)

	// 归集：结算失败释放订单，可重试成功。
	_, _, mch2 := seedControlled(t, "ISV-C2", "M-CTL2", "A-CTL2")
	o := seedSuccessOrder(t, "M-CTL2", "ISV-C2", "A-CTL2", "P-CTL-2", 4000, 60, 20)
	_, _ = db.Exec(ctx, `UPDATE pay_order SET if_code='stub_controlled' WHERE pay_order_id=$1`, o.PayOrderID)
	o, _ = findOrder(ctx, o.PayOrderID)
	ctrlStub.mu.Lock()
	ctrlStub.settleRes = channel.Result{State: channel.StateFail, ErrCode: "RULE_LIMIT", ErrMsg: "未确认"}
	ctrlStub.mu.Unlock()
	s, _, err := createSettle(ctx, mch2, o)
	if err != nil || s.State != settleFailed {
		t.Fatalf("failed settle: %+v %v", s, err)
	}
	o, _ = findOrder(ctx, o.PayOrderID)
	if o.SettleState != orderSettleNone {
		t.Fatalf("failed settle must release order, got %s", o.SettleState)
	}
	ctrlStub.mu.Lock()
	ctrlStub.settleRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ST-2"}
	ctrlStub.mu.Unlock()
	s, dup, err := createSettle(ctx, mch2, o)
	if err != nil || dup || s.State != settleSuccess || s.Amount != 4000 {
		t.Fatalf("retry settle: %+v dup=%v err=%v", s, dup, err)
	}
}

func TestControlled_OpenAPIAndAdmin(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, secret, mch := seedControlled(t, "ISV-C3", "M-CTL3", "A-CTL3")
	o := seedSuccessOrder(t, "M-CTL3", "ISV-C3", "A-CTL3", "P-CTL-3", 2000, 60, 20)
	_, _ = db.Exec(ctx, `UPDATE pay_order SET if_code='stub_controlled' WHERE pay_order_id=$1`, o.PayOrderID)

	ctrlStub.mu.Lock()
	ctrlStub.settleRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "ST-3"}
	ctrlStub.available = 2000
	ctrlStub.withdrawRes = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "CW-3"}
	ctrlStub.mu.Unlock()

	r := openRouter()
	res := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/"+o.MchOrderNo+"/settle", map[string]any{})
	mustCode(t, res, 200)
	if res.Body["idempotent"] != false {
		t.Fatalf("settle resp = %v", res.Body)
	}
	res = callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/balance?ifCode=stub_controlled", nil)
	mustCode(t, res, 200)
	if res.Body["invariant"] != true || int64(res.Body["ledger"].(float64)) != 2000 {
		t.Fatalf("balance = %v", res.Body)
	}
	res = callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/withdrawals",
		map[string]any{"ifCode": "stub_controlled", "mchWithdrawNo": "WD-API", "amount": 500})
	mustCode(t, res, 200)
	res = callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/withdrawals/WD-API", nil)
	mustCode(t, res, 200)
	w := res.Body["withdrawal"].(map[string]any)
	if w["state"] != withdrawSuccess || w["mchNo"] != mch.MchNo {
		t.Fatalf("query = %v", w)
	}

	admin := testRouter()
	res = call(t, admin, "op", http.MethodGet, "/api/pay/admin/balance?mchNo=M-CTL3&ifCode=stub_controlled", nil)
	mustCode(t, res, 200)
	res = call(t, admin, "nobody", http.MethodGet, "/api/pay/admin/withdrawals?mchNo=M-CTL3", nil)
	mustCode(t, res, 403)
	res = call(t, admin, "op", http.MethodGet, "/api/pay/admin/withdrawals?mchNo=M-CTL3", nil)
	mustCode(t, res, 200)
	if items := res.Body["items"].([]any); len(items) != 1 {
		t.Fatalf("withdrawals = %v", items)
	}
}
