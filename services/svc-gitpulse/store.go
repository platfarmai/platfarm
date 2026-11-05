// 共享模型与小型查询助手（poller / analyzer 共用）。
package main

import (
	"context"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	ID            int64      `json:"id"`
	Owner         string     `json:"owner"`
	Name          string     `json:"name"`
	Token         string     `json:"-"`
	Branch        string     `json:"branch"` // 留空 = 默认分支
	DefaultBranch string     `json:"defaultBranch"`
	PollEnabled   bool       `json:"pollEnabled"`
	LastPolledAt  *time.Time `json:"lastPolledAt"`
	LastCommitAt  *time.Time `json:"lastCommitAt"`
	CreatedBy     string     `json:"createdBy"`
	CreatedAt     time.Time  `json:"createdAt"`
	HasToken      bool       `json:"hasToken"`
}

type CommitLite struct {
	ID          int64
	SHA         string
	Message     string
	CommittedAt time.Time
	Files       []string
}

const repoCols = `id, owner, name, token, branch, default_branch, poll_enabled, last_polled_at, last_commit_at, created_by, created_at`

func scanRepo(row interface{ Scan(...any) error }) (Repo, error) {
	var r Repo
	err := row.Scan(&r.ID, &r.Owner, &r.Name, &r.Token, &r.Branch, &r.DefaultBranch, &r.PollEnabled,
		&r.LastPolledAt, &r.LastCommitAt, &r.CreatedBy, &r.CreatedAt)
	r.HasToken = r.Token != ""
	return r, err
}

func loadRepos(ctx context.Context, db *pgxpool.Pool, onlyEnabled bool) ([]Repo, error) {
	q := `SELECT ` + repoCols + ` FROM repos`
	if onlyEnabled {
		q += ` WHERE poll_enabled`
	}
	q += ` ORDER BY id`
	rows, err := db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func getRepo(ctx context.Context, db *pgxpool.Pool, id int64) (Repo, error) {
	return scanRepo(db.QueryRow(ctx, `SELECT `+repoCols+` FROM repos WHERE id=$1`, id))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
