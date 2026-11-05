// 管理接口：平台 admin 或持有 operator 角色的用户。M1 只做配置与只读查询。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

func requireOperator(c *gin.Context) (*Claims, bool) {
	claims := claimsOf(c)
	if claims.Role == "admin" {
		return claims, true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "需要平台管理员"})
	return nil, false
}

// ── 服务商 ──────────────────────────────────────────────────

func handleCreateISV(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in struct {
		IsvNo    string `json:"isvNo"`
		IsvName  string `json:"isvName"`
		FundMode string `json:"fundMode"`
	}
	if c.ShouldBindJSON(&in) != nil || in.IsvNo == "" || in.IsvName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "isvNo 与 isvName 必填"})
		return
	}
	if in.FundMode == "" {
		in.FundMode = "direct"
	}
	if in.FundMode != "direct" && in.FundMode != "controlled" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fundMode 只能是 direct 或 controlled"})
		return
	}
	_, err := db.Exec(c.Request.Context(), `INSERT INTO pay_isv (isv_no, isv_name, fund_mode, tenant_id)
		VALUES ($1,$2,$3,$4)`, in.IsvNo, in.IsvName, in.FundMode, claims.TenantId)
	if isUniqueViolation(err) {
		c.JSON(http.StatusConflict, gin.H{"error": "服务商号已存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"isvNo": in.IsvNo, "fundMode": in.FundMode})
}

// ── 商户 ────────────────────────────────────────────────────

func handleCreateMerchant(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in struct {
		MchNo         string `json:"mchNo"`
		MchName       string `json:"mchName"`
		MchShortName  string `json:"mchShortName"`
		MchType       int    `json:"mchType"`
		IsvNo         string `json:"isvNo"`
		ContactName   string `json:"contactName"`
		ContactPhone  string `json:"contactPhone"`
	}
	if c.ShouldBindJSON(&in) != nil || in.MchName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchName 必填"})
		return
	}
	if in.MchNo == "" {
		in.MchNo = newOrderID("M")
	}
	if in.MchType == 0 {
		in.MchType = 2
	}
	tenant := claims.TenantId
	_, err := db.Exec(c.Request.Context(), `INSERT INTO pay_merchant
		(mch_no, mch_name, mch_short_name, mch_type, isv_no, contact_name, contact_phone, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		in.MchNo, in.MchName, in.MchShortName, in.MchType, in.IsvNo, in.ContactName, in.ContactPhone, tenant)
	if isUniqueViolation(err) {
		c.JSON(http.StatusConflict, gin.H{"error": "商户号已存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mchNo": in.MchNo})
}

func handleListMerchants(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	rows, err := db.Query(c.Request.Context(), `SELECT mch_no, mch_name, mch_short_name, mch_type, isv_no, state, tenant_id
		FROM pay_merchant WHERE (tenant_id=$1 OR $1=0) ORDER BY created_at DESC LIMIT 200`, claims.TenantId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []Merchant{}
	for rows.Next() {
		var m Merchant
		if rows.Scan(&m.MchNo, &m.MchName, &m.MchShortName, &m.MchType, &m.IsvNo, &m.State, &m.TenantID) == nil {
			items = append(items, m)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// ── 应用 ────────────────────────────────────────────────────

func handleCreateApp(c *gin.Context) {
	if _, ok := requireOperator(c); !ok {
		return
	}
	var in struct {
		AppName string `json:"appName"`
		MchNo   string `json:"mchNo"`
	}
	if c.ShouldBindJSON(&in) != nil || in.MchNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchNo 必填"})
		return
	}
	mch, err := loadMerchant(c.Request.Context(), in.MchNo)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "商户不存在"})
		return
	}
	secret := newSecret()
	// 签名校验需要原文，故密封而非哈希；库泄露本身还原不了明文。
	sealed, err := seal(secret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成密钥失败"})
		return
	}
	appID := newOrderID("A")
	_, err = db.Exec(c.Request.Context(), `INSERT INTO pay_app (app_id, app_name, mch_no, app_secret, tenant_id)
		VALUES ($1,$2,$3,$4,$5)`, appID, in.AppName, in.MchNo, sealed, mch.TenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	// 明文密钥只此一次。
	c.JSON(http.StatusOK, gin.H{"appId": appID, "appSecret": secret, "note": "密钥仅显示一次"})
}

// ── 渠道参数与路由 ──────────────────────────────────────────

func handlePutChannelConfig(c *gin.Context) {
	if _, ok := requireOperator(c); !ok {
		return
	}
	var in struct {
		OwnerType string            `json:"ownerType"`
		OwnerID   string            `json:"ownerId"`
		IfCode    string            `json:"ifCode"`
		Params    map[string]string `json:"params"`
	}
	if c.ShouldBindJSON(&in) != nil || in.OwnerID == "" || in.IfCode == "" || len(in.Params) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ownerId、ifCode、params 必填"})
		return
	}
	if in.OwnerType != "isv" && in.OwnerType != "merchant" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ownerType 只能是 isv 或 merchant"})
		return
	}
	if channel.Ways(in.IfCode) == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知渠道 " + in.IfCode})
		return
	}
	sealed, err := mustSealParams(in.Params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加密失败"})
		return
	}
	_, err = db.Exec(c.Request.Context(), `INSERT INTO pay_channel_config (owner_type, owner_id, if_code, if_params)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (owner_type, owner_id, if_code) DO UPDATE SET if_params=$4, updated_at=now()`,
		in.OwnerType, in.OwnerID, in.IfCode, sealed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handlePutPassage(c *gin.Context) {
	if _, ok := requireOperator(c); !ok {
		return
	}
	var in struct {
		AppID   string `json:"appId"`
		IfCode  string `json:"ifCode"`
		WayCode string `json:"wayCode"`
		RateBps int    `json:"rateBps"`
	}
	if c.ShouldBindJSON(&in) != nil || in.AppID == "" || in.IfCode == "" || in.WayCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "appId、ifCode、wayCode 必填"})
		return
	}
	supported := false
	for _, w := range channel.Ways(in.IfCode) {
		if w == in.WayCode {
			supported = true
		}
	}
	if !supported {
		c.JSON(http.StatusBadRequest, gin.H{"error": "渠道不支持该支付方式"})
		return
	}
	app, err := loadApp(c.Request.Context(), in.AppID)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "应用不存在"})
		return
	}
	_, err = db.Exec(c.Request.Context(), `INSERT INTO pay_passage (app_id, mch_no, if_code, way_code, rate_bps, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (app_id, way_code) DO UPDATE SET if_code=$3, rate_bps=$5, state='active', updated_at=now()`,
		in.AppID, app.MchNo, in.IfCode, in.WayCode, in.RateBps, app.TenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ── 只读查询 ────────────────────────────────────────────────

func handleListOrders(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	rows, err := db.Query(c.Request.Context(), `SELECT `+orderCols+` FROM pay_order
		WHERE (tenant_id=$1 OR $1=0) ORDER BY created_at DESC LIMIT 100`, claims.TenantId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []Order{}
	for rows.Next() {
		o, serr := scanOrder(rows)
		if serr == nil {
			items = append(items, o)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func newSecret() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// 避免 json 导入在重构时丢失（响应结构都走 gin.H）。
var _ = json.Marshal
