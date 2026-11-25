// 商户资料修改：台账字段随时可改；决定资金走向的字段（服务商归属、类型）在商户投入使用后锁定。
package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

// handleUpdateMerchant PUT /admin/merchants/:mchNo：只改请求里出现的字段。
//
//   - mchName / mchShortName / contactName / contactPhone：平台内部台账，不报送渠道，随时可改。
//   - state：active | disabled。停用后该商户名下应用的开放 API 请求立即被拒绝。
//   - isvNo / mchType：决定用谁的渠道凭据收款。商户已经有订单、未取消的进件或代理商归属时不能再换，
//     否则历史订单的退款、对账、分润会指向另一个服务商。
func handleUpdateMerchant(c *gin.Context) {
	claims, ok := requireOperator(c)
	if !ok {
		return
	}
	var in struct {
		MchName      *string `json:"mchName"`
		MchShortName *string `json:"mchShortName"`
		MchType      *int    `json:"mchType"`
		IsvNo        *string `json:"isvNo"`
		ContactName  *string `json:"contactName"`
		ContactPhone *string `json:"contactPhone"`
		State        *string `json:"state"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体必须是合法 JSON"})
		return
	}
	for _, p := range []*string{in.MchName, in.MchShortName, in.IsvNo, in.ContactName, in.ContactPhone} {
		if p != nil {
			*p = strings.TrimSpace(*p)
		}
	}
	if in.MchName != nil && *in.MchName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "商户名称不能为空"})
		return
	}
	if in.MchType != nil && *in.MchType != 1 && *in.MchType != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchType 只能是 1（普通）或 2（特约）"})
		return
	}
	if in.State != nil && *in.State != stateActive && *in.State != stateDisabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state 只能是 active 或 disabled"})
		return
	}

	ctx := c.Request.Context()
	tx, err := db.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 锁住商户行：两次并发修改不会各自基于旧值判断“能不能换服务商”。
	var mch Merchant
	err = tx.QueryRow(ctx, `SELECT `+merchantCols+` FROM pay_merchant WHERE mch_no=$1 FOR UPDATE`, c.Param("mchNo")).
		Scan(mch.scanDest()...)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "商户不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if denyOtherTenant(c, claims, mch.TenantID, "商户") {
		return
	}

	next := mch
	if in.MchName != nil {
		next.MchName = *in.MchName
	}
	if in.MchShortName != nil {
		next.MchShortName = *in.MchShortName
	}
	if in.MchType != nil {
		next.MchType = *in.MchType
	}
	if in.IsvNo != nil {
		next.IsvNo = *in.IsvNo
	}
	if in.ContactName != nil {
		next.ContactName = *in.ContactName
	}
	if in.ContactPhone != nil {
		next.ContactPhone = *in.ContactPhone
	}
	if in.State != nil {
		next.State = *in.State
	}

	if next.IsvNo != mch.IsvNo || next.MchType != mch.MchType {
		if next.IsvNo != "" && next.IsvNo != mch.IsvNo {
			var isvTenant int
			err := tx.QueryRow(ctx, `SELECT tenant_id FROM pay_isv WHERE isv_no=$1`, next.IsvNo).Scan(&isvTenant)
			if notFound(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "服务商不存在"})
				return
			}
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
				return
			}
			// 与新建商户同一条规则：只能挂本租户或平台（租户 0）的服务商。
			if !isvUsable(isvTenant, mch.TenantID) {
				c.JSON(http.StatusForbidden, gin.H{"error": "服务商不属于商户所在租户"})
				return
			}
		}
		var orders, applyments int
		if err := tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM pay_order WHERE mch_no=$1),
			(SELECT count(*) FROM pay_applyment WHERE mch_no=$1 AND state<>$2)`,
			mch.MchNo, channel.ApplymentCanceled).Scan(&orders, &applyments); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}
		var blockers []string
		if orders > 0 {
			blockers = append(blockers, "已有订单")
		}
		if applyments > 0 {
			blockers = append(blockers, "有未取消的进件申请")
		}
		if mch.AgentNo != "" {
			blockers = append(blockers, "已归属代理商 "+mch.AgentNo)
		}
		if len(blockers) > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "该商户" + strings.Join(blockers, "、") + "，不能再更换服务商或类型；名称、联系人和启停不受影响"})
			return
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE pay_merchant SET mch_name=$2, mch_short_name=$3, mch_type=$4, isv_no=$5,
		contact_name=$6, contact_phone=$7, state=$8, updated_at=now() WHERE mch_no=$1`,
		mch.MchNo, next.MchName, next.MchShortName, next.MchType, next.IsvNo, next.ContactName, next.ContactPhone, next.State); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	if next.IsvNo != mch.IsvNo || next.MchType != mch.MchType || next.State != mch.State {
		log.Printf(`{"event":"merchant_update","mchNo":%q,"isvNo":[%q,%q],"mchType":[%d,%d],"state":[%q,%q],"by":%q}`,
			mch.MchNo, mch.IsvNo, next.IsvNo, mch.MchType, next.MchType, mch.State, next.State, claims.Username)
	}
	c.JSON(http.StatusOK, gin.H{"merchant": next})
}
