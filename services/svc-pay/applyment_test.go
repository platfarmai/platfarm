package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"platfarm/svc-pay/channel"
)

func samplePayload() map[string]any {
	return map[string]any{
		"subject": map[string]any{"type": "individual", "name": "张三小吃店", "short_name": "张三小吃",
			"license_no": "92110108MA00ABCD1X", "license_valid": "2021-05-01,长期", "mcc": "719"},
		"legal_person": map[string]any{"name": "张三", "id_type": "id_card", "id_no": "110101199001011234",
			"id_valid": "2015-01-01,2035-01-01", "phone": "13800138000"},
		"contact":            map[string]any{"name": "张三", "phone": "13800138000"},
		"business":           map[string]any{"service_phone": "13800138000", "address": map[string]any{"province": "北京市", "city": "北京市", "detail": "某路1号", "code": "110108"}, "scene": map[string]any{"types": []string{"offline"}}},
		"settlement_account": map[string]any{"type": "personal", "bank": "招商银行", "account_name": "张三", "account_no": "6225881234567890"},
		"rates":              []map[string]any{{"way_code": "WX_JSAPI", "fee_rate_bps": 38, "settle_cycle": "T1"}},
		"images":             map[string]string{"license": "101", "id_front": "102", "id_back": "103"},
	}
}

