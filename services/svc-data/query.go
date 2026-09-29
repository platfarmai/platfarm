// svc-data 上游分发：通用筛选查询（specs/009 开放路由，app token + data.records.read）。
//
// GET /api/data/records?dataset=d4&f=affix.strength>=200&f=aspect=Affix_legendary_necro_126
// 筛选 DSL：f=<field>[.<key>][<op><value>]，op ∈ >= <= = > <；无 op 表示"存在该字段/键"。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// identityOrApp 放行 tokenType=app（校验 scope）；其余走模板六步约定。
func identityOrApp(c *gin.Context, scope string) (*Claims, error) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, errors.New("missing bearer token")
	}
	claims, err := decode(strings.TrimPrefix(h, "Bearer "))
	if err != nil {
		return nil, err
	}
	if claims.TokenType == "app" {
		for _, s := range claims.Scopes {
			if s == scope {
				return claims, nil
			}
		}
		return nil, errors.New("scope " + scope + " required")
	}
	return identity(c)
}

// appCanReadDataset 数据集级授权：scope 含 dataset:<id>:read 或通配 dataset:*:read 才放行。
// 无任何 dataset: 前缀 scope 时回退为允许（兼容只发了 data.records.read 的存量 app）。
func appCanReadDataset(claims *Claims, dataset string) bool {
	if claims.TokenType != "app" {
		return true
	}
	scoped := false
	for _, s := range claims.Scopes {
		if strings.HasPrefix(s, "dataset:") {
			scoped = true
			if s == "dataset:*:read" || s == "dataset:"+dataset+":read" {
				return true
			}
		}
	}
	return !scoped
}

type filterCond struct {
	field, key, op string
	value          string
}

var filterOps = []string{">=", "<=", "=", ">", "<"} // 长 op 优先匹配

func parseFilter(spec string) (filterCond, error) {
	f := filterCond{}
	rest := spec
	for _, op := range filterOps {
		if i := strings.Index(spec, op); i > 0 {
			f.op, f.value = op, spec[i+len(op):]
			rest = spec[:i]
			break
		}
	}
	if dot := strings.Index(rest, "."); dot > 0 {
		f.field, f.key = rest[:dot], rest[dot+1:]
	} else {
		f.field = rest
	}
	if f.field == "" || (f.op != "" && f.value == "") || strings.ContainsAny(f.field+f.key, "=<>") {
		return f, fmt.Errorf("bad filter %q", spec)
	}
	return f, nil
}

// condSQL 生成 EXISTS 子查询；数值可比较则同时匹配 num 列，否则匹配 txt。
func condSQL(f filterCond, args *[]any) string {
	n := func(v any) string {
		*args = append(*args, v)
		return fmt.Sprintf("$%d", len(*args))
	}
	sub := "EXISTS (SELECT 1 FROM record_index i WHERE i.dataset = r.dataset AND i.pk = r.pk AND i.field = " + n(f.field)
	if f.key != "" {
		sub += " AND i.k = " + n(f.key)
	}
	if f.op != "" {
		if num, err := strconv.ParseFloat(f.value, 64); err == nil {
			if f.op == "=" {
				sub += " AND (i.num = " + n(num) + " OR i.txt = " + n(f.value) + ")"
			} else {
				sub += " AND i.num " + f.op + " " + n(num)
			}
		} else {
			sub += " AND i.txt " + f.op + " " + n(f.value)
		}
	}
	return sub + ")"
}

