// Package selfupdate 支撑 `ppanel-node upgrade`：定位 release 资产、校验、
// 原子替换二进制。
package selfupdate

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// release.yml 用这张表把 GOOS-GOARCH 映射成资产名。这里 embed 的是同一份拷贝，
// 手写映射迟早和构建脚本漂移。
//
//go:embed friendly-filenames.json
var friendlyNames []byte

// AssetName 返回当前平台对应的 release 资产文件名。
// 未知平台必须报错——拼出一个不存在的文件名只会在下载时变成 404。
func AssetName(goos, goarch, goarm string) (string, error) {
	var table map[string]struct {
		FriendlyName string `json:"friendlyName"`
	}
	if err := json.Unmarshal(friendlyNames, &table); err != nil {
		return "", fmt.Errorf("解析 friendly-filenames.json 失败: %w", err)
	}
	key := goos + "-" + goarch + goarm
	entry, ok := table[key]
	if !ok || entry.FriendlyName == "" {
		return "", fmt.Errorf("没有 %s 对应的 release 资产", key)
	}
	return "ppanel-node-" + entry.FriendlyName + ".zip", nil
}

// OpenSSL 3.x 输出的键名是 SHA2-256（不是 SHA256），等号后跟一个空格。
// 这个格式是下了真实的 .dgst 文件核对出来的。
var sha256Line = regexp.MustCompile(`(?m)^SHA2-256=\s*([0-9a-fA-F]{64})\s*$`)

// ParseSHA256 从 .dgst 内容里取出 SHA-256。
// 取不到一律报错：静默跳过校验等于把「升级」变成「从网上抓个文件就执行」。
func ParseSHA256(dgst []byte) (string, error) {
	m := sha256Line.FindSubmatch(dgst)
	if m == nil {
		return "", fmt.Errorf(".dgst 里找不到 SHA2-256，拒绝在未校验的情况下升级")
	}
	return strings.ToLower(string(m[1])), nil
}

var versionParts = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// IsNewer 判断 candidate 是否比 current 新。按数字逐段比，不能用字典序
// ——那样 v1.1.9 会被判成比 v1.1.10 新。
// current 解析不出来（比如未注入版本的本地构建 "TempVersion"）时一律视为可升级。
func IsNewer(current, candidate string) bool {
	c, okC := parseVersion(current)
	n, okN := parseVersion(candidate)
	if !okN {
		return false // 目标版本都认不出来，不动
	}
	if !okC {
		return true
	}
	for i := range c {
		if n[i] != c[i] {
			return n[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	m := versionParts.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// ResolveRepo 按「命令行 > 配置文件 > 默认值」定 release 仓库。
// 只有空白的值一律当作没填——否则配置里留个空格就会静默变成拉一个空仓库。
func ResolveRepo(flagValue, configValue string) string {
	for _, v := range []string{flagValue, configValue} {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return DefaultRepo
}

// ShouldSwitchVersion 判断是否需要切到控制台下发的期望版本。
//
// 【判据是「不等」，不是「更新」】新版本出问题时要能回退，而回退和升级走的是
// 同一条路——控制台把期望版本改成旧的那个，节点下次拉配置就切回去。
// 如果这里写成 IsNewer，降级会被静默忽略，而控制台上看起来已经改了。
//
// target 为空表示控制台不干预这个节点。
func ShouldSwitchVersion(current, target string) bool {
	t := strings.TrimSpace(target)
	if t == "" {
		return false
	}
	return strings.TrimSpace(current) != t
}
