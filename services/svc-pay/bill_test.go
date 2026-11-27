package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func billRouter() *gin.Engine {
	r := gin.New()
	v1 := r.Group(mount + "/v1")
	v1.GET("/bills/transactions", handleBillTransactions)
	v1.GET("/bills/summary", handleBillSummary)
	return r
}

func num(t *testing.T, v any) int64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("期望数字，得到 %T %v", v, v)
	}
	return int64(f)
}

// seedBillOrder 落一笔成功的支付单，并把成功时间改到指定的北京时间。
func seedBillOrder(t *testing.T, mchNo, isvNo, appID, id string, amount int64, at string) Order {
	t.Helper()
	o := seedSuccessOrder(t, mchNo, isvNo, appID, id, amount, 60, 20)
	when, err := time.ParseInLocation("2006-01-02 15:04:05", at, billZone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE pay_order SET success_at=$2 WHERE pay_order_id=$1`, id, when); err != nil {
		t.Fatal(err)
	}
	// seedSuccessOrder 绕过了 finishOrder，这里补记支付成功的手续费台账，好跟汇总对口径。
	if _, err := postLedger(context.Background(), nil, orderSettledEntries(o)); err != nil {
		t.Fatal(err)
	}
	return o
}

// seedBillRefund 对订单退一笔并推进到终态 to，成功的把成功时间改到指定的北京时间。
func seedBillRefund(t *testing.T, o Order, id string, amount int64, to, at string) {
	t.Helper()
	ctx := context.Background()
	if _, err := createRefund(ctx, Refund{RefundOrderID: id, PayOrderID: o.PayOrderID, MchNo: o.MchNo, IsvNo: o.IsvNo,
		AppID: o.AppID, MchRefundNo: "MR-" + id, IfCode: o.IfCode, WayCode: o.WayCode, RefundAmount: amount, Reason: "t"}); err != nil {
		t.Fatalf("seed refund: %v", err)
	}
	if ok, err := finishRefund(ctx, id, refundCreated, to, "TR-"+id, "", ""); err != nil || !ok {
		t.Fatalf("finish refund: %v ok=%v", err, ok)
	}
	if to != refundSuccess {
		return
	}
	when, err := time.ParseInLocation("2006-01-02 15:04:05", at, billZone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE pay_refund SET success_at=$2 WHERE refund_order_id=$1`, id, when); err != nil {
		t.Fatal(err)
	}
}

