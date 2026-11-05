// GitHub REST API 客户端：只取 commit 元数据（message / 时间 / diff stat / 文件列表），不拉代码内容。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

const ghBase = "https://api.github.com"

type ghClient struct {
	token string
	hc    *http.Client
}

// newGHClient：repo 自带 token 优先，留空 fallback 全局 GITHUB_TOKEN。
func newGHClient(repoToken string) *ghClient {
	tok := repoToken
	if tok == "" {
		tok = os.Getenv("GITHUB_TOKEN")
	}
	return &ghClient{token: tok, hc: &http.Client{Timeout: 30 * time.Second}}
}

func (g *ghClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ghBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github %s: %d %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	return json.Unmarshal(body, out)
}

type ghRepoInfo struct {
	DefaultBranch string `json:"default_branch"`
}

func (g *ghClient) repoInfo(ctx context.Context, owner, name string) (ghRepoInfo, error) {
	var info ghRepoInfo
	err := g.get(ctx, "/repos/"+owner+"/"+name, &info)
	return info, err
}

type ghListCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

// listCommits：按时间增量拉取（since 为空 = 首次回填，由调用方限制页数）。
func (g *ghClient) listCommits(ctx context.Context, owner, name, branch string, since time.Time, page int) ([]ghListCommit, error) {
	q := url.Values{}
	q.Set("per_page", "100")
	q.Set("page", fmt.Sprintf("%d", page))
	if branch != "" {
		q.Set("sha", branch)
	}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	var list []ghListCommit
	err := g.get(ctx, "/repos/"+owner+"/"+name+"/commits?"+q.Encode(), &list)
	return list, err
}

type ghCommitDetail struct {
	Stats struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
	} `json:"stats"`
	Files []struct {
		Filename string `json:"filename"`
	} `json:"files"`
}

func (g *ghClient) commitDetail(ctx context.Context, owner, name, sha string) (ghCommitDetail, error) {
	var d ghCommitDetail
	err := g.get(ctx, "/repos/"+owner+"/"+name+"/commits/"+sha, &d)
	return d, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
