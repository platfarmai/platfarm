// 轮询器：按 GITPULSE_POLL_MINUTES（默认 15）增量拉取各仓 commit 元数据，幂等入库，
// 入库后触发规则检查、issue 关联与按需 AI 分析。
package main

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const backfillPages = 2 // 首次接入回填最多 200 条

var repoLocks sync.Map // repoID → *sync.Mutex，避免定时轮询与手动触发同仓并发

func lockRepo(id int64) func() {
	mu, _ := repoLocks.LoadOrStore(id, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

func startPoller(ctx context.Context, db *pgxpool.Pool, ai *aiClient) {
	minutes := 15
	if v, err := strconv.Atoi(envOr("GITPULSE_POLL_MINUTES", "15")); err == nil && v > 0 {
		minutes = v
	}
	go func() {
		pollAll(ctx, db, ai)
		t := time.NewTicker(time.Duration(minutes) * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pollAll(ctx, db, ai)
			}
		}
	}()
	log.Printf("poller started: every %d minutes", minutes)
}

func pollAll(ctx context.Context, db *pgxpool.Pool, ai *aiClient) {
	repos, err := loadRepos(ctx, db, true)
	if err != nil {
		log.Printf("poll: load repos: %v", err)
		return
	}
	for _, r := range repos {
		if ctx.Err() != nil {
			return
		}
		if _, err := syncRepo(ctx, db, ai, r); err != nil {
			log.Printf("poll %s/%s: %v", r.Owner, r.Name, err)
		}
	}
}

// syncRepo：拉增量 → 入库 → 规则检查 → 显式引用关联 → AI 匹配 → 按需报告。返回新增数。
func syncRepo(ctx context.Context, db *pgxpool.Pool, ai *aiClient, repo Repo) (int, error) {
	unlock := lockRepo(repo.ID)
	defer unlock()

	gh := newGHClient(repo.Token)
	if repo.DefaultBranch == "" {
		info, err := gh.repoInfo(ctx, repo.Owner, repo.Name)
		if err != nil {
			return 0, err
		}
		repo.DefaultBranch = info.DefaultBranch
		_, _ = db.Exec(ctx, `UPDATE repos SET default_branch=$1 WHERE id=$2`, info.DefaultBranch, repo.ID)
	}
	branch := repo.Branch // 指定分支优先，留空用默认分支
	if branch == "" {
		branch = repo.DefaultBranch
	}

	var since time.Time
	maxPages := backfillPages
	if repo.LastCommitAt != nil {
		since = repo.LastCommitAt.Add(-2 * time.Minute) // 重叠窗口防漏，UNIQUE 去重
		maxPages = 5
	}

	var newCommits []CommitLite
	for page := 1; page <= maxPages; page++ {
		list, err := gh.listCommits(ctx, repo.Owner, repo.Name, branch, since, page)
		if err != nil {
			return len(newCommits), err
		}
		for _, item := range list {
			var exists bool
			if err := db.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM commits WHERE repo_id=$1 AND sha=$2)`,
				repo.ID, item.SHA).Scan(&exists); err != nil || exists {
				continue
			}
			detail, err := gh.commitDetail(ctx, repo.Owner, repo.Name, item.SHA)
			if err != nil {
				log.Printf("commit detail %s: %v", item.SHA[:8], err)
				continue
			}
			files := make([]string, 0, len(detail.Files))
			for _, f := range detail.Files {
				files = append(files, f.Filename)
			}
			filesJSON, _ := json.Marshal(files)
			login := ""
			if item.Author != nil {
				login = item.Author.Login
			}
			convOK, convNote := checkConvention(item.Commit.Message)
			var commitID int64
			err = db.QueryRow(ctx, `INSERT INTO commits
				(repo_id, sha, author_name, author_login, message, committed_at,
				 additions, deletions, files_changed, files, convention_ok, convention_note)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
				ON CONFLICT (repo_id, sha) DO NOTHING RETURNING id`,
				repo.ID, item.SHA, item.Commit.Author.Name, login, item.Commit.Message,
				item.Commit.Author.Date, detail.Stats.Additions, detail.Stats.Deletions,
				len(detail.Files), filesJSON, convOK, convNote).Scan(&commitID)
			if err != nil { // 并发下 DO NOTHING 无返回行，跳过即可
				continue
			}
			c := CommitLite{ID: commitID, SHA: item.SHA, Message: item.Commit.Message,
				CommittedAt: item.Commit.Author.Date, Files: files}
			newCommits = append(newCommits, c)
			linkExplicitRefs(ctx, db, repo.ID, c)
		}
		if len(list) < 100 {
			break
		}
	}

	// last_commit_at 以库内全量 max 为准（回填分页是从新到旧，不能用本轮增量的 max）
	_, _ = db.Exec(ctx, `UPDATE repos SET last_polled_at=now(),
		last_commit_at=(SELECT max(committed_at) FROM commits WHERE repo_id=$1) WHERE id=$1`, repo.ID)

	if len(newCommits) > 0 {
		matchIssuesAI(ctx, db, ai, repo, newCommits)
		log.Printf("synced %s/%s: %d new commits", repo.Owner, repo.Name, len(newCommits))
	}
	maybeAnalyze(ctx, db, ai, repo)
	return len(newCommits), nil
}
