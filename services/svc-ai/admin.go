// 管理面（admin 角色）：providers / model_aliases / ai_keys CRUD + 用量汇总。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效 id"})
		return 0, false
	}
	return id, true
}

func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

// ── providers ───────────────────────────────────────

func listProviders(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := db.Query(c.Request.Context(),
			`SELECT id, name, kind, base_url, api_key, enabled, created_at FROM providers ORDER BY id`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		type prov struct {
			ID        int64     `json:"id"`
			Name      string    `json:"name"`
			Kind      string    `json:"kind"`
			BaseURL   string    `json:"baseUrl"`
			KeyMasked string    `json:"keyMasked"`
			Enabled   bool      `json:"enabled"`
			CreatedAt time.Time `json:"createdAt"`
		}
		out := []prov{}
		for rows.Next() {
			var p prov
			var key string
			if rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &key, &p.Enabled, &p.CreatedAt) == nil {
				p.KeyMasked = maskKey(key)
				out = append(out, p)
			}
		}
		c.JSON(http.StatusOK, gin.H{"providers": out})
	}
}

func createProvider(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Name    string `json:"name" binding:"required"`
			Kind    string `json:"kind"`
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey" binding:"required"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if in.Kind == "" {
			in.Kind = "anthropic"
		}
		if in.Kind != "anthropic" && in.Kind != "openai" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "kind 仅支持 anthropic / openai"})
			return
		}
		if in.BaseURL == "" {
			if in.Kind == "openai" {
				in.BaseURL = "https://api.openai.com/v1"
			} else {
				in.BaseURL = "https://api.anthropic.com"
			}
		}
		var id int64
		if err := db.QueryRow(c.Request.Context(), `INSERT INTO providers (name, kind, base_url, api_key)
			VALUES ($1,$2,$3,$4) RETURNING id`, in.Name, in.Kind, in.BaseURL, in.APIKey).Scan(&id); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id})
	}
}

func updateProvider(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		var in struct {
			BaseURL *string `json:"baseUrl"`
			APIKey  *string `json:"apiKey"`
			Enabled *bool   `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ctx := c.Request.Context()
		if in.BaseURL != nil {
			_, _ = db.Exec(ctx, `UPDATE providers SET base_url=$1 WHERE id=$2`, *in.BaseURL, id)
		}
		if in.APIKey != nil && *in.APIKey != "" {
			_, _ = db.Exec(ctx, `UPDATE providers SET api_key=$1 WHERE id=$2`, *in.APIKey, id)
		}
		if in.Enabled != nil {
			_, _ = db.Exec(ctx, `UPDATE providers SET enabled=$1 WHERE id=$2`, *in.Enabled, id)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func deleteProvider(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		if _, err := db.Exec(c.Request.Context(), `DELETE FROM providers WHERE id=$1`, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// ── model aliases ───────────────────────────────────

func listAliases(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := db.Query(c.Request.Context(), `SELECT a.id, a.alias, a.provider_id, p.name,
			a.model, a.enabled FROM model_aliases a JOIN providers p ON p.id=a.provider_id ORDER BY a.id`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		type alias struct {
			ID           int64  `json:"id"`
			Alias        string `json:"alias"`
			ProviderID   int64  `json:"providerId"`
			ProviderName string `json:"providerName"`
			Model        string `json:"model"`
			Enabled      bool   `json:"enabled"`
		}
		out := []alias{}
		for rows.Next() {
			var a alias
			if rows.Scan(&a.ID, &a.Alias, &a.ProviderID, &a.ProviderName, &a.Model, &a.Enabled) == nil {
				out = append(out, a)
			}
		}
		c.JSON(http.StatusOK, gin.H{"aliases": out})
	}
}

func createAlias(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Alias      string `json:"alias" binding:"required"`
			ProviderID int64  `json:"providerId" binding:"required"`
			Model      string `json:"model" binding:"required"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		var id int64
		if err := db.QueryRow(c.Request.Context(), `INSERT INTO model_aliases (alias, provider_id, model)
			VALUES ($1,$2,$3) RETURNING id`, in.Alias, in.ProviderID, in.Model).Scan(&id); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id})
	}
}

