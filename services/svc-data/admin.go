// svc-data 管理面：数据集与抽取规则的运行时编辑（admin JWT），配置即数据、改完即生效。
package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func requireAdmin(c *gin.Context) (*Claims, error) {
	claims, err := identity(c)
	if err != nil {
		return nil, err
	}
	if claims.Role != "admin" {
		return nil, errors.New("admin role required")
	}
	return claims, nil
}

type datasetInput struct {
	Name          string      `json:"name"`
	Source        string      `json:"source"`
	PKField       string      `json:"pkField"`
	Rules         []IndexRule `json:"indexRules"`
	Enabled       *bool       `json:"enabled"`
	RetentionDays *int        `json:"retentionDays"`
	NotifyURL     *string     `json:"notifyUrl"`
	NotifySecret  *string     `json:"notifySecret"` // 空串表示不改；显式新值才轮换
}

func registerAdminRoutes(r *gin.Engine) {
	g := r.Group(mount+"/admin", func(c *gin.Context) {
		if _, err := requireAdmin(c); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": err.Error()})
		}
	})

	g.GET("/datasets", func(c *gin.Context) {
		list, err := listDatasets(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"datasets": list})
	})

	g.POST("/datasets/:id", func(c *gin.Context) { // 建数据集，密钥自动生成、只在响应中回显
		var in datasetInput
		if c.ShouldBindJSON(&in) != nil || c.Param("id") == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad input"})
			return
		}
		if err := validateRules(in.Rules); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if in.PKField == "" {
			in.PKField = "id"
		}
		secret := newSecret()
		retention := 0
		if in.RetentionDays != nil && *in.RetentionDays >= 0 {
			retention = *in.RetentionDays
		}
		notifyURL := ""
		if in.NotifyURL != nil {
			notifyURL = *in.NotifyURL
		}
		tag, err := db.Exec(c.Request.Context(), `
			INSERT INTO dataset (id, name, source, webhook_secret, pk_field, index_rules, retention_days, notify_url)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO NOTHING`,
			c.Param("id"), in.Name, in.Source, sealSecret(secret), in.PKField, mustJSON(in.Rules),
			retention, notifyURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "insert failed"})
			return
		}
		if tag.RowsAffected() == 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "dataset exists"})
			return
		}
		writeAudit(c.Request.Context(), c.Param("id"), actorName(c), "create",
			gin.H{"rules": len(in.Rules)})
		c.JSON(http.StatusOK, gin.H{"id": c.Param("id"), "webhookSecret": secret,
			"note": "密钥仅此一次明文显示；遗失请轮换"})
	})

	g.PUT("/datasets/:id", func(c *gin.Context) { // 编辑规则/字段/启停——不碰已入库数据
		var in datasetInput
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad input"})
			return
		}
		if err := validateRules(in.Rules); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ds, err := loadDataset(c.Request.Context(), c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		if in.PKField == "" {
			in.PKField = ds.PKField
		}
		enabled := ds.Enabled
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		retention := ds.RetentionDays
		if in.RetentionDays != nil && *in.RetentionDays >= 0 {
			retention = *in.RetentionDays
		}
		notifyURL := ds.NotifyURL
		if in.NotifyURL != nil {
			notifyURL = *in.NotifyURL
		}
		notifySecret := ds.NotifySecret
		if in.NotifySecret != nil && *in.NotifySecret != "" {
			notifySecret = *in.NotifySecret
		}
		if _, err := db.Exec(c.Request.Context(), `
			UPDATE dataset SET name=$2, source=$3, pk_field=$4, index_rules=$5, enabled=$6,
				retention_days=$7, notify_url=$8, notify_secret=$9, updated_at=now()
			WHERE id=$1`,
			ds.ID, in.Name, in.Source, in.PKField, mustJSON(in.Rules), enabled,
			retention, notifyURL, sealSecret(notifySecret)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
			return
		}
		writeAudit(c.Request.Context(), ds.ID, actorName(c), "update",
			gin.H{"rules": len(in.Rules), "enabled": enabled, "retentionDays": retention, "notify": notifyURL != ""})
		c.JSON(http.StatusOK, gin.H{"id": ds.ID, "hint": "规则已更新；如需让新规则作用于已入库数据请执行 reindex"})
	})

	g.POST("/datasets/:id/rotate-secret", func(c *gin.Context) {
		secret := newSecret()
		tag, err := db.Exec(c.Request.Context(),
			`UPDATE dataset SET webhook_secret=$2, updated_at=now() WHERE id=$1`, c.Param("id"), sealSecret(secret))
		if err != nil || tag.RowsAffected() == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		writeAudit(c.Request.Context(), c.Param("id"), actorName(c), "rotate-secret", gin.H{})
		c.JSON(http.StatusOK, gin.H{"id": c.Param("id"), "webhookSecret": secret, "note": "密钥仅此一次明文显示"})
	})

	g.POST("/datasets/:id/reindex", func(c *gin.Context) {
		ds, err := loadDataset(c.Request.Context(), c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		n, err := reindexDataset(c.Request.Context(), ds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "reindex failed"})
			return
		}
		writeAudit(c.Request.Context(), ds.ID, actorName(c), "reindex", gin.H{"records": n})
		c.JSON(http.StatusOK, gin.H{"id": ds.ID, "reindexed": n})
	})

	g.POST("/datasets/:id/purge", func(c *gin.Context) { // 按 retention_days 清理过期数据
		ds, err := loadDataset(c.Request.Context(), c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		n, err := purgeExpired(c.Request.Context(), ds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "purge failed"})
			return
		}
		writeAudit(c.Request.Context(), ds.ID, actorName(c), "purge",
			gin.H{"deleted": n, "retentionDays": ds.RetentionDays})
		c.JSON(http.StatusOK, gin.H{"id": ds.ID, "deleted": n})
	})

	g.GET("/datasets/:id/audit", func(c *gin.Context) {
		entries, err := listAudit(c.Request.Context(), c.Param("id"), 100)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"audit": entries})
	})

	g.POST("/datasets/:id/preview", func(c *gin.Context) { // 规则试跑：拿样例 payload 预览抽出的索引行
		var in struct {
			Rules   []IndexRule     `json:"indexRules"`
			Payload json.RawMessage `json:"payload"`
		}
		if c.ShouldBindJSON(&in) != nil || len(in.Payload) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "indexRules 与 payload 必填"})
			return
		}
		if err := validateRules(in.Rules); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": extractEntries(in.Rules, in.Payload)})
	})

	g.DELETE("/datasets/:id", func(c *gin.Context) { // 删配置；?purge=true 连数据一起删
		ctx := c.Request.Context()
		if c.Query("purge") == "true" {
			if _, err := db.Exec(ctx, `DELETE FROM record_index WHERE dataset=$1`, c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "purge failed"})
				return
			}
			if _, err := db.Exec(ctx, `DELETE FROM record WHERE dataset=$1`, c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "purge failed"})
				return
			}
		}
		tag, err := db.Exec(ctx, `DELETE FROM dataset WHERE id=$1`, c.Param("id"))
		if err != nil || tag.RowsAffected() == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
			return
		}
		writeAudit(ctx, c.Param("id"), actorName(c), "delete", gin.H{"purged": c.Query("purge") == "true"})
		c.JSON(http.StatusOK, gin.H{"id": c.Param("id"), "purged": c.Query("purge") == "true"})
	})
}

func actorName(c *gin.Context) string {
	claims, err := identity(c)
	if err != nil || claims.Username == "" {
		return "unknown"
	}
	return claims.Username
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("[]")
	}
	return b
}
