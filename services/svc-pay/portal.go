// 商户自助门户（商户中心）。
//
// 运营侧（/admin/merchants/:mchNo/portal*）：一键为商户开通登录账号 + 开放平台 appKey，重置密码 / 密钥，启停登录。
// 商户侧（/portal/*）：只读自己的商户资料、应用与通道、订单，自助换发签名密钥，以及调试工具（playground.go）。
//
// 授权模型：pay_merchant_portal 里有 active 行的登录账号 = 该商户的门户用户，数据范围就是那一个 mch_no。
// 它不是 svc-grants 里的业务角色——角色说不清“是哪一个商户”，而绑定行可以。
// 门户用户没有 operator / reviewer 角色，管理接口（/admin/*）对它一律 403。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
	"platfarm/svc-pay/channel/sandbox"
)

// portalScopes 商户 appKey 的默认授权：收款、退款、查账。
// 分账 / 结算确认 / 提现涉及把钱划给别人，不默认给；需要时由平台在开放平台控制台追加。
var portalScopes = []string{"pay.order.create", "pay.order.query", "pay.refund.create", "pay.refund.query", "pay.bill.query"}

var portalUsernameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,31}$`)

type portalRow struct {
	MchNo      string
	UserID     int
	Username   string
	OpenAppKey string
	State      string
	TenantID   int
}

const portalCols = `mch_no, user_id, username, open_app_key, state, tenant_id`

func scanPortal(row interface{ Scan(...any) error }) (portalRow, error) {
	var p portalRow
	err := row.Scan(&p.MchNo, &p.UserID, &p.Username, &p.OpenAppKey, &p.State, &p.TenantID)
	return p, err
}

func loadPortalByMch(ctx context.Context, mchNo string) (portalRow, error) {
	return scanPortal(db.QueryRow(ctx, `SELECT `+portalCols+` FROM pay_merchant_portal WHERE mch_no=$1`, mchNo))
}

func loadPortalByUser(ctx context.Context, userID int) (portalRow, error) {
	return scanPortal(db.QueryRow(ctx, `SELECT `+portalCols+` FROM pay_merchant_portal WHERE user_id=$1`, userID))
}

// portalView 商户列表里带出的门户开通情况。
type portalView struct {
	Username string `json:"username"`
	AppKey   string `json:"appKey"`
	State    string `json:"state"`
}

// openAppKeyFor 商户的开放平台 appKey：mch-<商户号小写>。商户号里有其它字符时折成 '-' 并挂一段摘要防撞名。
func openAppKeyFor(mchNo string) string {
	lower := strings.ToLower(mchNo)
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, lower)
	if clean == lower {
		return "mch-" + clean
	}
	sum := sha256.Sum256([]byte(mchNo))
	return "mch-" + clean + "-" + hex.EncodeToString(sum[:3])
}

func bearerToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
}

// ownedMerchant 取商户并确认它属于调用者租户。失败已写响应。
func ownedMerchant(c *gin.Context, claims *Claims, mchNo string) (Merchant, bool) {
	mch, err := loadMerchant(c.Request.Context(), mchNo)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "商户不存在"})
		return Merchant{}, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return Merchant{}, false
	}
	if denyOtherTenant(c, claims, mch.TenantID, "商户") {
		return Merchant{}, false
	}
	return mch, true
}

// requirePortalAdmin 开账号、重置密码、换发 appSecret 都要经 auth 的管理接口，auth 只认平台管理员代持。
func requirePortalAdmin(c *gin.Context) (*Claims, bool) {
	claims, ok := requireOperator(c)
	if !ok {
		return nil, false
	}
	if !isPlatformAdmin(claims) {
		c.JSON(http.StatusForbidden, gin.H{"error": "这一步要在账号服务里建 / 改登录账号，只有平台管理员可以操作"})
		return nil, false
	}
	return claims, true
}

// authFailed 把 auth 的非预期响应折成给操作员看的错误。已写响应。
func authFailed(c *gin.Context, what string, status int, raw []byte, err error) {
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": what + "失败：" + err.Error()})
		return
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	switch status {
	case http.StatusUnauthorized:
		c.JSON(http.StatusBadGateway, gin.H{"error": what + "失败：账号服务没有放行 svc-pay（auth 的 AUTH_LOGIN_SERVICES 需包含 svc-pay）"})
	case http.StatusForbidden:
		c.JSON(http.StatusForbidden, gin.H{"error": what + "失败：需要平台管理员身份"})
	default:
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("%s失败：账号服务返回 %d %s", what, status, trunc(body.Error, 200))})
	}
}

// ── 运营侧 ──────────────────────────────────────────────────

// handleProvisionPortal POST /admin/merchants/:mchNo/portal {username}
// 一键开通：建登录账号（密码只回一次）→ 绑定商户 → 签发开放平台 appKey（appSecret 只回一次）
// → 商户还没有应用时顺手建一个（签名密钥只回一次）。
// 中途失败可以原样再点一次：已完成的步骤不会重做，只补没做完的。
func handleProvisionPortal(c *gin.Context) {
	claims, ok := requirePortalAdmin(c)
	if !ok {
		return
	}
	mch, ok := ownedMerchant(c, claims, c.Param("mchNo"))
	if !ok {
		return
	}
	var in struct {
		Username string `json:"username"`
	}
	_ = c.ShouldBindJSON(&in) // 补做未完成的开通时可以不带请求体
	in.Username = strings.TrimSpace(in.Username)
	ctx := c.Request.Context()
	userToken := bearerToken(c)
	out := gin.H{"mchNo": mch.MchNo, "mchName": mch.MchName}

	p, err := loadPortalByMch(ctx, mch.MchNo)
	switch {
	case err == nil && p.OpenAppKey != "":
		c.JSON(http.StatusConflict, gin.H{"error": "该商户已开通登录（账号 " + p.Username + "）。忘记密码或密钥请用「重置」"})
		return
	case err != nil && !notFound(err):
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	case notFound(err):
		if !portalUsernameRe.MatchString(in.Username) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "登录名需为 3–32 位字母、数字或 . _ -，且以字母或数字开头"})
			return
		}
		status, raw, aerr := authAdmin(ctx, userToken, http.MethodPost, "/internal/auth/users",
			map[string]string{"username": in.Username, "role": "user"})
		if aerr == nil && status == http.StatusConflict {
			c.JSON(http.StatusConflict, gin.H{"error": "登录名 " + in.Username + " 已被占用，换一个"})
			return
		}
		var created struct {
			ID       int    `json:"id"`
			Password string `json:"password"`
		}
		if aerr != nil || status != http.StatusCreated || json.Unmarshal(raw, &created) != nil || created.ID == 0 {
			authFailed(c, "创建登录账号", status, raw, aerr)
			return
		}
		if _, err := db.Exec(ctx, `INSERT INTO pay_merchant_portal (mch_no, user_id, username, tenant_id, created_by)
			VALUES ($1,$2,$3,$4,$5)`, mch.MchNo, created.ID, in.Username, mch.TenantID, claims.UserId); err != nil {
			// 账号已经建出来但没绑上：把密码还给操作员，免得留下一个谁也登不上的账号。
			log.Printf("portal bind %s user %d: %v", mch.MchNo, created.ID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "登录账号 " + in.Username + " 已创建但绑定商户失败，请到用户管理里停用该账号后重试"})
			return
		}
		// 商户不在平台租户时，把账号挪进商户所在租户（auth 换租户会吊销旧 token，新账号无影响）。
		if mch.TenantID != 0 {
			if st, _, perr := authAdmin(ctx, userToken, http.MethodPatch, fmt.Sprintf("/internal/auth/users/%d", created.ID),
				map[string]int{"tenantId": mch.TenantID}); perr != nil || st != http.StatusOK {
				log.Printf("portal tenant %s user %d: status=%d err=%v", mch.MchNo, created.ID, st, perr)
				out["warning"] = "账号已创建，但未能归入商户所在租户；不影响商户中心的使用"
			}
		}
		p = portalRow{MchNo: mch.MchNo, UserID: created.ID, Username: in.Username, State: stateActive, TenantID: mch.TenantID}
		out["password"] = created.Password
	}
	out["username"] = p.Username

	// 开放平台 appKey：appSecret 在 auth 里只存哈希，这一次响应之后谁也拿不回明文。
	appKey := openAppKeyFor(mch.MchNo)
	status, raw, aerr := authAdmin(ctx, userToken, http.MethodPost, "/internal/auth/apps", map[string]any{
		"appKey": appKey, "name": trunc(mch.MchName, 60), "scopes": portalScopes, "ratePerMin": 120})
	if aerr == nil && status == http.StatusConflict {
		// 上一次开通在写回绑定前中断：key 已存在但明文没人见过，换发一把。
		status, raw, aerr = authAdmin(ctx, userToken, http.MethodPost, "/internal/auth/apps/"+url.PathEscape(appKey)+"/rotate", nil)
		if aerr == nil && status == http.StatusOK {
			status = http.StatusCreated
		}
	}
	var issued struct {
		AppSecret string `json:"appSecret"`
	}
	if aerr != nil || status != http.StatusCreated || json.Unmarshal(raw, &issued) != nil || issued.AppSecret == "" {
		if _, has := out["password"]; has {
			// 密码只有这一次机会交到操作员手里：appKey 没签成也要把它带回去。
			out["error"] = "登录账号已创建，但开放平台 appKey 签发失败；保存好下面的初始密码，然后再点一次「开通商户登录」补发 appKey"
			out["partial"] = true
			c.JSON(http.StatusOK, out)
			return
		}
		authFailed(c, "签发开放平台 appKey", status, raw, aerr)
		return
	}
	if _, err := db.Exec(ctx, `UPDATE pay_merchant_portal SET open_app_key=$2, updated_at=now() WHERE mch_no=$1`,
		mch.MchNo, appKey); err != nil {
		log.Printf("portal appkey %s: %v", mch.MchNo, err)
	}
	out["appKey"], out["appSecret"] = appKey, issued.AppSecret

	// 商户还没有应用：顺手建一个，否则登录进去什么凭据都看不到。已有应用的不动它的密钥。
	var apps int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pay_app WHERE mch_no=$1`, mch.MchNo).Scan(&apps); err == nil && apps == 0 {
		if appID, secret, cerr := createApp(ctx, mch, "默认应用"); cerr == nil {
			out["payApp"] = gin.H{"appId": appID, "appSecret": secret}
		} else {
			log.Printf("portal default app %s: %v", mch.MchNo, cerr)
		}
	}
	log.Printf(`{"event":"portal_provision","mchNo":%q,"username":%q,"appKey":%q,"by":%q}`, mch.MchNo, p.Username, appKey, claims.Username)
	c.JSON(http.StatusOK, out)
}

