// HTTP 处理器：仓库管理 / 提交时间线 / 分析报告 / 总览 / 趋势。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效 id"})
		return 0, false
	}
	return id, true
}

// ── 仓库 ─────────────────────────────────────────────

func listReposHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		repos, err := loadRepos(c.Request.Context(), db, false)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if repos == nil {
			repos = []Repo{}
		}
		c.JSON(http.StatusOK, gin.H{"repos": repos})
	}
}

func createRepoHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Owner       string `json:"owner" binding:"required"`
			Name        string `json:"name" binding:"required"`
			Token       string `json:"token"`
			Branch      string `json:"branch"`
			PollEnabled *bool  `json:"pollEnabled"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		enabled := true
		if in.PollEnabled != nil {
			enabled = *in.PollEnabled
		}
		row := db.QueryRow(c.Request.Context(), `INSERT INTO repos (owner, name, token, branch, poll_enabled, created_by)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+repoCols,
			in.Owner, in.Name, in.Token, in.Branch, enabled, c.GetString("userId"))
		r, err := scanRepo(row)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "创建失败（可能已存在）: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"repo": r})
	}
}

func updateRepoHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		var in struct {
			Token       *string `json:"token"`
			Branch      *string `json:"branch"`
			PollEnabled *bool   `json:"pollEnabled"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ctx := c.Request.Context()
		if in.Token != nil {
			if _, err := db.Exec(ctx, `UPDATE repos SET token=$1 WHERE id=$2`, *in.Token, id); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		if in.Branch != nil {
			// 切分支时清掉增量水位，下次轮询自动回填新分支（sha 唯一约束去重）
			if _, err := db.Exec(ctx, `UPDATE repos SET branch=$1,
				last_commit_at = CASE WHEN branch <> $1 THEN NULL ELSE last_commit_at END
				WHERE id=$2`, *in.Branch, id); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		if in.PollEnabled != nil {
			if _, err := db.Exec(ctx, `UPDATE repos SET poll_enabled=$1 WHERE id=$2`, *in.PollEnabled, id); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		r, err := getRepo(ctx, db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "仓库不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"repo": r})
	}
}

func deleteRepoHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		if _, err := db.Exec(c.Request.Context(), `DELETE FROM repos WHERE id=$1`, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func pollRepoHandler(db *pgxpool.Pool, ai *aiClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		repo, err := getRepo(c.Request.Context(), db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "仓库不存在"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		n, err := syncRepo(ctx, db, ai, repo)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "newCommits": n})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "newCommits": n})
	}
}

func analyzeRepoHandler(db *pgxpool.Pool, ai *aiClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		repo, err := getRepo(c.Request.Context(), db, id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "仓库不存在"})
			return
		}
		go func() { // 手动触发日报（异步，周期 24h）
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			end := time.Now()
			_ = runReport(ctx, db, ai, repo, "daily_digest", end.Add(-24*time.Hour), end)
		}()
		c.JSON(http.StatusOK, gin.H{"ok": true, "status": "queued"})
	}
}

// ── 提交时间线 ────────────────────────────────────────

func listCommitsHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		ctx := c.Request.Context()
		var total int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM commits WHERE repo_id=$1`, id).Scan(&total); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		rows, err := db.Query(ctx, `SELECT id, sha, author_name, author_login, message, committed_at,
				additions, deletions, files_changed, convention_ok, convention_note, ai_note
			FROM commits WHERE repo_id=$1 ORDER BY committed_at DESC LIMIT $2 OFFSET $3`, id, limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		type commitOut struct {
			ID             int64     `json:"id"`
			SHA            string    `json:"sha"`
			AuthorName     string    `json:"authorName"`
			AuthorLogin    string    `json:"authorLogin"`
			Message        string    `json:"message"`
			CommittedAt    time.Time `json:"committedAt"`
			Additions      int       `json:"additions"`
			Deletions      int       `json:"deletions"`
			FilesChanged   int       `json:"filesChanged"`
			ConventionOK   *bool     `json:"conventionOk"`
			ConventionNote string    `json:"conventionNote"`
			AINote         string    `json:"aiNote"`
		}
		out := []commitOut{}
		for rows.Next() {
			var co commitOut
			if err := rows.Scan(&co.ID, &co.SHA, &co.AuthorName, &co.AuthorLogin, &co.Message,
				&co.CommittedAt, &co.Additions, &co.Deletions, &co.FilesChanged,
				&co.ConventionOK, &co.ConventionNote, &co.AINote); err == nil {
				out = append(out, co)
			}
		}
		c.JSON(http.StatusOK, gin.H{"commits": out, "total": total})
	}
}

// ── 分析报告 ─────────────────────────────────────────

func listAnalysesHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		kind := c.Query("kind")
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
		if limit <= 0 || limit > 50 {
			limit = 10
		}
		q := `SELECT id, kind, period_start, period_end, summary, data, created_at
			FROM analyses WHERE repo_id=$1`
		args := []any{id}
		if kind != "" {
			q += ` AND kind=$2`
			args = append(args, kind)
		}
		q += ` ORDER BY created_at DESC LIMIT ` + strconv.Itoa(limit)
		rows, err := db.Query(c.Request.Context(), q, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		type analysisOut struct {
			ID          int64           `json:"id"`
			Kind        string          `json:"kind"`
			PeriodStart time.Time       `json:"periodStart"`
			PeriodEnd   time.Time       `json:"periodEnd"`
			Summary     string          `json:"summary"`
			Data        json.RawMessage `json:"data"`
			CreatedAt   time.Time       `json:"createdAt"`
		}
		out := []analysisOut{}
		for rows.Next() {
			var a analysisOut
			if rows.Scan(&a.ID, &a.Kind, &a.PeriodStart, &a.PeriodEnd, &a.Summary, &a.Data, &a.CreatedAt) == nil {
				out = append(out, a)
			}
		}
		c.JSON(http.StatusOK, gin.H{"analyses": out})
	}
}