// makePollDue 把下一次轮询时间拨到现在，模拟时间流逝。
func makePollDue(t *testing.T, id string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `UPDATE pay_applyment SET next_poll_at=now() WHERE applyment_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func stubFetchFile(_ context.Context, bearer, fileID string) (fetchedFile, error) {
	if bearer == "" {
		return fetchedFile{}, fmt.Errorf("no bearer forwarded")
	}
	return fetchedFile{Filename: "f" + fileID + ".jpg", Mime: "image/jpeg", Size: 7, Content: []byte("JPG" + fileID)}, nil
}

func TestApplyment_FullFlow_DraftToFinished(t *testing.T) {
	requireDB(t)
	fetchFile = stubFetchFile
	seedISVAndMerchant(t, "ISV1", "M-FLOW", "stub_channel")
	r := testRouter()
	ctx := context.Background()

	// 1. operator 建草稿：资料密封、图片留存。
	res := call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-FLOW", "ifCode": "stub_channel", "payload": samplePayload()})
	mustCode(t, res, 200)
	ap := res.Body["applyment"].(map[string]any)
	id := ap["applymentId"].(string)
	if ap["state"] != channel.ApplymentDraft {
		t.Fatalf("state %v", ap["state"])
	}
	payload := ap["payload"].(map[string]any)
	if got := payload["legal_person"].(map[string]any)["id_no"]; got != "110***1234" {
		t.Fatalf("id_no should be masked in response, got %v", got)
	}
	var stored string
	_ = db.QueryRow(ctx, `SELECT payload->'legal_person'->>'id_no' FROM pay_applyment WHERE applyment_id=$1`, id).Scan(&stored)
	if stored == "110101199001011234" || stored == "" {
		t.Fatalf("id_no stored in plaintext or empty: %q", stored)
	}
	var mediaCount int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM pay_applyment_media WHERE applyment_id=$1`, id).Scan(&mediaCount)
	if mediaCount != 3 {
		t.Fatalf("media rows = %d, want 3", mediaCount)
	}

	// 2. nobody（无 operator 角色）不能看也不能改。
	mustCode(t, call(t, r, "nobody", http.MethodGet, "/api/pay/admin/applyments/"+id, nil), 403)

	// 3. 提交审核。
	res = call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil)
	mustCode(t, res, 200)
	if res.Body["applyment"].(map[string]any)["state"] != channel.ApplymentReviewing {
		t.Fatalf("state %v", res.Body["applyment"])
	}
	// 重复提交审核：状态不对 → 409。
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil), 409)

	// 4. operator 没有 reviewer 角色 → 403；reviewer 通过 → 自动提交渠道 → submitted。
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/approve", nil), 403)
	stub.setView(channel.ApplymentView{})
	res = call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments/"+id+"/approve", nil)
	mustCode(t, res, 200)
	ap = res.Body["applyment"].(map[string]any)
	if ap["state"] != channel.ApplymentSubmitted || ap["channelApplymentId"] != "CH-"+id || ap["unexpressed"] != "rates" {
		t.Fatalf("after approve: state=%v channel=%v unexpressed=%v err=%v", ap["state"], ap["channelApplymentId"], ap["unexpressed"], res.Body["channelError"])
	}
	stub.mu.Lock()
	uploads := len(stub.uploads)
	stub.mu.Unlock()
	if uploads != 3 {
		t.Fatalf("media uploads = %d, want 3", uploads)
	}
	var mediaUploaded int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM pay_applyment_media WHERE applyment_id=$1 AND media_id<>''`, id).Scan(&mediaUploaded)
	if mediaUploaded != 3 {
		t.Fatalf("media_id written back = %d", mediaUploaded)
	}

	// 5. 轮询：提交后首次轮询安排在 1 分钟后，未到点不查渠道。
	stub.setView(channel.ApplymentView{State: channel.ApplymentChannelAuditing, RawState: "APPLYMENT_STATE_AUDITING"})
	if err := pollApplyments(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := findApplyment(ctx, id)
	if a.State != channel.ApplymentSubmitted {
		t.Fatalf("poll before next_poll_at must not query channel, got %s", a.State)
	}
	// 时间到点（模拟）→ 渠道审核中 → channel_auditing，并安排下一次。
	makePollDue(t, id)
	if err := pollApplyments(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = findApplyment(ctx, id)
	if a.State != channel.ApplymentChannelAuditing || a.NextPollAt == nil {
		t.Fatalf("after poll: %s nextPoll=%v", a.State, a.NextPollAt)
	}
	// next_poll_at 在未来，立即再轮询不应触达；手动 sync 不受限制。
	stub.setView(channel.ApplymentView{State: channel.ApplymentNeedSign, RawState: "APPLYMENT_STATE_TO_BE_SIGNED", SignURL: "https://sign"})
	_ = pollApplyments(ctx)
	a, _ = findApplyment(ctx, id)
	if a.State != channel.ApplymentChannelAuditing {
		t.Fatalf("poll should respect next_poll_at, got %s", a.State)
	}
	res = call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/sync", nil)
	mustCode(t, res, 200)
	a, _ = findApplyment(ctx, id)
	if a.State != channel.ApplymentNeedSign || a.SignURL != "https://sign" {
		t.Fatalf("after sync: %s %s", a.State, a.SignURL)
	}
	stub.setView(channel.ApplymentView{State: channel.ApplymentFinished, RawState: "APPLYMENT_STATE_FINISHED", SubMchID: "1900001"})
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/sync", nil), 200)
	a, _ = findApplyment(ctx, id)
	if a.State != channel.ApplymentFinished || a.SubMchID != "1900001" || a.NextPollAt != nil {
		t.Fatalf("after finish: %+v", a)
	}
	params, err := loadChannelParams(ctx, "merchant", "M-FLOW", "stub_channel")
	if err != nil || params["sub_mchid"] != "1900001" {
		t.Fatalf("sub_mchid not written to merchant channel config: %v %v", params, err)
	}
	// 终态不再变化：渠道哪怕回退也不动（finished 不在轮询集合里，到点也不会被查）。
	stub.setView(channel.ApplymentView{State: channel.ApplymentRejected, RawState: "APPLYMENT_STATE_REJECTED"})
	makePollDue(t, id)
	_ = pollApplyments(ctx)
	a, _ = findApplyment(ctx, id)
	if a.State != channel.ApplymentFinished {
		t.Fatalf("finished must be terminal, got %s", a.State)
	}
	// 已 finished 不可取消。
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/cancel", nil), 409)
}

func TestApplyment_RejectThenResubmit(t *testing.T) {
	requireDB(t)
	fetchFile = stubFetchFile
	seedISVAndMerchant(t, "ISV1", "M-REJ", "stub_channel")
	r := testRouter()

	res := call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-REJ", "ifCode": "stub_channel", "payload": samplePayload()})
	mustCode(t, res, 200)
	id := res.Body["applyment"].(map[string]any)["applymentId"].(string)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil), 200)

	// 驳回必须带原因。
	mustCode(t, call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments/"+id+"/reject", map[string]any{"reason": ""}), 400)
	res = call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments/"+id+"/reject", map[string]any{"reason": "营业执照模糊"})
	mustCode(t, res, 200)
	ap := res.Body["applyment"].(map[string]any)
	if ap["state"] != channel.ApplymentRejected || ap["rejectReason"] != "营业执照模糊" || ap["reviewer"] != "reviewer" {
		t.Fatalf("%v", ap)
	}

	// 驳回后不能直接再提交审核，要先改资料（回到 draft）。
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil), 409)
	p := samplePayload()
	p["images"] = map[string]string{"license": "201", "id_front": "102", "id_back": "103"} // 换了执照图
	res = call(t, r, "op", http.MethodPut, "/api/pay/admin/applyments/"+id, map[string]any{"payload": p})
	mustCode(t, res, 200)
	ap = res.Body["applyment"].(map[string]any)
	if ap["state"] != channel.ApplymentDraft || ap["rejectReason"] != "" {
		t.Fatalf("after update: %v", ap)
	}
	var lic string
	_ = db.QueryRow(context.Background(), `SELECT file_id FROM pay_applyment_media WHERE applyment_id=$1 AND kind='license'`, id).Scan(&lic)
	if lic != "201" {
		t.Fatalf("license media not refreshed: %s", lic)
	}
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil), 200)

	// 列表按状态过滤。
	res = call(t, r, "admin", http.MethodGet, "/api/pay/admin/applyments?state=reviewing&mchNo=M-REJ", nil)
	mustCode(t, res, 200)
	if items := res.Body["items"].([]any); len(items) != 1 {
		t.Fatalf("reviewing list = %d", len(items))
	}
}

func TestApplyment_ChannelSubmitErrorKeepsApproved(t *testing.T) {
	requireDB(t)
	fetchFile = stubFetchFile
	seedISVAndMerchant(t, "ISV1", "M-ERR", "stub_channel")
	r := testRouter()

	res := call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-ERR", "ifCode": "stub_channel", "payload": samplePayload()})
	mustCode(t, res, 200)
	id := res.Body["applyment"].(map[string]any)["applymentId"].(string)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil), 200)

	stub.mu.Lock()
	stub.submitErr = fmt.Errorf("applyment submit: PARAM_ERROR 结算规则不存在")
	stub.mu.Unlock()
	res = call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments/"+id+"/approve", nil)
	mustCode(t, res, 200)
	ap := res.Body["applyment"].(map[string]any)
	if ap["state"] != channel.ApplymentApproved || !strings.Contains(ap["channelMsg"].(string), "PARAM_ERROR") || res.Body["channelError"] == nil {
		t.Fatalf("expected approved with channel error: %v %v", ap, res.Body["channelError"])
	}
	// 修好渠道问题后手动重提成功。
	stub.mu.Lock()
	stub.submitErr = nil
	stub.mu.Unlock()
	res = call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit", nil)
	mustCode(t, res, 200)
	if res.Body["applyment"].(map[string]any)["state"] != channel.ApplymentSubmitted {
		t.Fatalf("%v", res.Body)
	}
}

func TestApplyment_ValidationAndGuards(t *testing.T) {
	requireDB(t)
	fetchFile = stubFetchFile
	seedISVAndMerchant(t, "ISV1", "M-VAL", "stub_channel")
	r := testRouter()

	// 资料不完整 → 400 + problems。
	bad := samplePayload()
	delete(bad, "contact")
	res := call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-VAL", "ifCode": "stub_channel", "payload": bad})
	mustCode(t, res, 400)
	if probs, _ := res.Body["problems"].([]any); len(probs) == 0 {
		t.Fatalf("expected problems list: %v", res.Body)
	}
	// 渠道不支持进件 → 400；商户不存在 → 404；商户未挂服务商 → 400。
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-VAL", "ifCode": "no_such", "payload": samplePayload()}), 400)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-NONE", "ifCode": "stub_channel", "payload": samplePayload()}), 404)
	_, _ = db.Exec(context.Background(), `INSERT INTO pay_merchant (mch_no, mch_name, mch_type) VALUES ('M-NOISV','x',1) ON CONFLICT DO NOTHING`)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-NOISV", "ifCode": "stub_channel", "payload": samplePayload()}), 400)

	// 图片拉取失败 → 502 且草稿回滚。
	fetchFile = func(context.Context, string, string) (fetchedFile, error) { return fetchedFile{}, fmt.Errorf("boom") }
	res = call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-VAL", "ifCode": "stub_channel", "payload": samplePayload()})
	mustCode(t, res, 502)
	var n int
	_ = db.QueryRow(context.Background(), `SELECT count(*) FROM pay_applyment WHERE mch_no='M-VAL'`).Scan(&n)
	if n != 0 {
		t.Fatalf("draft should be rolled back, found %d", n)
	}
	fetchFile = stubFetchFile

	// 取消：草稿可取消，取消后不可再审。
	res = call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-VAL", "ifCode": "stub_channel", "payload": samplePayload()})
	mustCode(t, res, 200)
	id := res.Body["applyment"].(map[string]any)["applymentId"].(string)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/cancel", nil), 200)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil), 409)
	mustCode(t, call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments/"+id+"/approve", nil), 409)
}

func TestSealOpenMaskPayload(t *testing.T) {
	var a channel.Applyment
	a.LegalPerson.IDNo = "110101199001011234"
	a.SettlementAccount.AccountNo = "6225881234567890"
	sealed, err := sealPayload(a)
	if err != nil || sealed.LegalPerson.IDNo == a.LegalPerson.IDNo {
		t.Fatalf("seal: %v", err)
	}
	opened, err := openPayload(sealed)
	if err != nil || opened.LegalPerson.IDNo != a.LegalPerson.IDNo || opened.SettlementAccount.AccountNo != a.SettlementAccount.AccountNo {
		t.Fatalf("open: %v %+v", err, opened)
	}
	raw, _ := json.Marshal(sealed)
	var masked channel.Applyment
	_ = json.Unmarshal(maskedPayload(raw), &masked)
	if masked.LegalPerson.IDNo != "110***1234" || masked.SettlementAccount.AccountNo != "622***7890" {
		t.Fatalf("mask: %+v", masked)
	}
}

func TestPollBackoff(t *testing.T) {
	if pollBackoff(0).Minutes() != 1 || pollBackoff(1).Minutes() != 2 || pollBackoff(3).Minutes() != 8 {
		t.Fatal("backoff ladder wrong")
	}
	if pollBackoff(20).Hours() != 1 {
		t.Fatalf("cap should be 1h, got %v", pollBackoff(20))
	}
}
