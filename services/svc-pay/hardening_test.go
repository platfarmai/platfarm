package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

func TestApplyment_TwoPersonReviewAndSealedImages(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	fetchFile = stubFetchFile
	testUsers["reviewer2"] = &Claims{TokenType: "access", UserId: 12, Username: "reviewer2", Role: "user"}
	testGrants[12] = map[string]bool{roleReviewer: true}
	defer func() { delete(testUsers, "reviewer2"); delete(testGrants, 12) }()
	seedISVAndMerchant(t, "ISV-2P", "M-2P", "stub_channel")
	r := testRouter()

	// reviewer 同时持有 operator：自己录入的资料不能自己通过。
	res := call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments",
		map[string]any{"mchNo": "M-2P", "ifCode": "stub_channel", "payload": samplePayload()})
	mustCode(t, res, 200)
	id := res.Body["applyment"].(map[string]any)["applymentId"].(string)

	// 证件照片落库是密文，不是 svc-file 给的原始字节。
	var content []byte
	var sealed bool
	if err := db.QueryRow(ctx, `SELECT content, content_sealed FROM pay_applyment_media WHERE applyment_id=$1 AND kind='id_front'`, id).
		Scan(&content, &sealed); err != nil {
		t.Fatal(err)
	}
	if !sealed || strings.Contains(string(content), "JPG102") {
		t.Fatalf("id photo stored in plaintext: sealed=%v content=%q", sealed, content)
	}

	mustCode(t, call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments/"+id+"/submit-review", nil), 200)
	res = call(t, r, "reviewer", http.MethodPost, "/api/pay/admin/applyments/"+id+"/approve", nil)
	mustCode(t, res, 403)
	if !strings.Contains(res.Body["error"].(string), "自己") {
		t.Fatalf("self-approval error = %v", res.Body)
	}

	// 另一位审核员通过；提交渠道时图片已解密，渠道收到的是原始字节。
	res = call(t, r, "reviewer2", http.MethodPost, "/api/pay/admin/applyments/"+id+"/approve", nil)
	mustCode(t, res, 200)
	if st := res.Body["applyment"].(map[string]any)["state"]; st != channel.ApplymentSubmitted {
		t.Fatalf("after approve: %v %v", st, res.Body["channelError"])
	}
	stub.mu.Lock()
	got := stub.uploadData["f102.jpg"]
	stub.mu.Unlock()
	if got != "JPG102" {
		t.Fatalf("channel received %q, want the decrypted image", got)
	}

	// 进件完成不会把被停用的商户重新启用。
	if _, err := db.Exec(ctx, `UPDATE pay_merchant SET state='disabled' WHERE mch_no='M-2P'`); err != nil {
		t.Fatal(err)
	}
	stub.setView(channel.ApplymentView{State: channel.ApplymentFinished, RawState: "APPLYMENT_STATE_FINISHED", SubMchID: "1900002"})
	mustCode(t, call(t, r, "reviewer2", http.MethodPost, "/api/pay/admin/applyments/"+id+"/sync", nil), 403)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/applyments/"+id+"/sync", nil), 200)
	if m, _ := loadMerchant(ctx, "M-2P"); m.State != stateDisabled {
		t.Fatalf("finished applyment re-enabled a disabled merchant: %s", m.State)
	}
}

func TestOpenAPI_SignatureBindsMethodPathAndNonce(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, secret := seedStubMerchant(t, "ISV-SG", "M-SG", "A-SG")
	o := seedSuccessOrder(t, "M-SG", "ISV-SG", app.AppID, "P-SG-1", 1000, 60, 20)
	r := gin.New()
	r.GET(mount+"/v1/orders/:mchOrderNo", handleQueryOrder)
	r.POST(mount+"/v1/orders/:mchOrderNo/close", handleCloseOrder)

	send := func(method, path, ts, nonce, sig string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(""))
		req.Header.Set("X-Pay-App-Id", app.AppID)
		req.Header.Set("X-Pay-Timestamp", ts)
		req.Header.Set("X-Pay-Nonce", nonce)
		req.Header.Set("X-Pay-Signature", sig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	queryPath := "/api/pay/v1/orders/" + o.MchOrderNo
	sig := signPayload(secret, app.AppID, ts, "n-sg-1", http.MethodGet, queryPath, "")

	// 截获的查询签名（空正文）不能挪去关单。
	if code := send(http.MethodPost, queryPath+"/close", ts, "n-sg-1", sig); code != http.StatusUnauthorized {
		t.Fatalf("signature reused on another endpoint: %d", code)
	}
	if code := send(http.MethodGet, queryPath, ts, "n-sg-1", sig); code != http.StatusOK {
		t.Fatalf("legit query: %d", code)
	}
	// 同一随机串再来一次是重放。
	if code := send(http.MethodGet, queryPath, ts, "n-sg-1", sig); code != http.StatusUnauthorized {
		t.Fatalf("replay accepted: %d", code)
	}
	if got, _ := findOrder(ctx, o.PayOrderID); got.State != orderSuccess {
		t.Fatalf("order changed: %s", got.State)
	}
}