func deleteAlias(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		if _, err := db.Exec(c.Request.Context(), `DELETE FROM model_aliases WHERE id=$1`, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// ── ai keys ─────────────────────────────────────────

func listKeys(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := db.Query(c.Request.Context(),
			`SELECT id, name, enabled, created_at, last_used_at FROM ai_keys ORDER BY id`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		type key struct {
			ID         int64      `json:"id"`
			Name       string     `json:"name"`
			Enabled    bool       `json:"enabled"`
			CreatedAt  time.Time  `json:"createdAt"`
			LastUsedAt *time.Time `json:"lastUsedAt"`
		}
		out := []key{}
		for rows.Next() {
			var k key
			if rows.Scan(&k.ID, &k.Name, &k.Enabled, &k.CreatedAt, &k.LastUsedAt) == nil {
				out = append(out, k)
			}
		}
		c.JSON(http.StatusOK, gin.H{"keys": out})
	}
}

// createKey：生成 pfk- 虚拟 key，明文仅本次响应返回。
func createKey(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Name string `json:"name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		plain := "pfk-" + hex.EncodeToString(raw)
		var id int64
		if err := db.QueryRow(c.Request.Context(), `INSERT INTO ai_keys (name, key_hash)
			VALUES ($1,$2) RETURNING id`, in.Name, hashKey(plain)).Scan(&id); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "key": plain,
			"notice": "明文 key 仅此一次返回，请立即保存"})
	}
}

func updateKey(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&in); err != nil || in.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "enabled required"})
			return
		}
		if _, err := db.Exec(c.Request.Context(),
			`UPDATE ai_keys SET enabled=$1 WHERE id=$2`, *in.Enabled, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func deleteKey(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		if _, err := db.Exec(c.Request.Context(), `DELETE FROM ai_keys WHERE id=$1`, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// ── usage ───────────────────────────────────────────

func usageSummary(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
		if days <= 0 || days > 90 {
			days = 7
		}
		ctx := c.Request.Context()
		interval := strconv.Itoa(days) + " days"

		type agg struct {
			Key          string `json:"key"`
			Calls        int    `json:"calls"`
			InputTokens  int64  `json:"inputTokens"`
			OutputTokens int64  `json:"outputTokens"`
			Errors       int    `json:"errors"`
		}
		byKey := []agg{}
		rows, err := db.Query(ctx, `SELECT key_name, count(*), coalesce(sum(input_tokens),0),
				coalesce(sum(output_tokens),0), count(*) FILTER (WHERE status <> 200)
			FROM usage WHERE created_at > now() - $1::interval GROUP BY key_name ORDER BY count(*) DESC`, interval)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for rows.Next() {
			var a agg
			if rows.Scan(&a.Key, &a.Calls, &a.InputTokens, &a.OutputTokens, &a.Errors) == nil {
				byKey = append(byKey, a)
			}
		}
		rows.Close()

		type modelAgg struct {
			Model        string `json:"model"`
			Calls        int    `json:"calls"`
			InputTokens  int64  `json:"inputTokens"`
			OutputTokens int64  `json:"outputTokens"`
		}
		byModel := []modelAgg{}
		mrows, err := db.Query(ctx, `SELECT model, count(*), coalesce(sum(input_tokens),0),
				coalesce(sum(output_tokens),0)
			FROM usage WHERE created_at > now() - $1::interval GROUP BY model ORDER BY count(*) DESC`, interval)
		if err == nil {
			for mrows.Next() {
				var a modelAgg
				if mrows.Scan(&a.Model, &a.Calls, &a.InputTokens, &a.OutputTokens) == nil {
					byModel = append(byModel, a)
				}
			}
			mrows.Close()
		}

		type row struct {
			KeyName      string    `json:"keyName"`
			Alias        string    `json:"alias"`
			Provider     string    `json:"provider"`
			Model        string    `json:"model"`
			InputTokens  int       `json:"inputTokens"`
			OutputTokens int       `json:"outputTokens"`
			Status       int       `json:"status"`
			LatencyMs    int       `json:"latencyMs"`
			CreatedAt    time.Time `json:"createdAt"`
		}
		recent := []row{}
		rrows, err := db.Query(ctx, `SELECT key_name, alias, provider, model, input_tokens,
				output_tokens, status, latency_ms, created_at
			FROM usage ORDER BY created_at DESC LIMIT 50`)
		if err == nil {
			for rrows.Next() {
				var r row
				if rrows.Scan(&r.KeyName, &r.Alias, &r.Provider, &r.Model, &r.InputTokens,
					&r.OutputTokens, &r.Status, &r.LatencyMs, &r.CreatedAt) == nil {
					recent = append(recent, r)
				}
			}
			rrows.Close()
		}
		c.JSON(http.StatusOK, gin.H{"byKey": byKey, "byModel": byModel, "recent": recent})
	}
}
