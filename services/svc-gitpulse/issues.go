// 问题闭环：前端提 issue → AI/显式引用关联修复 commit → 人工确认 → 核验 lead time。
package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type issueOut struct {
	ID              int64      `json:"id"`
	RepoID          int64      `json:"repoId"`
	RepoName        string     `json:"repoName"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	CreatedBy       string     `json:"createdBy"`
	CreatedByName   string     `json:"createdByName"`
	CreatedAt       time.Time  `json:"createdAt"`
	VerifiedAt      *time.Time `json:"verifiedAt"`
	LeadTimeSeconds *int64     `json:"leadTimeSeconds"`
	MatchCount      int        `json:"matchCount"`
}

const issueSelect = `SELECT i.id, i.repo_id, r.owner || '/' || r.name, i.title, i.description, i.status,
	i.created_by, i.created_by_name, i.created_at, i.verified_at, i.lead_time_seconds,
	(SELECT count(*) FROM issue_commits ic WHERE ic.issue_id=i.id AND ic.status <> 'rejected')
	FROM issues i JOIN repos r ON r.id=i.repo_id`

func scanIssue(row interface{ Scan(...any) error }) (issueOut, error) {
	var i issueOut
	err := row.Scan(&i.ID, &i.RepoID, &i.RepoName, &i.Title, &i.Description, &i.Status,
		&i.CreatedBy, &i.CreatedByName, &i.CreatedAt, &i.VerifiedAt, &i.LeadTimeSeconds, &i.MatchCount)
	return i, err
}

func listIssuesHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		q := issueSelect
		args := []any{}
		where := []string{}
		if rid, err := strconv.ParseInt(c.Query("repoId"), 10, 64); err == nil && rid > 0 {
			args = append(args, rid)
			where = append(where, "i.repo_id=$"+strconv.Itoa(len(args)))
		}
		if st := c.Query("status"); st != "" {
			args = append(args, st)
			where = append(where, "i.status=$"+strconv.Itoa(len(args)))
		}
		for n, w := range where {
			if n == 0 {
				q += " WHERE " + w
			} else {
				q += " AND " + w
			}
		}
		q += " ORDER BY i.created_at DESC LIMIT 200"
		rows, err := db.Query(c.Request.Context(), q, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []issueOut{}
		for rows.Next() {
			if i, err := scanIssue(rows); err == nil {
				out = append(out, i)
			}
		}
		c.JSON(http.StatusOK, gin.H{"issues": out})
	}
}

func createIssueHandler(db *pgxpool.Pool, ai *aiClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			RepoID      int64  `json:"repoId" binding:"required"`
			Title       string `json:"title" binding:"required"`
			Description string `json:"description"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ctx := c.Request.Context()
		var id int64
		err := db.QueryRow(ctx, `INSERT INTO issues (repo_id, title, description, created_by, created_by_name)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			in.RepoID, in.Title, in.Description, c.GetString("userId"), c.GetString("username")).Scan(&id)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		issue, err := scanIssue(db.QueryRow(ctx, issueSelect+" WHERE i.id=$1", id))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"issue": issue})
	}
}

func getIssueHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		ctx := c.Request.Context()
		issue, err := scanIssue(db.QueryRow(ctx, issueSelect+" WHERE i.id=$1", id))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "issue 不存在"})
			return
		}
		rows, err := db.Query(ctx, `SELECT ic.id, ic.commit_id, cm.sha, cm.message, cm.committed_at,
				cm.author_name, ic.source, ic.confidence, ic.status, ic.reason
			FROM issue_commits ic JOIN commits cm ON cm.id=ic.commit_id
			WHERE ic.issue_id=$1 ORDER BY ic.confidence DESC, cm.committed_at`, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		type matchOut struct {
			ID          int64     `json:"id"`
			CommitID    int64     `json:"commitId"`
			SHA         string    `json:"sha"`
			Message     string    `json:"message"`
			CommittedAt time.Time `json:"committedAt"`
			AuthorName  string    `json:"authorName"`
			Source      string    `json:"source"`
			Confidence  float64   `json:"confidence"`
			Status      string    `json:"status"`
			Reason      string    `json:"reason"`
		}
		matches := []matchOut{}
		for rows.Next() {
			var m matchOut
			if rows.Scan(&m.ID, &m.CommitID, &m.SHA, &m.Message, &m.CommittedAt,
				&m.AuthorName, &m.Source, &m.Confidence, &m.Status, &m.Reason) == nil {
				matches = append(matches, m)
			}
		}
		c.JSON(http.StatusOK, gin.H{"issue": issue, "matches": matches})
	}
}

// confirmMatchHandler：人工确认修复 → match confirmed，issue verified，核算 lead time。
func confirmMatchHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		issueID, ok := pathID(c, "id")
		if !ok {
			return
		}
		matchID, ok := pathID(c, "matchId")
		if !ok {
			return
		}
		ctx := c.Request.Context()
		var commitAt, createdAt time.Time
		err := db.QueryRow(ctx, `SELECT cm.committed_at, i.created_at
			FROM issue_commits ic JOIN commits cm ON cm.id=ic.commit_id JOIN issues i ON i.id=ic.issue_id
			WHERE ic.id=$1 AND ic.issue_id=$2`, matchID, issueID).Scan(&commitAt, &createdAt)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "关联记录不存在"})
			return
		}
		lead := int64(commitAt.Sub(createdAt).Seconds())
		if lead < 0 {
			lead = 0
		}
		if _, err := db.Exec(ctx, `UPDATE issue_commits SET status='confirmed' WHERE id=$1`, matchID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if _, err := db.Exec(ctx, `UPDATE issues SET status='verified', verified_at=now(), lead_time_seconds=$1
			WHERE id=$2`, lead, issueID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		issue, _ := scanIssue(db.QueryRow(ctx, issueSelect+" WHERE i.id=$1", issueID))
		c.JSON(http.StatusOK, gin.H{"issue": issue})
	}
}

func rejectMatchHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		issueID, ok := pathID(c, "id")
		if !ok {
			return
		}
		matchID, ok := pathID(c, "matchId")
		if !ok {
			return
		}
		ctx := c.Request.Context()
		if _, err := db.Exec(ctx, `UPDATE issue_commits SET status='rejected'
			WHERE id=$1 AND issue_id=$2`, matchID, issueID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 没有剩余有效关联则回退 open
		_, _ = db.Exec(ctx, `UPDATE issues SET status='open' WHERE id=$1 AND status='ai_matched'
			AND NOT EXISTS(SELECT 1 FROM issue_commits WHERE issue_id=$1 AND status <> 'rejected')`, issueID)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// patchIssueHandler：仅支持 close / reopen。
func patchIssueHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		var in struct {
			Status string `json:"status" binding:"required"`
		}
		if err := c.ShouldBindJSON(&in); err != nil || (in.Status != "closed" && in.Status != "open") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status 仅支持 closed / open"})
			return
		}
		ctx := c.Request.Context()
		if _, err := db.Exec(ctx, `UPDATE issues SET status=$1 WHERE id=$2`, in.Status, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		issue, err := scanIssue(db.QueryRow(ctx, issueSelect+" WHERE i.id=$1", id))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "issue 不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"issue": issue})
	}
}