// portalForAdmin 取商户的门户绑定（运营侧）。失败已写响应。
func portalForAdmin(c *gin.Context, claims *Claims) (Merchant, portalRow, bool) {
	mch, ok := ownedMerchant(c, claims, c.Param("mchNo"))
	if !ok {
		return Merchant{}, portalRow{}, false
	}
	p, err := loadPortalByMch(c.Request.Context(), mch.MchNo)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "该商户还没有开通登录"})
		return Merchant{}, portalRow{}, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return Merchant{}, portalRow{}, false
	}
	return mch, p, true
}

// handleResetPortalPassword POST /admin/merchants/:mchNo/portal/password：重置登录密码，新密码只回一次。
func handleResetPortalPassword(c *gin.Context) {
	claims, ok := requirePortalAdmin(c)
	if !ok {
		return
	}
	mch, p, ok := portalForAdmin(c, claims)
	if !ok {
		return
	}
	status, raw, err := authAdmin(c.Request.Context(), bearerToken(c), http.MethodPost,
		fmt.Sprintf("/internal/auth/users/%d/reset-password", p.UserID), nil)
	var out struct {
		Password string `json:"password"`
	}
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.Password == "" {
		authFailed(c, "重置登录密码", status, raw, err)
		return
	}
	log.Printf(`{"event":"portal_reset_password","mchNo":%q,"username":%q,"by":%q}`, mch.MchNo, p.Username, claims.Username)
	c.JSON(http.StatusOK, gin.H{"mchNo": mch.MchNo, "mchName": mch.MchName, "username": p.Username, "password": out.Password})
}

