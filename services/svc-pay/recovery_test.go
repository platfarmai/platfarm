package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"platfarm/svc-pay/channel"
)

// ageRow 把一行的 updated_at 推到过去，让恢复扫描立刻拾取。
func ageRow(t *testing.T, table, keyCol, key string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `UPDATE `+table+` SET updated_at=now()-interval '5 minutes' WHERE `+keyCol+`=$1`, key); err != nil {
		t.Fatal(err)
	}
}

func TestRecovery_RefundResubmittedWithSameNumberThenQueried(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	app, _ := seedStubMerchant(t, "ISV-RR", "M-RR", "A-RR")
	o := seedSuccessOrder(t, "M-RR", "ISV-RR", app.AppID, "P-RR-1", 1000, 60, 20)
	r, err := createRefund(ctx, Refund{RefundOrderID: "R-RR-1", PayOrderID: o.PayOrderID, MchNo: o.MchNo, IsvNo: o.IsvNo,
		AppID: o.AppID, MchRefundNo: "MRR-1", IfCode: o.IfCode, WayCode: o.WayCode, RefundAmount: 600})
	if err != nil {
		t.Fatal(err)
	}
	// 首次提交结果未知：留在 created。
	if r, err = applyRefundResult(ctx, r, channel.Result{State: channel.StateUnknown, ErrCode: "SYSTEM_ERROR"}); err != nil || r.State != refundCreated {
		t.Fatalf("unknown: %+v %v", r, err)
	}
	// 恢复：用同一个退款单号重提，渠道受理（处理中）。
	setStub(func(s *stubChannel) {
		s.refundRes = &channel.Result{State: channel.StateWaiting, ChannelOrderNo: "RF-RR"}
	})
	ageRow(t, "pay_refund", "refund_order_id", r.RefundOrderID)
	if err := sweepRefunds(ctx); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	calls := append([]string(nil), stub.refundCalls...)
	stub.mu.Unlock()
	if len(calls) != 1 || calls[0] != "R-RR-1" {
		t.Fatalf("resubmit must reuse the refund number: %v", calls)
	}
	if r, _ = findRefund(ctx, "R-RR-1"); r.State != refundProcessing || r.ChannelOrderNo != "RF-RR" {
		t.Fatalf("after resubmit: %+v", r)
	}
	// 处理中的退款靠查询推进到成功（回调丢了也能完成）。
	setStub(func(s *stubChannel) {
		s.queryRefund = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "RF-RR", Amount: 600}
	})
	ageRow(t, "pay_refund", "refund_order_id", r.RefundOrderID)
	if err := sweepRefunds(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ = findRefund(ctx, "R-RR-1"); r.State != refundSuccess {
		t.Fatalf("after query: %+v", r)
	}
	if got, _ := findOrder(ctx, o.PayOrderID); got.RefundState != refundPartial || got.RefundAmount != 600 {
		t.Fatalf("order rollup: %s %d", got.RefundState, got.RefundAmount)
	}
	// 渠道报的退款金额对不上：不推进。
	r2, _ := createRefund(ctx, Refund{RefundOrderID: "R-RR-2", PayOrderID: o.PayOrderID, MchNo: o.MchNo, IsvNo: o.IsvNo,
		AppID: o.AppID, MchRefundNo: "MRR-2", IfCode: o.IfCode, WayCode: o.WayCode, RefundAmount: 100})
	_, _ = casRefund(ctx, r2.RefundOrderID, refundCreated, refundProcessing, "", "", "")
	setStub(func(s *stubChannel) { s.queryRefund = channel.Result{State: channel.StateSuccess, Amount: 400} })
	ageRow(t, "pay_refund", "refund_order_id", r2.RefundOrderID)
	_ = sweepRefunds(ctx)
	if r2, _ = findRefund(ctx, "R-RR-2"); r2.State != refundProcessing {
		t.Fatalf("amount mismatch must not finish the refund: %s", r2.State)
	}
}