// ── 总览 / 趋势 ──────────────────────────────────────

func overviewHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		repos, err := loadRepos(ctx, db, false)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		type card struct {
			Repo
			Commits7d        int      `json:"commits7d"`
			Commits30d       int      `json:"commits30d"`
			ConventionRate   *float64 `json:"conventionRate30d"`
			AvgLeadTimeHours *float64 `json:"avgLeadTimeHours"`
			OpenIssues       int      `json:"openIssues"`
			LastDigest       *struct {
				Summary   string    `json:"summary"`
				CreatedAt time.Time `json:"createdAt"`
			} `json:"lastDigest"`
		}
		out := []card{}
		for _, r := range repos {
			k := card{Repo: r}
			_ = db.QueryRow(ctx, `SELECT
					count(*) FILTER (WHERE committed_at > now() - interval '7 days'),
					count(*) FILTER (WHERE committed_at > now() - interval '30 days'),
					(count(*) FILTER (WHERE convention_ok AND committed_at > now() - interval '30 days'))::float
						/ nullif(count(convention_ok) FILTER (WHERE committed_at > now() - interval '30 days'), 0)
				FROM commits WHERE repo_id=$1`, r.ID).
				Scan(&k.Commits7d, &k.Commits30d, &k.ConventionRate)
			_ = db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('open','ai_matched')),
					avg(lead_time_seconds) FILTER (WHERE status='verified')/3600.0
				FROM issues WHERE repo_id=$1`, r.ID).Scan(&k.OpenIssues, &k.AvgLeadTimeHours)
			var summary string
			var at time.Time
			if db.QueryRow(ctx, `SELECT summary, created_at FROM analyses
				WHERE repo_id=$1 AND kind='daily_digest' ORDER BY created_at DESC LIMIT 1`, r.ID).
				Scan(&summary, &at) == nil {
				k.LastDigest = &struct {
					Summary   string    `json:"summary"`
					CreatedAt time.Time `json:"createdAt"`
				}{summary, at}
			}
			out = append(out, k)
		}
		c.JSON(http.StatusOK, gin.H{"repos": out})
	}
}

func trendsHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		ctx := c.Request.Context()
		type week struct {
			WeekStart        time.Time `json:"weekStart"`
			Commits          int       `json:"commits"`
			ConventionRate   *float64  `json:"conventionRate"`
			AvgLeadTimeHours *float64  `json:"avgLeadTimeHours"`
			VerifiedIssues   int       `json:"verifiedIssues"`
		}
		weeks := map[string]*week{}
		rows, err := db.Query(ctx, `SELECT date_trunc('week', committed_at) wk, count(*),
				(count(*) FILTER (WHERE convention_ok))::float / nullif(count(convention_ok), 0)
			FROM commits WHERE repo_id=$1 AND committed_at > now() - interval '84 days'
			GROUP BY wk ORDER BY wk`, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for rows.Next() {
			var w week
			if rows.Scan(&w.WeekStart, &w.Commits, &w.ConventionRate) == nil {
				weeks[w.WeekStart.Format(time.RFC3339)] = &w
			}
		}
		rows.Close()
		irows, err := db.Query(ctx, `SELECT date_trunc('week', verified_at), avg(lead_time_seconds)/3600.0, count(*)
			FROM issues WHERE repo_id=$1 AND status='verified' AND verified_at > now() - interval '84 days'
			GROUP BY 1`, id)
		if err == nil {
			for irows.Next() {
				var wk time.Time
				var avg float64
				var n int
				if irows.Scan(&wk, &avg, &n) == nil {
					key := wk.Format(time.RFC3339)
					if weeks[key] == nil { // 该周无 commit 也要计入核验 issue
						weeks[key] = &week{WeekStart: wk}
					}
					weeks[key].AvgLeadTimeHours = &avg
					weeks[key].VerifiedIssues = n
				}
			}
			irows.Close()
		}
		outWeeks := []week{}
		for _, w := range weeks {
			outWeeks = append(outWeeks, *w)
		}
		sort.Slice(outWeeks, func(i, j int) bool { return outWeeks[i].WeekStart.Before(outWeeks[j].WeekStart) })
		hotspots := []hotspot{}
		hrows, err := db.Query(ctx, `SELECT f.path, count(*) FROM commits c,
				LATERAL jsonb_array_elements_text(c.files) AS f(path)
			WHERE c.repo_id=$1 AND c.committed_at > now() - interval '30 days'
			GROUP BY f.path ORDER BY count(*) DESC LIMIT 10`, id)
		if err == nil {
			for hrows.Next() {
				var h hotspot
				if hrows.Scan(&h.Path, &h.Changes) == nil {
					hotspots = append(hotspots, h)
				}
			}
			hrows.Close()
		}
		c.JSON(http.StatusOK, gin.H{"weeks": outWeeks, "hotspots": hotspots})
	}
}
