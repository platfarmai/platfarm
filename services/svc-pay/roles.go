// 业务角色闸门（specs/027）。平台 admin 直通；其他用户须在 svc-grants 持有本服务的角色
// （manifest roles.vocabulary：operator / reviewer）。查询用调用者自己的 token 打 svc-grants /mine，
// 不需要服务 token，也不需要 svc-grants 的 accept 名单。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	roleOperator = "operator"
	roleReviewer = "reviewer"
	roleCacheTTL = 30 * time.Second
)

// roleChecker 可被测试替换。
var roleChecker = grantsHasRole

// 租户隔离：只有平台管理员（role=admin）跨租户；其他用户只能触达本租户的数据。
// 租户 0 是平台自己的租户（auth 里也是“未分配”），不是“看全部”。
func isPlatformAdmin(c *Claims) bool { return c.Role == "admin" }

func inTenant(c *Claims, tenantID int) bool { return isPlatformAdmin(c) || c.TenantId == tenantID }

// denyOtherTenant 对象不属于调用者租户时写 403 并返回 true。
func denyOtherTenant(c *gin.Context, claims *Claims, tenantID int, what string) bool {
	if inTenant(claims, tenantID) {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{"error": what + "不属于当前租户"})
	return true
}

// tenantScope 列表查询的租户条件，占用 $1、$2；后续参数从 $3 起编号。
func tenantScope(c *Claims) (string, []any) {
	return `($2::bool OR tenant_id=$1)`, []any{c.TenantId, isPlatformAdmin(c)}
}

// isvUsable 商户可以挂在本租户或平台（租户 0）的服务商下：平台作为服务商为各租户进件、收款。
func isvUsable(isvTenant, mchTenant int) bool { return isvTenant == mchTenant || isvTenant == 0 }

// ownerTenant 渠道参数归属（服务商或商户）的租户。
func ownerTenant(ctx context.Context, ownerType, ownerID string) (int, error) {
	if ownerType == "merchant" {
		m, err := loadMerchant(ctx, ownerID)
		return m.TenantID, err
	}
	var t int
	err := db.QueryRow(ctx, `SELECT tenant_id FROM pay_isv WHERE isv_no=$1`, ownerID).Scan(&t)
	return t, err
}

type roleCacheEntry struct {
	ok  bool
	exp time.Time
}

var (
	roleMu    sync.Mutex
	roleCache = map[string]roleCacheEntry{}
)

// requireRole admin 直通；否则查 svc-grants。失败已写响应。
func requireRole(c *gin.Context, role string) (*Claims, bool) {
	claims := claimsOf(c)
	if claims.Role == "admin" {
		return claims, true
	}
	if claims.TokenType != "access" {
		c.JSON(http.StatusForbidden, gin.H{"error": "需要用户身份"})
		return nil, false
	}
	ok, err := roleChecker(c.Request.Context(), c.GetHeader("Authorization"), claims.UserId, role)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "角色服务不可用: " + err.Error()})
		return nil, false
	}
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "需要 " + role + " 角色"})
		return nil, false
	}
	return claims, true
}

func grantsHasRole(ctx context.Context, bearer string, userID int, role string) (bool, error) {
	key := fmt.Sprintf("%d:%s", userID, role)
	roleMu.Lock()
	e, cached := roleCache[key]
	roleMu.Unlock()
	// 只复用“没有该角色”的结果，挡住无权限请求打满 svc-grants。
	// “有该角色”不缓存：授权和撤销都在下一次请求生效，不靠过期的肯定结果。
	if cached && time.Now().Before(e.exp) && !e.ok {
		return false, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, envOr("GRANTS_URL", "http://svc-grants:8080")+"/api/grants/mine", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := svcHTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("svc-grants %d", resp.StatusCode)
	}
	var out struct {
		Items []struct {
			ServiceID string `json:"serviceId"`
			Role      string `json:"role"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, err
	}
	has := false
	for _, g := range out.Items {
		if g.ServiceID == "svc-pay" && g.Role == role {
			has = true
			break
		}
	}
	roleMu.Lock()
	if has {
		delete(roleCache, key)
	} else {
		roleCache[key] = roleCacheEntry{ok: false, exp: time.Now().Add(roleCacheTTL)}
	}
	roleMu.Unlock()
	return has, nil
}