// handleResetPortalAppSecret POST /admin/merchants/:mchNo/portal/app-secret：
// 换发开放平台 appSecret，旧的立即失效（已签出的 access token 最长还能用 1 小时）。
func handleResetPortalAppSecret(c *gin.Context) {
	claims, ok := requirePortalAdmin(c)
	if !ok {
		return
	}
	mch, p, ok := portalForAdmin(c, claims)
	if !ok {
		return
	}
	if p.OpenAppKey == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "该商户的 appKey 还没签发，先点「开通商户登录」补完"})
		return
	}
	status, raw, err := authAdmin(c.Request.Context(), bearerToken(c), http.MethodPost,
		"/internal/auth/apps/"+url.PathEscape(p.OpenAppKey)+"/rotate", nil)
	var out struct {
		AppSecret string `json:"appSecret"`
	}
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.AppSecret == "" {
		authFailed(c, "换发 appSecret", status, raw, err)
		return
	}
	log.Printf(`{"event":"portal_reset_app_secret","mchNo":%q,"appKey":%q,"by":%q}`, mch.MchNo, p.OpenAppKey, claims.Username)
	c.JSON(http.StatusOK, gin.H{"mchNo": mch.MchNo, "mchName": mch.MchName, "username": p.Username,
		"appKey": p.OpenAppKey, "appSecret": out.AppSecret})
}

