// 微信交易账单：申请下载地址 → 签名下载 → 解析 CSV（字段以反引号开头，末尾是汇总两行）。
package wxpay

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"platfarm/svc-pay/channel"
)

var _ channel.BillChannel = (*partner)(nil)

// TradeBill GET /v3/bill/tradebill?bill_date=&bill_type=[&sub_mchid=]，再下载账单文件。
// sub 为空时下载服务商全量账单（覆盖所有特约商户，行里带特约商户号）。
func (p *partner) TradeBill(ctx context.Context, billDate, billType string, prov, sub map[string]string) ([]channel.BillRow, error) {
	q := url.Values{"bill_date": {billDate}, "bill_type": {orDefault(billType, "ALL")}}
	if sub["sub_mchid"] != "" {
		q.Set("sub_mchid", sub["sub_mchid"])
	}
	resp, raw, err := doSigned(ctx, http.MethodGet, "/v3/bill/tradebill?"+q.Encode(), nil, prov)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		if f.ErrCode == "NO_STATEMENT_EXIST" || resp.StatusCode == http.StatusNotFound {
			return nil, channel.ErrNoBill
		}
		return nil, fmt.Errorf("tradebill: %s %s", f.ErrCode, f.ErrMsg)
	}
	var meta struct {
		DownloadURL string `json:"download_url"`
	}
	if json.Unmarshal(raw, &meta) != nil || meta.DownloadURL == "" {
		return nil, fmt.Errorf("tradebill: bad response")
	}
	dresp, data, err := doSigned(ctx, http.MethodGet, meta.DownloadURL, nil, prov)
	if err != nil {
		return nil, err
	}
	if dresp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("bill download: status %d", dresp.StatusCode)
	}
	return parseTradeBill(string(data))
}

// parseTradeBill 解析账单 CSV。首行表头；数据行每个字段以 ` 开头；之后是汇总表头 + 汇总行。
// 按表头中文名取列，服务商账单与普通商户账单列集不同也能解析。
func parseTradeBill(csv string) ([]channel.BillRow, error) {
	csv = strings.TrimPrefix(csv, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(csv, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("empty bill")
	}
	header := strings.Split(strings.TrimSpace(lines[0]), ",")
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	get := func(fields []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(fields) {
			return ""
		}
		return strings.TrimPrefix(strings.TrimSpace(fields[i]), "`")
	}
	var rows []channel.BillRow
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "`") {
			break // 汇总表头：数据行结束
		}
		fields := strings.Split(line, ",")
		rows = append(rows, channel.BillRow{
			TradeTime:      get(fields, "交易时间"),
			SubMchID:       get(fields, "特约商户号"),
			ChannelOrderNo: get(fields, "微信订单号"),
			OutTradeNo:     get(fields, "商户订单号"),
			TradeState:     get(fields, "交易状态"),
			Amount:         yuanToFen(get(fields, "订单金额")),
			RefundID:       get(fields, "微信退款单号"),
			OutRefundNo:    get(fields, "商户退款单号"),
			RefundAmount:   yuanToFen(get(fields, "退款金额")),
			RefundState:    get(fields, "退款状态"),
			Fee:            yuanToFen(get(fields, "手续费")),
		})
	}
	return rows, nil
}

// yuanToFen "12.34" → 1234；空或非法返回 0。
func yuanToFen(s string) int64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * 100))
}
