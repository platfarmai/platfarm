// 管理接口：应用、支付通道与渠道参数的查询和维护（管理台「应用·通道」页）。
// 渠道参数里的密钥类字段只回“已配置”，原文永不出库。
package main

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"platfarm/svc-pay/channel"
)

// handleListChannels GET /admin/channels：已注册渠道、支持的支付方式与参数字段。
func handleListChannels(c *gin.Context) {
	if _, ok := requireOperator(c); !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": channel.List()})
}

// ── 应用 ────────────────────────────────────────────────────

type passageItem struct {
	WayCode string `json:"wayCode"`
	IfCode  string `json:"ifCode"`
	RateBps int    `json:"rateBps"`
	State   string `json:"state"`
}

type appItem struct {
	AppID     string        `json:"appId"`
	AppName   string        `json:"appName"`
	MchNo     string        `json:"mchNo"`
	State     string        `json:"state"`
	TenantID  int           `json:"tenantId"`
	CreatedAt time.Time     `json:"createdAt"`
	Passages  []passageItem `json:"passages"`
}

// handleListApps GET /admin/apps[?mchNo=]：应用及其支付通道。不含密钥。
func handleListApps(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	scope, args := tenantScope(claims)
	if mchNo := c.Query("mchNo"); mchNo != "" {
		scope += ` AND mch_no=$3`
		args = append(args, mchNo)
	}
	items, err := loadAppItems(c.Request.Context(), scope, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// loadAppItems 按条件取应用及其支付通道（最多 200 个，新的在前）。不含密钥。
func loadAppItems(ctx context.Context, where string, args ...any) ([]*appItem, error) {
	rows, err := db.Query(ctx, `SELECT app_id, app_name, mch_no, state, tenant_id, created_at FROM pay_app
		WHERE `+where+` ORDER BY created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	items := []*appItem{}
	byID := map[string]*appItem{}
	ids := []string{}
	for rows.Next() {
		a := &appItem{Passages: []passageItem{}}
		if rows.Scan(&a.AppID, &a.AppName, &a.MchNo, &a.State, &a.TenantID, &a.CreatedAt) == nil {
			items = append(items, a)
			byID[a.AppID] = a
			ids = append(ids, a.AppID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return items, nil
	}
	prows, err := db.Query(ctx, `SELECT app_id, way_code, if_code, rate_bps, state FROM pay_passage
		WHERE app_id = ANY($1) ORDER BY way_code`, ids)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var appID string
		var p passageItem
		if prows.Scan(&appID, &p.WayCode, &p.IfCode, &p.RateBps, &p.State) == nil {
			byID[appID].Passages = append(byID[appID].Passages, p)
		}
	}
	return items, prows.Err()
}

// ownedApp 取应用并确认它属于调用者租户。失败已写响应。
func ownedApp(c *gin.Context, claims *Claims, appID string) (App, bool) {
	app, err := loadApp(c.Request.Context(), appID)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "应用不存在"})
		return App{}, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return App{}, false
	}
	if denyOtherTenant(c, claims, app.TenantID, "应用") {
		return App{}, false
	}
	return app, true
}

// handleRotateAppSecret POST /admin/apps/:appId/secret：换发签名密钥，旧密钥立即失效。
func handleRotateAppSecret(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	app, ok := ownedApp(c, claims, c.Param("appId"))
	if !ok {
		return
	}
	secret, err := rotateAppSecret(c.Request.Context(), app.AppID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "换发失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"appId": app.AppID, "appSecret": secret, "note": "密钥仅显示一次"})
}

// rotateAppSecret 给应用换一把签名密钥并返回明文（只此一次，库里只有密封后的值）。
func rotateAppSecret(ctx context.Context, appID string) (string, error) {
	secret := newSecret()
	sealed, err := seal(secret)
	if err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `UPDATE pay_app SET app_secret=$2, updated_at=now() WHERE app_id=$1`, appID, sealed); err != nil {
		return "", err
	}
	return secret, nil
}

// handleSetAppState PUT /admin/apps/:appId/state {state}：停用后该应用的开放 API 请求一律 401。
func handleSetAppState(c *gin.Context) {
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
	app, ok := ownedApp(c, claims, c.Param("appId"))
	if !ok {
		return
	}
	if _, err := db.Exec(c.Request.Context(), `UPDATE pay_app SET state=$2, updated_at=now() WHERE app_id=$1`,
		app.AppID, in.State); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"appId": app.AppID, "state": in.State})
}

// handleDisablePassage DELETE /admin/passages?appId=&wayCode=：停用一条支付通道
// （保留记录；再次 PUT /admin/passages 即重新启用）。已创建的订单不受影响。
func handleDisablePassage(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	appID, wayCode := c.Query("appId"), c.Query("wayCode")
	if appID == "" || wayCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "appId、wayCode 必填"})
		return
	}
	app, ok := ownedApp(c, claims, appID)
	if !ok {
		return
	}
	tag, err := db.Exec(c.Request.Context(), `UPDATE pay_passage SET state='disabled', updated_at=now()
		WHERE app_id=$1 AND way_code=$2`, app.AppID, wayCode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "该应用没有这条支付通道"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ── 渠道参数 ────────────────────────────────────────────────

type paramView struct {
	Key    string `json:"key"`
	Set    bool   `json:"set"`
	Secret bool   `json:"secret"`
	Value  string `json:"value"` // 仅非密钥字段回显
}

type channelConfigView struct {
	IfCode    string      `json:"ifCode"`
	State     string      `json:"state"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Fields    []paramView `json:"fields"`
}

// viewParams 把明文参数折成可回显的视图：渠道声明为非密钥的字段给原值，
// 其余（密钥字段，以及渠道没有声明的键）只说“已配置”。
func viewParams(ifCode, ownerType string, params map[string]string) []paramView {
	out := []paramView{}
	declared := map[string]bool{}
	for _, f := range channel.Fields(ifCode, ownerType) {
		declared[f.Key] = true
		v := paramView{Key: f.Key, Set: params[f.Key] != "", Secret: f.Secret}
		if !f.Secret {
			v.Value = params[f.Key]
		}
		out = append(out, v)
	}
	extra := []string{}
	for k, val := range params {
		if !declared[k] && val != "" {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, paramView{Key: k, Set: true, Secret: true})
	}
	return out
}

// handleGetChannelConfig GET /admin/channel-config?ownerType=&ownerId=：
// 某个服务商 / 商户已配置的渠道参数概况。
func handleGetChannelConfig(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	ownerType, ownerID := c.Query("ownerType"), c.Query("ownerId")
	if ownerID == "" || (ownerType != "isv" && ownerType != "merchant") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ownerType 只能是 isv 或 merchant，ownerId 必填"})
		return
	}
	ctx := c.Request.Context()
	tenant, err := ownerTenant(ctx, ownerType, ownerID)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "参数归属不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if denyOtherTenant(c, claims, tenant, "参数归属") {
		return
	}
	rows, err := db.Query(ctx, `SELECT if_code, if_params, state, updated_at FROM pay_channel_config
		WHERE owner_type=$1 AND owner_id=$2 ORDER BY if_code`, ownerType, ownerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []channelConfigView{}
	for rows.Next() {
		var v channelConfigView
		var sealed string
		if rows.Scan(&v.IfCode, &sealed, &v.State, &v.UpdatedAt) != nil {
			continue
		}
		plain, err := open(sealed)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": v.IfCode + " 渠道参数解密失败（PAY_SECRET_KEY 是否变更过？）"})
			return
		}
		v.Fields = viewParams(v.IfCode, ownerType, parseParams(plain))
		items = append(items, v)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// mergeChannelParams 在已有参数上叠加 patch：非空值覆盖，空串删除该键，没出现的键保持不变。
// 行锁内读改写，避免与进件完成回写 sub_mchid、同步平台证书互相覆盖。
func mergeChannelParams(ctx context.Context, ownerType, ownerID, ifCode string, patch map[string]string, tenant int) error {
	_, err := inTx(ctx, func(tx pgx.Tx) (bool, error) {
		params := map[string]string{}
		var sealed string
		err := tx.QueryRow(ctx, `SELECT if_params FROM pay_channel_config
			WHERE owner_type=$1 AND owner_id=$2 AND if_code=$3 FOR UPDATE`, ownerType, ownerID, ifCode).Scan(&sealed)
		switch {
		case err == nil:
			plain, oerr := open(sealed)
			if oerr != nil {
				return false, oerr
			}
			params = parseParams(plain)
		case !notFound(err):
			return false, err
		}
		for k, v := range patch {
			if v == "" {
				delete(params, k)
			} else {
				params[k] = v
			}
		}
		next, err := mustSealParams(params)
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO pay_channel_config (owner_type, owner_id, if_code, if_params, tenant_id)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (owner_type, owner_id, if_code) DO UPDATE SET if_params=$4, tenant_id=$5, updated_at=now()`,
			ownerType, ownerID, ifCode, next, tenant)
		return err == nil, err
	})
	return err
}
