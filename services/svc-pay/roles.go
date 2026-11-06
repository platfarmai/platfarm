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
	if e, ok := roleCache[key]; ok && time.Now().Before(e.exp) {
		roleMu.Unlock()
		return e.ok, nil
	}
	roleMu.Unlock()

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
	roleCache[key] = roleCacheEntry{ok: has, exp: time.Now().Add(roleCacheTTL)}
	roleMu.Unlock()
	return has, nil
}