// handleSetPortalState PUT /admin/merchants/:mchNo/portal/state {state}：
// 停用后该账号进不了商户中心；不影响商户系统用 appKey + 签名调开放 API（那个由商户 / 应用的启停控制）。
func handleSetPortalState(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in struct {
		State string `json:"state"`
	}
	if c.ShouldBindJSON(&in) != nil || (in.State != stateActive && in.State != stateDisabled) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state 只能是 active 或 disabled"})
		return
	}
	mch, p, ok := portalForAdmin(c, claims)
	if !ok {
		return
	}
	if _, err := db.Exec(c.Request.Context(), `UPDATE pay_merchant_portal SET state=$2, updated_at=now() WHERE mch_no=$1`,
		mch.MchNo, in.State); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	log.Printf(`{"event":"portal_state","mchNo":%q,"username":%q,"state":%q,"by":%q}`, mch.MchNo, p.Username, in.State, claims.Username)
	c.JSON(http.StatusOK, gin.H{"mchNo": mch.MchNo, "state": in.State})
}

// ── 商户侧 ──────────────────────────────────────────────────

func registerPortalRoutes(g *gin.RouterGroup) {
	g.GET("/me", handlePortalMe)
	g.GET("/apps", handlePortalApps)
	g.POST("/apps/:appId/secret", handlePortalRotateSecret)
	g.GET("/orders", handlePortalOrders)
	g.POST("/playground", handlePlayground)
}

// portalCaller 解析“这次请求代表哪个商户”。失败已写响应。
//
//   - 不带 mchNo：调用者必须是绑定了商户的门户用户，范围就是它绑定的那个商户。
//   - 带 ?mchNo=：运营替商户排查问题用，走 operator 角色 + 租户校验。
//     门户用户带了别人的 mchNo 会在 operator 这一关被 403，越不了权。
func portalCaller(c *gin.Context) (Merchant, portalRow, bool) {
	claims := claimsOf(c)
	if claims.TokenType != "access" {
		c.JSON(http.StatusForbidden, gin.H{"error": "需要用户身份"})
		return Merchant{}, portalRow{}, false
	}
	ctx := c.Request.Context()
	if mchNo := c.Query("mchNo"); mchNo != "" {
		if _, ok := requireOperator(c); !ok {
			return Merchant{}, portalRow{}, false
		}
		mch, ok := ownedMerchant(c, claims, mchNo)
		if !ok {
			return Merchant{}, portalRow{}, false
		}
		p, err := loadPortalByMch(ctx, mchNo)
		if err != nil && !notFound(err) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return Merchant{}, portalRow{}, false
		}
		return mch, p, true
	}
	p, err := loadPortalByUser(ctx, claims.UserId)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "当前账号没有绑定商户", "code": "no_portal"})
		return Merchant{}, portalRow{}, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return Merchant{}, portalRow{}, false
	}
	if p.State != stateActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "商户登录已被停用，请联系平台", "code": "portal_disabled"})
		return Merchant{}, portalRow{}, false
	}
	mch, err := loadMerchant(ctx, p.MchNo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return Merchant{}, portalRow{}, false
	}
	return mch, p, true
}

