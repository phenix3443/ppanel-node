package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRepo 是我们自己的 fork。上游脚本写死 perfect-panel，那条路只会把节点
// 装回缺 REALITY 缓冲修复的版本。
const DefaultRepo = "phenix3443/ppanel-node"

const binaryInZip = "ppnode"

type Release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// FetchRelease 取指定 tag 的 release；tag 为空时取 latest。
func FetchRelease(repo, tag string) (*Release, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	if tag != "" {
		url = "https://api.github.com/repos/" + repo + "/releases/tags/" + tag
	}
	body, err := get(url)
	if err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("解析 release 响应失败: %w", err)
	}
	if r.TagName == "" {
		return nil, fmt.Errorf("%s 没有可用的 release", repo)
	}
	return &r, nil
}

// AssetURLs 找出当前平台的压缩包和它的 .dgst。两者缺一都不能继续——
// 没有校验和就没法确认下到的是什么。
func (r *Release) AssetURLs(asset string) (zipURL, dgstURL string, err error) {
	for _, a := range r.Assets {
		switch a.Name {
		case asset:
			zipURL = a.URL
		case asset + ".dgst":
			dgstURL = a.URL
		}
	}
	if zipURL == "" {
		return "", "", fmt.Errorf("release %s 里没有 %s", r.TagName, asset)
	}
	if dgstURL == "" {
		return "", "", fmt.Errorf("release %s 里 %s 没有配套的 .dgst，拒绝在未校验的情况下升级", r.TagName, asset)
	}
	return zipURL, dgstURL, nil
}

// DownloadBinary 下载、校验 SHA-256、从压缩包里取出可执行文件。
func DownloadBinary(zipURL, dgstURL string) ([]byte, error) {
	dgst, err := get(dgstURL)
	if err != nil {
		return nil, fmt.Errorf("下载校验和失败: %w", err)
	}
	want, err := ParseSHA256(dgst)
	if err != nil {
		return nil, err
	}

	archive, err := get(zipURL)
	if err != nil {
		return nil, fmt.Errorf("下载压缩包失败: %w", err)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("校验和不匹配：期望 %s，实得 %s", want, got)
	}

	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != binaryInZip {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("压缩包里没有 %s", binaryInZip)
}

// ReplaceVerified 先把 payload 落成临时文件、跑一次 `version` 确认它能执行且
// 版本正确，通过之后才替换现役二进制。验证不过就原样留着旧的。
func ReplaceVerified(path string, payload []byte, wantVersion string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ppanel-node-upgrade-*")
	if err != nil {
		return fmt.Errorf("在 %s 建临时文件失败（需要写权限）: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	// 关键一步：动现役二进制之前先确认新的这个真的能用。
	if err := VerifyBinary(tmpName, wantVersion); err != nil {
		return err
	}
	// 留一份旧的，便于人工恢复（自动回滚在节点起不来时无从触发）。
	_ = os.Rename(path, path+".bak")
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Rename(path+".bak", path) // 尽力还原
		return err
	}
	return nil
}

// Replace 原子替换目标路径上的可执行文件。
//
// 【不能原地覆盖】正在运行的可执行文件写入时会得到 "text file busy"。
// 所以先在同一目录写临时文件（必须同目录，跨设备 rename 会失败），
// 再 rename 顶替——rename 是原子的，中途失败不会留下半个二进制。
func Replace(path string, payload []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ppanel-node-upgrade-*")
	if err != nil {
		return fmt.Errorf("在 %s 建临时文件失败（需要写权限）: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后这行是空操作

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	// 【必须 fsync】rename 的元数据变更是原子的，数据落盘不是。
	// 写完直接 rename 再掉电，重启后这个路径上可能是个 0 字节文件——
	// 那时节点起不来，也就再也收不到「换回去」的指令。
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func get(url string) ([]byte, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s 返回 %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// VerifyBinary 在替换现役二进制【之前】确认下载来的那个能跑、且自报的版本
// 就是目标版本。
//
// 【为什么必须先验证而不是事后回滚】节点跑不起来就拉不到配置，也就再也收不到
// 「换回旧版本」的指令——远程恢复手段为零，只能人上机器。所以宁可多跑一次
// 子进程，也不能把一个没验过的文件 rename 上去。
//
// 它能挡住两类真实故障：架构不匹配（armv6 拿到 v7a 包会 SIGILL）和资产损坏。
func VerifyBinary(path, wantVersion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("下载的二进制跑不起来（架构不符或文件损坏）: %v: %s", err, trim(out))
	}
	if !strings.Contains(string(out), wantVersion) {
		return fmt.Errorf(
			"下载的二进制自报版本与目标不符：期望包含 %q，实际输出 %q。"+
				"放过去会陷入每轮拉取一次的重启循环", wantVersion, trim(out))
	}
	return nil
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