// 查账：交易流水 = 成功的支付 + 成功的退款，按北京时间归日，只看自己商户；汇总与台账手续费口径一致。
func TestBill_TransactionsAndSummary(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, secret := seedStubMerchant(t, "ISV-BILL", "M-BILL", "APP-BILL")
	other, otherSecret := seedStubMerchant(t, "ISV-BILL2", "M-BILL2", "APP-BILL2")

	// 北京时间 3 月 1 日 00:30 = UTC 2 月 28 日 16:30：必须算在 3 月 1 日。
	o1 := seedBillOrder(t, "M-BILL", "ISV-BILL", app.AppID, "P-BILL-1", 10000, "2026-03-01 00:30:00")
	o2 := seedBillOrder(t, "M-BILL", "ISV-BILL", app.AppID, "P-BILL-2", 5000, "2026-03-01 23:59:59")
	o3 := seedBillOrder(t, "M-BILL", "ISV-BILL", app.AppID, "P-BILL-3", 3333, "2026-03-02 10:00:00")
	seedBillOrder(t, "M-BILL2", "ISV-BILL2", other.AppID, "P-BILL-X", 7000, "2026-03-01 12:00:00")
	// 未支付的订单、失败的退款都不是流水。
	seedPayingOrder(t, "M-BILL", "ISV-BILL", app.AppID, "P-BILL-P", 900, "", time.Hour)
	seedBillRefund(t, o1, "R-BILL-1", 2500, refundSuccess, "2026-03-02 12:00:00")
	seedBillRefund(t, o2, "R-BILL-2", 1000, refundFailed, "")

	r := billRouter()
	base := mount + "/v1/bills"
	fee3 := o3.FeeAmount

	// —— 流水明细
	list := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/transactions?startDate=2026-03-01&endDate=2026-03-02", nil)
	mustCode(t, list, 200)
	if list.Body["mchNo"] != "M-BILL" || num(t, list.Body["total"]) != 4 || list.Body["hasMore"] != false {
		t.Fatalf("list head: %v", list.Body)
	}
	items := list.Body["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("want 4 items, got %d: %v", len(items), items)
	}
	wantOrder := []string{"P-BILL-1", "P-BILL-2", "P-BILL-3", "P-BILL-1"}
	for i, it := range items {
		if got := it.(map[string]any)["payOrderId"]; got != wantOrder[i] {
			t.Fatalf("item %d: want %s got %v", i, wantOrder[i], got)
		}
	}
	first := items[0].(map[string]any)
	if first["type"] != "pay" || first["billDate"] != "2026-03-01" || first["tradeTime"] != "2026-03-01T00:30:00+08:00" ||
		first["appId"] != app.AppID || first["mchOrderNo"] != o1.MchOrderNo || first["channelOrderNo"] != "T-P-BILL-1" ||
		num(t, first["amount"]) != 10000 || num(t, first["feeAmount"]) != 60 || num(t, first["netAmount"]) != 9940 {
		t.Fatalf("pay item: %v", first)
	}
	refund := items[3].(map[string]any)
	if refund["type"] != "refund" || refund["billDate"] != "2026-03-02" || refund["mchOrderNo"] != o1.MchOrderNo ||
		refund["mchRefundNo"] != "MR-R-BILL-1" || refund["refundOrderId"] != "R-BILL-1" || refund["channelOrderNo"] != "TR-R-BILL-1" ||
		num(t, refund["amount"]) != 2500 || num(t, refund["feeAmount"]) != 15 || num(t, refund["netAmount"]) != -2485 {
		t.Fatalf("refund item: %v", refund)
	}
	for _, it := range items {
		row := it.(map[string]any)
		for _, hidden := range []string{"agentNo", "agentAmount", "rebateAmount", "isvNo", "tenantId"} {
			if _, leaked := row[hidden]; leaked {
				t.Fatalf("流水不应带出 %s: %v", hidden, row)
			}
		}
	}

	// —— 分页
	p1 := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/transactions?startDate=2026-03-01&endDate=2026-03-02&pageSize=3", nil)
	mustCode(t, p1, 200)
	if len(p1.Body["items"].([]any)) != 3 || p1.Body["hasMore"] != true || num(t, p1.Body["total"]) != 4 {
		t.Fatalf("page 1: %v", p1.Body)
	}
	p2 := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/transactions?startDate=2026-03-01&endDate=2026-03-02&pageSize=3&page=2", nil)
	mustCode(t, p2, 200)
	if rest := p2.Body["items"].([]any); len(rest) != 1 || rest[0].(map[string]any)["type"] != "refund" || p2.Body["hasMore"] != false {
		t.Fatalf("page 2: %v", p2.Body)
	}
	big := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/transactions?startDate=2026-03-01&pageSize=9999", nil)
	mustCode(t, big, 200)
	if num(t, big.Body["pageSize"]) != billMaxPage {
		t.Fatalf("pageSize 应封顶 %d: %v", billMaxPage, big.Body["pageSize"])
	}

	// —— 类型过滤、单日（endDate 缺省 = startDate）
	onlyRefund := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/transactions?startDate=2026-03-01&endDate=2026-03-02&type=refund", nil)
	mustCode(t, onlyRefund, 200)
	if num(t, onlyRefund.Body["total"]) != 1 {
		t.Fatalf("type=refund: %v", onlyRefund.Body)
	}
	day1 := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/transactions?startDate=2026-03-01", nil)
	mustCode(t, day1, 200)
	if num(t, day1.Body["total"]) != 2 || day1.Body["endDate"] != "2026-03-01" {
		t.Fatalf("single day: %v", day1.Body)
	}
	empty := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/transactions?startDate=2025-01-01", nil)
	mustCode(t, empty, 200)
	if got, isList := empty.Body["items"].([]any); !isList || len(got) != 0 {
		t.Fatalf("没有流水时 items 应为空数组: %v", empty.Body)
	}

	// —— 商户隔离：另一个商户只看到自己的一笔
	mine := callSigned(t, r, other.AppID, otherSecret, http.MethodGet, base+"/transactions?startDate=2026-03-01&endDate=2026-03-02", nil)
	mustCode(t, mine, 200)
	if got := mine.Body["items"].([]any); len(got) != 1 || got[0].(map[string]any)["payOrderId"] != "P-BILL-X" {
		t.Fatalf("other merchant: %v", mine.Body)
	}

	// —— 按日汇总
	sum := callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/summary?startDate=2026-03-01&endDate=2026-03-03", nil)
	mustCode(t, sum, 200)
	days := sum.Body["days"].([]any)
	if len(days) != 3 {
		t.Fatalf("want 3 days (含无交易日), got %v", days)
	}
	type want struct {
		date                                           string
		payCount, payAmount, refundCount, refundAmount int64
		feeAmount, feeRefundAmount, netAmount          int64
	}
	wants := []want{
		{"2026-03-01", 2, 15000, 0, 0, 90, 0, 14910},
		{"2026-03-02", 1, 3333, 1, 2500, fee3, 15, 3333 - 2500 - fee3 + 15},
		{"2026-03-03", 0, 0, 0, 0, 0, 0, 0},
	}
	check := func(label string, row map[string]any, w want) {
		t.Helper()
		if num(t, row["payCount"]) != w.payCount || num(t, row["payAmount"]) != w.payAmount ||
			num(t, row["refundCount"]) != w.refundCount || num(t, row["refundAmount"]) != w.refundAmount ||
			num(t, row["feeAmount"]) != w.feeAmount || num(t, row["feeRefundAmount"]) != w.feeRefundAmount ||
			num(t, row["netAmount"]) != w.netAmount {
			t.Fatalf("%s: want %+v got %v", label, w, row)
		}
	}
	for i, w := range wants {
		row := days[i].(map[string]any)
		if row["date"] != w.date {
			t.Fatalf("day %d: want %s got %v", i, w.date, row["date"])
		}
		check(w.date, row, w)
	}
	total := sum.Body["total"].(map[string]any)
	check("total", total, want{"", 3, 18333, 1, 2500, 90 + fee3, 15, 18333 - 2500 - 90 - fee3 + 15})
	if _, has := total["date"]; has {
		t.Fatalf("total 不应带 date: %v", total)
	}

	// 汇总的手续费与台账同一口径：fee 记负、fee_refund 记正。
	ledger, err := ledgerSum(ctx, "M-BILL", "", []string{ledgerFee, ledgerFeeRefund})
	if err != nil {
		t.Fatal(err)
	}
	if ledger != -num(t, total["feeAmount"])+num(t, total["feeRefundAmount"]) {
		t.Fatalf("ledger %d != summary fee %v / feeRefund %v", ledger, total["feeAmount"], total["feeRefundAmount"])
	}
}