// handleRecords 通用查询：原文 payload 原样返回，字段结构由数据集自己定义。
func handleRecords(c *gin.Context) {
	dsID := c.Query("dataset")
	if dsID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dataset required"})
		return
	}
	claims, err := identityOrApp(c, "data.records.read")
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if !appCanReadDataset(claims, dsID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "dataset not granted"})
		return
	}
	args := []any{dsID}
	conds := []string{"r.dataset = $1"}
	if pk := c.Query("pk"); pk != "" {
		args = append(args, pk)
		conds = append(conds, fmt.Sprintf("r.pk = $%d", len(args)))
	}
	if since := c.Query("updated_since"); since != "" { // RFC3339，增量拉取
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "updated_since 需 RFC3339"})
			return
		}
		args = append(args, t)
		conds = append(conds, fmt.Sprintf("r.updated_at > $%d", len(args)))
	}
	// f= 组内 AND；or= 为 OR 组（组间 AND）：f=a&f=b&or=c&or=d → a AND b AND (c OR d)
	for _, spec := range c.QueryArray("f") {
		f, err := parseFilter(spec)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		conds = append(conds, condSQL(f, &args))
	}
	if ors := c.QueryArray("or"); len(ors) > 0 {
		parts := make([]string, 0, len(ors))
		for _, spec := range ors {
			f, err := parseFilter(spec)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			parts = append(parts, condSQL(f, &args))
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}

	limit := 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}
	orderBy := "r.updated_at DESC, r.pk"
	if sort := c.Query("sort"); sort != "" { // sort=field[.key][:asc|desc]，按索引值排序
		field, dir := sort, "ASC"
		if i := strings.LastIndex(sort, ":"); i > 0 && (sort[i+1:] == "asc" || sort[i+1:] == "desc") {
			field, dir = sort[:i], strings.ToUpper(sort[i+1:])
		}
		f, err := parseFilter(field)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		args = append(args, f.field, f.key)
		orderBy = fmt.Sprintf(`(SELECT COALESCE(i.num, 0) FROM record_index i
			WHERE i.dataset=r.dataset AND i.pk=r.pk AND i.field=$%d AND i.k=$%d LIMIT 1) %s, r.pk`,
			len(args)-1, len(args), dir)
	}
	args = append(args, limit, offset)
	q := fmt.Sprintf(`
		SELECT r.pk, r.payload, r.updated_at, COUNT(*) OVER () AS total
		FROM record r WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`, strings.Join(conds, " AND "), orderBy, len(args)-1, len(args))

	rows, err := db.Query(c.Request.Context(), q, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()

	type recordOut struct {
		PK        string          `json:"pk"`
		Payload   json.RawMessage `json:"payload"`
		UpdatedAt time.Time       `json:"updatedAt"`
	}
	items := make([]recordOut, 0, limit)
	total := 0
	for rows.Next() {
		var r recordOut
		if err := rows.Scan(&r.PK, &r.Payload, &r.UpdatedAt, &total); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "scan failed"})
			return
		}
		if fields := c.Query("fields"); fields != "" { // 投影：只回指定路径，减少传输
			r.Payload = projectFields(r.Payload, strings.Split(fields, ","))
		}
		items = append(items, r)
	}
	c.JSON(http.StatusOK, gin.H{"dataset": dsID, "items": items, "total": total, "limit": limit, "offset": offset})
}

// projectFields 按点路径从原文抽取子集（路径不存在则跳过）。
func projectFields(payload []byte, paths []string) []byte {
	var doc map[string]any
	if json.Unmarshal(payload, &doc) != nil {
		return payload
	}
	out := map[string]any{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if v := walk(doc, p); v != nil {
			setPath(out, p, v)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return payload
	}
	return b
}

// setPath 按点路径写入，中间层自动建 map。
func setPath(doc map[string]any, path string, v any) {
	segs := strings.Split(path, ".")
	cur := doc
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = v
}

// handleStats GET /api/data/stats —— 各数据集记录数（平台用户 JWT）。
func handleStats(c *gin.Context) {
	if _, err := identity(c); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	rows, err := db.Query(c.Request.Context(), `
		SELECT d.id, d.enabled, COALESCE(cnt.n, 0), COALESCE(idx.n, 0)
		FROM dataset d
		LEFT JOIN (SELECT dataset, count(*) n FROM record GROUP BY dataset) cnt ON cnt.dataset = d.id
		LEFT JOIN (SELECT dataset, count(*) n FROM record_index GROUP BY dataset) idx ON idx.dataset = d.id
		ORDER BY d.id`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id string
		var enabled bool
		var records, indexRows int
		if err := rows.Scan(&id, &enabled, &records, &indexRows); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "scan failed"})
			return
		}
		out = append(out, gin.H{"dataset": id, "enabled": enabled, "records": records, "indexRows": indexRows})
	}
	c.JSON(http.StatusOK, gin.H{"service": "svc-data", "datasets": out})
}
