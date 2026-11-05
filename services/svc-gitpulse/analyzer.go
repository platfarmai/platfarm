// 分析层：规则引擎（零成本预筛）+ AI（中文日报/周报、issue 语义匹配）。
// 原则：能用规则算的不花 token；AI 只消费预计算的事实。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	conventionRe = regexp.MustCompile(`^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([^)]+\))?!?: .+`)
	issueRefRe   = regexp.MustCompile(`(?i)(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)\s+#(\d+)`)
)

// checkConvention：规则判定 Conventional Commits。merge/revert 自动提交不判（返回 nil）。
func checkConvention(msg string) (*bool, string) {
	first := strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0]
	if strings.HasPrefix(first, "Merge ") || strings.HasPrefix(first, "Revert \"") {
		return nil, ""
	}
	ok := conventionRe.MatchString(first)
	note := ""
	if !ok {
		note = "不符合 Conventional Commits（期望 type(scope): subject）"
	}
	t := ok
	return &t, note
}

func parseIssueRefs(msg string) []int64 {
	var ids []int64
	for _, m := range issueRefRe.FindAllStringSubmatch(msg, -1) {
		if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// linkExplicitRefs：commit message 里的 fix #N → 高置信关联，issue 置 ai_matched。
func linkExplicitRefs(ctx context.Context, db *pgxpool.Pool, repoID int64, c CommitLite) {
	for _, issueID := range parseIssueRefs(c.Message) {
		var ok bool
		if err := db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM issues WHERE id=$1 AND repo_id=$2 AND status IN ('open','ai_matched'))`,
			issueID, repoID).Scan(&ok); err != nil || !ok {
			continue
		}
		_, _ = db.Exec(ctx, `INSERT INTO issue_commits (issue_id, commit_id, source, confidence, reason)
			VALUES ($1,$2,'message_ref',1.0,'commit message 显式引用')
			ON CONFLICT (issue_id, commit_id) DO NOTHING`, issueID, c.ID)
		_, _ = db.Exec(ctx, `UPDATE issues SET status='ai_matched' WHERE id=$1 AND status='open'`, issueID)
	}
}

// matchIssuesAI：对 open issue 做语义匹配（新 commit 批量送一次，带置信度，待人工确认）。
func matchIssuesAI(ctx context.Context, db *pgxpool.Pool, ai *aiClient, repo Repo, newCommits []CommitLite) {
	if !ai.enabled() || len(newCommits) == 0 || len(newCommits) > 50 {
		return
	}
	rows, err := db.Query(ctx,
		`SELECT id, title, description FROM issues WHERE repo_id=$1 AND status IN ('open','ai_matched')`, repo.ID)
	if err != nil {
		return
	}
	type iss struct {
		ID          int64
		Title, Desc string
	}
	var issues []iss
	for rows.Next() {
		var i iss
		if rows.Scan(&i.ID, &i.Title, &i.Desc) == nil {
			issues = append(issues, i)
		}
	}
	rows.Close()
	if len(issues) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("## 待修复问题\n")
	for _, i := range issues {
		fmt.Fprintf(&sb, "- issue %d: %s — %s\n", i.ID, i.Title, truncate(i.Desc, 200))
	}
	sb.WriteString("\n## 新提交\n")
	for _, c := range newCommits {
		files := strings.Join(c.Files, ", ")
		fmt.Fprintf(&sb, "- %s (%s): %s [files: %s]\n", c.SHA[:minInt(8, len(c.SHA))],
			c.CommittedAt.Format("2006-01-02 15:04"), truncate(c.Message, 150), truncate(files, 200))
	}
	var out struct {
		Matches []struct {
			IssueID    int64   `json:"issueId"`
			SHA        string  `json:"sha"`
			Confidence float64 `json:"confidence"`
			Reason     string  `json:"reason"`
		} `json:"matches"`
	}
	sys := "你是代码仓库分析助手。判断哪些提交可能修复了哪些问题。只在有实质关联时输出匹配，置信度 0~1。" +
		`严格返回 JSON：{"matches":[{"issueId":1,"sha":"完整或前8位sha","confidence":0.8,"reason":"中文一句话依据"}]}，无匹配返回 {"matches":[]}`
	if err := ai.completeJSON(ctx, sys, sb.String(), 1500, &out); err != nil {
		return
	}
	for _, m := range out.Matches {
		if m.Confidence < 0.5 {
			continue
		}
		var commitID int64
		if err := db.QueryRow(ctx,
			`SELECT id FROM commits WHERE repo_id=$1 AND sha LIKE $2 || '%' LIMIT 1`,
			repo.ID, m.SHA).Scan(&commitID); err != nil {
			continue
		}
		_, _ = db.Exec(ctx, `INSERT INTO issue_commits (issue_id, commit_id, source, confidence, reason)
			VALUES ($1,$2,'ai_match',$3,$4) ON CONFLICT (issue_id, commit_id) DO NOTHING`,
			m.IssueID, commitID, m.Confidence, m.Reason)
		_, _ = db.Exec(ctx, `UPDATE issues SET status='ai_matched' WHERE id=$1 AND status='open'`, m.IssueID)
	}
}

// maybeAnalyze：日报 >24h 一次，周报 >7d 一次（都要求窗口内有新 commit）。
func maybeAnalyze(ctx context.Context, db *pgxpool.Pool, ai *aiClient, repo Repo) {
	runIfDue(ctx, db, ai, repo, "daily_digest", 24*time.Hour)
	runIfDue(ctx, db, ai, repo, "weekly_trend", 7*24*time.Hour)
}

func runIfDue(ctx context.Context, db *pgxpool.Pool, ai *aiClient, repo Repo, kind string, span time.Duration) {
	var last *time.Time
	_ = db.QueryRow(ctx,
		`SELECT max(created_at) FROM analyses WHERE repo_id=$1 AND kind=$2`, repo.ID, kind).Scan(&last)
	if last != nil && time.Since(*last) < span {
		return
	}
	end := time.Now()
	start := end.Add(-span)
	var n int
	_ = db.QueryRow(ctx,
		`SELECT count(*) FROM commits WHERE repo_id=$1 AND committed_at > $2`, repo.ID, start).Scan(&n)
	if n == 0 {
		return
	}
	if err := runReport(ctx, db, ai, repo, kind, start, end); err != nil {
		log.Printf("analyze %s repo=%s/%s: %v", kind, repo.Owner, repo.Name, err)
	}
}

type periodStats struct {
	Commits        int        `json:"commits"`
	Authors        int        `json:"authors"`
	Additions      int        `json:"additions"`
	Deletions      int        `json:"deletions"`
	ConventionOK   int        `json:"conventionOk"`
	ConventionAll  int        `json:"conventionChecked"`
	LargestFiles   int        `json:"largestCommitFiles"`
	Hotspots       []hotspot  `json:"hotspots"`
	VerifiedIssues int        `json:"verifiedIssues"`
	AvgLeadHours   *float64   `json:"avgLeadHours"`
}

type hotspot struct {
	Path    string `json:"path"`
	Changes int    `json:"changes"`
}

func collectStats(ctx context.Context, db *pgxpool.Pool, repoID int64, start, end time.Time) (periodStats, error) {
	var s periodStats
	err := db.QueryRow(ctx, `SELECT count(*), count(DISTINCT author_name), coalesce(sum(additions),0),
			coalesce(sum(deletions),0), count(*) FILTER (WHERE convention_ok),
			count(convention_ok), coalesce(max(files_changed),0)
		FROM commits WHERE repo_id=$1 AND committed_at > $2 AND committed_at <= $3`,
		repoID, start, end).Scan(&s.Commits, &s.Authors, &s.Additions, &s.Deletions,
		&s.ConventionOK, &s.ConventionAll, &s.LargestFiles)
	if err != nil {
		return s, err
	}
	rows, err := db.Query(ctx, `SELECT f.path, count(*) FROM commits c,
			LATERAL jsonb_array_elements_text(c.files) AS f(path)
		WHERE c.repo_id=$1 AND c.committed_at > $2 GROUP BY f.path ORDER BY count(*) DESC LIMIT 5`,
		repoID, start)
	if err == nil {
		for rows.Next() {
			var h hotspot
			if rows.Scan(&h.Path, &h.Changes) == nil {
				s.Hotspots = append(s.Hotspots, h)
			}
		}
		rows.Close()
	}
	_ = db.QueryRow(ctx, `SELECT count(*), avg(lead_time_seconds)/3600.0 FROM issues
		WHERE repo_id=$1 AND status='verified' AND verified_at > $2`, repoID, start).
		Scan(&s.VerifiedIssues, &s.AvgLeadHours)
	return s, nil
}

func runReport(ctx context.Context, db *pgxpool.Pool, ai *aiClient, repo Repo, kind string, start, end time.Time) error {
	stats, err := collectStats(ctx, db, repo.ID, start, end)
	if err != nil {
		return err
	}
	summary := ruleSummary(kind, stats)
	var notable []struct {
		SHA  string `json:"sha"`
		Note string `json:"note"`
	}
	if ai.enabled() {
		rows, err := db.Query(ctx, `SELECT sha, author_name, message, committed_at, additions, deletions,
				files_changed, coalesce(convention_ok, true)
			FROM commits WHERE repo_id=$1 AND committed_at > $2 AND committed_at <= $3
			ORDER BY committed_at DESC LIMIT 120`, repo.ID, start, end)
		if err != nil {
			return err
		}
		var sb strings.Builder
		for rows.Next() {
			var sha, author, msg string
			var at time.Time
			var add, del, fc int
			var convOK bool
			if rows.Scan(&sha, &author, &msg, &at, &add, &del, &fc, &convOK) == nil {
				flag := ""
				if !convOK {
					flag = " [不规范]"
				}
				fmt.Fprintf(&sb, "- %s %s %s +%d/-%d %d files%s: %s\n", sha[:8],
					at.Format("01-02 15:04"), author, add, del, fc, flag, truncate(firstLine(msg), 120))
			}
		}
		rows.Close()
		statJSON, _ := json.Marshal(stats)
		kindName := map[string]string{"daily_digest": "日报", "weekly_trend": "周报"}[kind]
		sys := "你是项目周期管理 AI，面向 " + repo.Owner + "/" + repo.Name + " 仓库生成" + kindName + "。" +
			"分析重点：提交节奏与时间分布、功能推进脉络、风险信号（巨型提交/集中返工/规范滑坡）、修复周期。" +
			"聚焦项目健康度，不评价个人绩效、不统计个人产出排名。中文输出。" +
			`严格返回 JSON：{"summary":"markdown 格式报告（150~400字，可用列表）","notable":[{"sha":"前8位","note":"一句话注解（值得关注的提交才给）"}]}`
		var out struct {
			Summary string `json:"summary"`
			Notable []struct {
				SHA  string `json:"sha"`
				Note string `json:"note"`
			} `json:"notable"`
		}
		if err := ai.completeJSON(ctx, sys,
			"统计指标：\n"+string(statJSON)+"\n\n提交列表：\n"+sb.String(), 2000, &out); err == nil && out.Summary != "" {
			summary = out.Summary
			notable = out.Notable
		}
	}
	data, _ := json.Marshal(stats)
	if _, err := db.Exec(ctx, `INSERT INTO analyses (repo_id, kind, period_start, period_end, summary, data)
		VALUES ($1,$2,$3,$4,$5,$6)`, repo.ID, kind, start, end, summary, data); err != nil {
		return err
	}
	for _, n := range notable {
		if len(n.SHA) >= 7 && n.Note != "" {
			_, _ = db.Exec(ctx, `UPDATE commits SET ai_note=$1 WHERE repo_id=$2 AND sha LIKE $3 || '%'`,
				n.Note, repo.ID, n.SHA)
		}
	}
	log.Printf("analysis %s stored for %s/%s (%d commits)", kind, repo.Owner, repo.Name, stats.Commits)
	return nil
}

// ruleSummary：AI 不可用时的降级报告。
func ruleSummary(kind string, s periodStats) string {
	rate := "N/A"
	if s.ConventionAll > 0 {
		rate = fmt.Sprintf("%.0f%%", float64(s.ConventionOK)/float64(s.ConventionAll)*100)
	}
	return fmt.Sprintf("（规则统计）提交 %d 次 / %d 位作者，+%d/-%d 行，规范率 %s，最大单次提交改动 %d 个文件。",
		s.Commits, s.Authors, s.Additions, s.Deletions, rate, s.LargestFiles)
}

func firstLine(s string) string { return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0] }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