// 查账参数校验与签名：坏参数 400，不签名 401，改了查询串签名就不匹配。
func TestBill_RejectsBadInput(t *testing.T) {
	requireDB(t)
	app, secret := seedStubMerchant(t, "ISV-BILLV", "M-BILLV", "APP-BILLV")
	r := billRouter()
	base := mount + "/v1/bills"

	for _, q := range []string{
		"/transactions",
		"/transactions?startDate=20260301",
		"/transactions?startDate=2026-03-01&endDate=2026-3-2",
		"/transactions?startDate=2026-03-02&endDate=2026-03-01",
		"/transactions?startDate=2026-03-01&endDate=2026-04-01",
		"/transactions?startDate=2026-03-01&type=fee",
		"/transactions?startDate=2026-03-01&page=0",
		"/transactions?startDate=2026-03-01&pageSize=abc",
		"/summary",
		"/summary?startDate=2026-03-01&endDate=2026-04-01",
	} {
		if got := callSigned(t, r, app.AppID, secret, http.MethodGet, base+q, nil); got.Code != 400 {
			t.Fatalf("%s: want 400 got %d %v", q, got.Code, got.Body)
		}
	}
	// 正好 31 天可以查。
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodGet, base+"/summary?startDate=2026-03-01&endDate=2026-03-31", nil), 200)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/transactions?startDate=2026-03-01", nil))
	if w.Code != 401 {
		t.Fatalf("unsigned: want 401 got %d", w.Code)
	}

	// 用 3 月 1 日的签名去查 3 月 2 日：查询串在签名里，必须拒绝。
	signed := httptest.NewRequest(http.MethodGet, base+"/transactions?startDate=2026-03-01", nil)
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := signPayload(secret, app.AppID, stamp, "n-bill-tamper", http.MethodGet, signed.URL.RequestURI(), "")
	tampered := httptest.NewRequest(http.MethodGet, base+"/transactions?startDate=2026-03-02", nil)
	tampered.Header.Set("X-Pay-App-Id", app.AppID)
	tampered.Header.Set("X-Pay-Timestamp", stamp)
	tampered.Header.Set("X-Pay-Nonce", "n-bill-tamper")
	tampered.Header.Set("X-Pay-Signature", sig)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, tampered)
	if w.Code != 401 {
		t.Fatalf("tampered query: want 401 got %d %s", w.Code, w.Body.String())
	}
}
