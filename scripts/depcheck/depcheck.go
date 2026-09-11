// Package depcheck 比对 go.mod 里 pin 的依赖版本和上游最新提交，落后就报出来。
//
// 【为什么需要它】2026-09-11 踩过：REALITY 的一个修复 9-08 就合进了
// xtls/reality，但 ppanel-node 依赖的第三方 fork wyx2685/xray-core 停在 8-28、
// 上游 ppanel-node 的最新 tag 又发布于 9-07——修复卡在链条中间 13 天，
// 而没有任何东西会告诉我们。这段时间里节点对所有客户端都连不上。
package depcheck

import (
	"fmt"
	"regexp"
	"time"
)

// PinnedRef 是一个 Go pseudo-version 拆出来的信息。
type PinnedRef struct {
	Time   time.Time
	Commit string
}

var pseudoVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?-(\d{14})-([0-9a-f]{12})$`)

// ParsePinnedRef 拆解 v0.0.0-20260828071630-83ad74c46335 这类 pseudo-version。
// 正式 tag（如 v1.2.3）会报错而不是静默返回零值——静默零值会让整个检查永远
// 报「没落后」，正是这个工具要防的事。
func ParsePinnedRef(version string) (PinnedRef, error) {
	m := pseudoVersion.FindStringSubmatch(version)
	if m == nil {
		return PinnedRef{}, fmt.Errorf("%q 不是 pseudo-version，无法比对时间", version)
	}
	t, err := time.Parse("20060102150405", m[1])
	if err != nil {
		return PinnedRef{}, fmt.Errorf("%q 的时间戳无法解析: %w", version, err)
	}
	return PinnedRef{Time: t, Commit: m[2]}, nil
}

// DaysBehind 返回 pin 落后上游多少个整天；pin 不比上游旧时返回 0。
func DaysBehind(pinned, latest time.Time) int {
	if !latest.After(pinned) {
		return 0
	}
	return int(latest.Sub(pinned).Hours() / 24)
}

var (
	requireLine = regexp.MustCompile(`(?m)^\s*%s\s+(\S+)`)
	replaceLine = regexp.MustCompile(`(?m)^replace\s+\S+\s+\S+\s+=>\s+%s\s+(\S+)`)
)

// PinnedVersion 从 go.mod 文本里取出某个模块被 pin 的版本。
// require 段和 replace 的右侧都要认——这次落后的恰恰是 replace 指向的 fork。
func PinnedVersion(gomod, module string) string {
	quoted := regexp.QuoteMeta(module)
	for _, tmpl := range []*regexp.Regexp{replaceLine, requireLine} {
		re := regexp.MustCompile(fmt.Sprintf(tmpl.String(), quoted))
		if m := re.FindStringSubmatch(gomod); m != nil {
			return m[1]
		}
	}
	return ""
}