func TestRecovery_PendingDivisionResubmittedAndLedgerAtomic(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	app, _ := seedStubMerchant(t, "ISV-RD", "M-RD", "A-RD")
	mch, _ := loadMerchant(ctx, "M-RD")
	if _, err := db.Exec(ctx, `INSERT INTO pay_division_receiver
		(mch_no, if_code, receiver_type, account, relation_type, max_ratio_bps, default_ratio_bps, bind_state, state)
		VALUES ('M-RD','stub_channel','MERCHANT_ID','1900000777','SUPPLIER',2000,1000,'bound','active')`); err != nil {
		t.Fatal(err)
	}
	order := seedSuccessOrder(t, "M-RD", "ISV-RD", app.AppID, "P-RD-1", 10000, 60, 20)

	// 提交结果未知：分账单 pending，订单占住。
	setStub(func(s *stubChannel) {
		s.shareResult = channel.DivisionResult{State: channel.StateUnknown, ErrCode: "SYSTEM_ERROR"}
	})
	d, _, err := createDivision(ctx, app, mch, order, nil, true, "test")
	if err != nil || d.State != divisionPending {
		t.Fatalf("unknown share: %+v %v", d, err)
	}
	// 台账写入失败时整笔回滚：分账单不变成功，订单不变 done。
	if _, err := db.Exec(ctx, `CREATE OR REPLACE FUNCTION test_reject_ledger() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.ref_id = 'BLOCKED' OR NEW.ref_type = 'division' THEN RAISE EXCEPTION 'injected ledger failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER test_reject_ledger BEFORE INSERT ON pay_ledger FOR EACH ROW EXECUTE FUNCTION test_reject_ledger()`); err != nil {
		t.Fatal(err)
	}
	success := channel.DivisionResult{State: channel.StateSuccess, ChannelOrderNo: "CH-RD",
		Receivers: []channel.DivisionReceiver{{Account: "1900000777", Amount: 1000, Result: "SUCCESS"}}}
	if _, err := applyDivisionResult(ctx, d, success); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("ledger failure must surface: %v", err)
	}
	if got, _ := findDivision(ctx, d.DivisionNo); got.State != divisionPending {
		t.Fatalf("division advanced without its ledger: %s", got.State)
	}
	if got, _ := findOrder(ctx, order.PayOrderID); got.DivisionState != orderDivisionPending {
		t.Fatalf("order advanced without the ledger: %s", got.DivisionState)
	}
	if _, err := db.Exec(ctx, `DROP TRIGGER test_reject_ledger ON pay_ledger; DROP FUNCTION test_reject_ledger()`); err != nil {
		t.Fatal(err)
	}

	// 恢复：同一个分账单号重提，渠道这次给出成功。
	setStub(func(s *stubChannel) { s.shareResult = success })
	ageRow(t, "pay_division", "division_no", d.DivisionNo)
	if err := sweepDivisions(ctx); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	nos := append([]string(nil), stub.shareNos...)
	stub.mu.Unlock()
	if len(nos) != 2 || nos[0] != d.DivisionNo || nos[1] != d.DivisionNo {
		t.Fatalf("resubmission must reuse the division number: %v", nos)
	}
	if got, _ := findDivision(ctx, d.DivisionNo); got.State != divisionSuccess {
		t.Fatalf("after resubmit: %s", got.State)
	}
	if sum, _ := ledgerSum(ctx, "M-RD", "stub_channel", []string{ledgerSharing}); sum != -1000 {
		t.Fatalf("sharing ledger = %d", sum)
	}

	// 退款在途（额度已占、尚未成功）的订单不能分账。
	o2 := seedSuccessOrder(t, "M-RD", "ISV-RD", app.AppID, "P-RD-2", 5000, 60, 20)
	if _, err := createRefund(ctx, Refund{RefundOrderID: "R-RD-2", PayOrderID: o2.PayOrderID, MchNo: o2.MchNo, IsvNo: o2.IsvNo,
		AppID: o2.AppID, MchRefundNo: "MRD-2", IfCode: o2.IfCode, WayCode: o2.WayCode, RefundAmount: 100}); err != nil {
		t.Fatal(err)
	}
	o2, _ = findOrder(ctx, o2.PayOrderID)
	if _, _, err := createDivision(ctx, app, mch, o2, nil, true, "test"); err == nil || !strings.Contains(err.Error(), "退款") {
		t.Fatalf("division during refund: %v", err)
	}
}

func TestLease_OneHolderAtATime(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	if !claimLease(ctx, "test-lease", "A", time.Minute) {
		t.Fatal("first claim")
	}
	if claimLease(ctx, "test-lease", "B", time.Minute) {
		t.Fatal("second holder must wait for expiry")
	}
	if !claimLease(ctx, "test-lease", "A", time.Minute) {
		t.Fatal("holder renews")
	}
	if _, err := db.Exec(ctx, `UPDATE pay_worker_lease SET until=now()-interval '1 second' WHERE name='test-lease'`); err != nil {
		t.Fatal(err)
	}
	if !claimLease(ctx, "test-lease", "B", time.Minute) {
		t.Fatal("expired lease is taken over")
	}
}
