// 多租户激活（specs/022）：auth 只管租户实体与用户归属（身份域）；
// 数据隔离仍是 L3——各服务查询按 claims.tenantId 过滤，平台不代判。
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type tenantRow struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Status    int    `json:"status"`
	CreatedAt string `json:"createdAt"`
}

// handleTenantsList GET /internal/auth/tenants —— admin OBO。
func (s *server) handleTenantsList(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(),
		`SELECT id, name, status, created_at FROM tenants ORDER BY id`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	items := []tenantRow{}
	for rows.Next() {
		var t tenantRow
		var ts time.Time
		if rows.Scan(&t.ID, &t.Name, &t.Status, &ts) == nil {
			t.CreatedAt = ts.Format(time.RFC3339)
			items = append(items, t)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// handleTenantsCreate POST /internal/auth/tenants {name} —— admin OBO。
func (s *server) handleTenantsCreate(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	var in struct{ Name string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Name == "" {
		writeErr(w, 400, "name required")
		return
	}
	var id int
	if err := s.db.QueryRow(r.Context(),
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`, in.Name).Scan(&id); err != nil {
		writeErr(w, 409, "tenant name exists")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": in.Name})
}

// handleTenantsUpdate PATCH /internal/auth/tenants/{id} {name?, status?} —— admin OBO。
// 停用租户即用户级吊销该租户全部用户（token 内 tenantId 已定，禁用后不得继续使用）。
func (s *server) handleTenantsUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	var in struct {
		Name   *string `json:"name"`
		Status *int    `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "invalid body")
		return
	}
	var name string
	var status int
	if err := s.db.QueryRow(r.Context(),
		`SELECT name, status FROM tenants WHERE id=$1`, id).Scan(&name, &status); err != nil {
		writeErr(w, 404, "tenant not found")
		return
	}
	if in.Name != nil && *in.Name != "" {
		name = *in.Name
	}
	if in.Status != nil {
		status = *in.Status
	}
	if _, err := s.db.Exec(r.Context(),
		`UPDATE tenants SET name=$2, status=$3 WHERE id=$1`, id, name, status); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if status != 1 {
		rows, err := s.db.Query(r.Context(), `SELECT id FROM users WHERE tenant_id=$1`, id)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var uid int
				if rows.Scan(&uid) == nil {
					s.revokeUserTokens(uid)
				}
			}
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
