package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"platfarm/svc-pay/channel"
)

// TestMain：设置了 PAY_TEST_DATABASE_URL 时连库、重建 schema、跑迁移，否则 DB 用例自行 Skip。
// 密钥随机生成（密封/解封只需进程内一致）。
func TestMain(m *testing.M) {
	secretKey = make([]byte, 32)
	_, _ = rand.Read(secretKey)
	gin.SetMode(gin.TestMode)
	log.SetOutput(os.Stderr)

	if url := os.Getenv("PAY_TEST_DATABASE_URL"); url != "" {
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			fmt.Println("test db:", err)
			os.Exit(1)
		}
		if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			fmt.Println("reset schema:", err)
			os.Exit(1)
		}
		if err := runMigrations(ctx, pool); err != nil {
			fmt.Println("migrate:", err)
			os.Exit(1)
		}
		db = pool
	}
	registerStubChannel()
	os.Exit(m.Run())
}

func requireDB(t *testing.T) {
	t.Helper()
	if db == nil {
		t.Skip("PAY_TEST_DATABASE_URL 未设置，跳过数据库用例")
	}
}

// ── 测试路由：用 X-Test-User 头伪造身份 ────────────────────────

// testUsers id → claims；角色由 roleChecker 桩决定。
var testUsers = map[string]*Claims{
	"admin":    {TokenType: "access", UserId: 1, Username: "admin", Role: "admin"},
	"op":       {TokenType: "access", UserId: 7, Username: "op", Role: "user"},
	"reviewer": {TokenType: "access", UserId: 8, Username: "reviewer", Role: "user"},
	"nobody":   {TokenType: "access", UserId: 9, Username: "nobody", Role: "user"},
}

var testGrants = map[int]map[string]bool{
	7: {roleOperator: true},
	8: {roleOperator: true, roleReviewer: true},
}

func stubRoleChecker(_ context.Context, _ string, userID int, role string) (bool, error) {
	return testGrants[userID][role], nil
}

func testRouter() *gin.Engine {
	roleChecker = stubRoleChecker
	r := gin.New()
	admin := r.Group(mount+"/admin", func(c *gin.Context) {
		u, ok := testUsers[c.GetHeader("X-Test-User")]
		if !ok {
			c.AbortWithStatusJSON(401, gin.H{"error": "no test user"})
			return
		}
		c.Set("claims", u)
	})
	registerAdminRoutes(admin)
	return r
}

type resp struct {
	Code int
	Body map[string]any
}

func call(t *testing.T, r *gin.Engine, user, method, path string, body any) resp {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = strings.NewReader(string(raw))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", user)
	req.Header.Set("Authorization", "Bearer test-"+user)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	out := resp{Code: w.Code, Body: map[string]any{}}
	_ = json.Unmarshal(w.Body.Bytes(), &out.Body)
	return out
}

func mustCode(t *testing.T, r resp, want int) {
	t.Helper()
	if r.Code != want {
		t.Fatalf("status %d, want %d: %v", r.Code, want, r.Body)
	}
}

// ── 桩渠道：同时实现 PayChannel + OnboardingChannel，行为可按用例调整 ─────

type stubChannel struct {
	mu          sync.Mutex
	uploads     []string
	submitErr   error
	view        channel.ApplymentView
	payResult   channel.Result
	queryResult channel.Result
}

var stub = &stubChannel{}

var registerStubOnce sync.Once

func registerStubChannel() {
	registerStubOnce.Do(func() { channel.Register(stub) })
}

func (s *stubChannel) IfCode() string   { return "stub_channel" }
func (s *stubChannel) FundMode() string { return channel.FundDirect }
func (s *stubChannel) Ways() []string   { return []string{"STUB_PAY"} }
func (s *stubChannel) Pay(_ context.Context, _ channel.PayIn) (channel.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.payResult, nil
}
func (s *stubChannel) Query(_ context.Context, _ channel.OrderRef) (channel.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queryResult, nil
}
func (s *stubChannel) Close(context.Context, channel.OrderRef) error { return nil }
func (s *stubChannel) Refund(context.Context, channel.RefundIn) (channel.Result, error) {
	return channel.Result{State: channel.StateSuccess, ChannelOrderNo: "RF1"}, nil
}
func (s *stubChannel) ParseNotify(*http.Request, map[string]string) (channel.Notify, error) {
	return channel.Notify{}, fmt.Errorf("stub has no notify")
}
func (s *stubChannel) UploadMedia(_ context.Context, filename string, _ []byte, _ map[string]string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, filename)
	return "MEDIA-" + filename, nil
}
func (s *stubChannel) SubmitApplyment(_ context.Context, in channel.ApplymentIn) (channel.ApplymentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.submitErr != nil {
		return channel.ApplymentResult{}, s.submitErr
	}
	if in.Payload.LegalPerson.IDNo == "" || strings.HasPrefix(in.Payload.LegalPerson.IDNo, "sealed") {
		return channel.ApplymentResult{}, fmt.Errorf("payload not opened")
	}
	for kind := range in.Payload.Images {
		if in.Media[kind] == "" {
			return channel.ApplymentResult{}, fmt.Errorf("media %s not uploaded", kind)
		}
	}
	return channel.ApplymentResult{ChannelApplymentID: "CH-" + in.BusinessCode, Unexpressed: []string{"rates"}}, nil
}
func (s *stubChannel) QueryApplyment(_ context.Context, _ string, _ map[string]string) (channel.ApplymentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view, nil
}

func (s *stubChannel) setView(v channel.ApplymentView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = v
}

// ── 种子数据 ────────────────────────────────────────────────

// seedISVAndMerchant 建服务商 + 挂在其下的特约商户 + 服务商渠道参数，返回 mchNo。
func seedISVAndMerchant(t *testing.T, isvNo, mchNo, ifCode string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO pay_isv (isv_no, isv_name) VALUES ($1,$1) ON CONFLICT DO NOTHING`, isvNo); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO pay_merchant (mch_no, mch_name, mch_type, isv_no) VALUES ($1,$1,2,$2)
		ON CONFLICT (mch_no) DO UPDATE SET isv_no=$2`, mchNo, isvNo); err != nil {
		t.Fatal(err)
	}
	sealed, err := mustSealParams(map[string]string{"sp_mchid": "1900000109"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO pay_channel_config (owner_type, owner_id, if_code, if_params)
		VALUES ('isv',$1,$2,$3) ON CONFLICT (owner_type, owner_id, if_code) DO UPDATE SET if_params=$3`, isvNo, ifCode, sealed); err != nil {
		t.Fatal(err)
	}
}
