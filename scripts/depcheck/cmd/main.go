// depcheck 比对 go.mod 里 pin 的上游依赖和它们的最新提交，落后超过阈值就
// 以非零码退出，让 CI 去开 issue。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/perfect-panel/ppanel-node/scripts/depcheck"
)

// 只盯这两个：前者是 ppanel-node 真正用的 xray（官方被 replace 掉了），
// 后者是 REALITY 本体——2026-09-11 那次故障的修复就在它里面。
var tracked = []struct{ Module, Repo string }{
	{"github.com/wyx2685/xray-core", "wyx2685/xray-core"},
	{"github.com/xtls/reality", "XTLS/REALITY"},
}

func main() {
	threshold := flag.Int("days", 7, "落后多少天算需要处理")
	gomodPath := flag.String("gomod", "go.mod", "go.mod 路径")
	flag.Parse()

	raw, err := os.ReadFile(*gomodPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读不到 go.mod:", err)
		os.Exit(2)
	}

	stale := false
	for _, t := range tracked {
		version := depcheck.PinnedVersion(string(raw), t.Module)
		if version == "" {
			fmt.Printf("::warning::go.mod 里找不到 %s，检查项可能已失效\n", t.Module)
			stale = true
			continue
		}
		pin, err := depcheck.ParsePinnedRef(version)
		if err != nil {
			fmt.Printf("%-32s pin=%s（非 pseudo-version，跳过时间比对）\n", t.Module, version)
			continue
		}
		latest, sha, err := latestCommit(t.Repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "查 %s 最新提交失败: %v\n", t.Repo, err)
			os.Exit(2)
		}
		behind := depcheck.DaysBehind(pin.Time, latest)
		fmt.Printf("%-32s pin=%s(%s)  上游=%s(%s)  落后 %d 天\n",
			t.Module, pin.Commit, pin.Time.Format("2006-01-02"), sha[:12], latest.Format("2006-01-02"), behind)
		if behind >= *threshold {
			stale = true
		}
	}

	if stale {
		fmt.Println("")
		fmt.Println("有依赖落后超过阈值。上游的正确性修复不会自己流下来——")
		fmt.Println("2026-09-11 就是因此让节点对所有客户端失效（REALITY 缓冲修复卡在链条里 13 天）。")
		os.Exit(1)
	}
}

func latestCommit(repo string) (time.Time, string, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/commits?per_page=1", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return time.Time{}, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, "", fmt.Errorf("GitHub 返回 %s", resp.Status)
	}
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return time.Time{}, "", err
	}
	if len(commits) == 0 {
		return time.Time{}, "", fmt.Errorf("%s 没有提交", repo)
	}
	return commits[0].Commit.Committer.Date.UTC(), commits[0].SHA, nil
}