// portalMerchantView 商户能看到的自家资料：不含服务商、代理商归属与租户（那是平台与服务商之间的事）。
type portalMerchantView struct {
	MchNo        string `json:"mchNo"`
	MchName      string `json:"mchName"`
	MchShortName string `json:"mchShortName"`
	MchType      int    `json:"mchType"`
	ContactName  string `json:"contactName"`
	ContactPhone string `json:"contactPhone"`
	State        string `json:"state"`
}

func sandboxEnabled() bool {
	_, err := channel.Get(sandbox.IfCode)
	return err == nil
}

// publicBase 开放 API 的对外地址（文档与 curl 示例用）：优先 PAY_PUBLIC_BASE，否则按当前请求推断。
func publicBase(c *gin.Context) string {
	if base := strings.TrimRight(os.Getenv("PAY_PUBLIC_BASE"), "/"); base != "" {
		return base
	}
	scheme := c.GetHeader("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
	}
	return scheme + "://" + c.Request.Host
}

// handlePortalMe GET /portal/me：当前商户、开放平台 appKey、渠道子商户号。
func handlePortalMe(c *gin.Context) {
	mch, p, ok := portalCaller(c)
	if !ok {
		return
	}
	subs, err := merchantSubAccounts(c.Request.Context(), []string{mch.MchNo})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	mode := "merchant"
	if c.Query("mchNo") != "" {
		mode = "operator"
	}
	list := subs[mch.MchNo]
	if list == nil {
		list = []subAccount{}
	}
	c.JSON(http.StatusOK, gin.H{
		"mode": mode,
		"merchant": portalMerchantView{MchNo: mch.MchNo, MchName: mch.MchName, MchShortName: mch.MchShortName,
			MchType: mch.MchType, ContactName: mch.ContactName, ContactPhone: mch.ContactPhone, State: mch.State},
		"username":    p.Username,
		"openAppKey":  p.OpenAppKey,
		"scopes":      portalScopes,
		"subAccounts": list,
		"sandbox":     sandboxEnabled(),
		"apiBase":     publicBase(c),
	})
}

// handlePortalApps GET /portal/apps：本商户的应用与支付通道（不含密钥）。
func handlePortalApps(c *gin.Context) {
	mch, _, ok := portalCaller(c)
	if !ok {
		return
	}
	items, err := loadAppItems(c.Request.Context(), `mch_no=$1`, mch.MchNo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// portalApp 取应用并确认它是这个商户的；别家的应用一律当不存在。失败已写响应。
func portalApp(c *gin.Context, mch Merchant, appID string) (App, bool) {
	app, err := loadApp(c.Request.Context(), appID)
	if notFound(err) || (err == nil && app.MchNo != mch.MchNo) {
		c.JSON(http.StatusNotFound, gin.H{"error": "应用不存在"})
		return App{}, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return App{}, false
	}
	return app, true
}

// handlePortalRotateSecret POST /portal/apps/:appId/secret：商户自助换发签名密钥，旧密钥立即失效。
func handlePortalRotateSecret(c *gin.Context) {
	mch, _, ok := portalCaller(c)
	if !ok {
		return
	}
	app, ok := portalApp(c, mch, c.Param("appId"))
	if !ok {
		return
	}
	secret, err := rotateAppSecret(c.Request.Context(), app.AppID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "换发失败"})
		return
	}
	log.Printf(`{"event":"portal_rotate_secret","mchNo":%q,"appId":%q,"by":%q}`, mch.MchNo, app.AppID, claimsOf(c).Username)
	c.JSON(http.StatusOK, gin.H{"appId": app.AppID, "appSecret": secret, "note": "密钥仅显示一次"})
}

// handlePortalOrders GET /portal/orders：本商户最近 100 笔订单。
// 用开放 API 同款的 Order 视图：不带代理商分润（那是服务商与代理商之间的账）。
func handlePortalOrders(c *gin.Context) {
	mch, _, ok := portalCaller(c)
	if !ok {
		return
	}
	rows, err := db.Query(c.Request.Context(), `SELECT `+orderCols+` FROM pay_order
		WHERE mch_no=$1 ORDER BY created_at DESC LIMIT 100`, mch.MchNo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []Order{}
	for rows.Next() {
		if o, serr := scanOrder(rows); serr == nil {
			items = append(items, o)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
