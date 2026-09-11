package depcheck

import (
	"testing"
	"time"
)

// Go 的 pseudo-version 形如 v0.0.0-20260828071630-83ad74c46335，
// 中段是 UTC 时间戳、尾段是 commit 前缀。解析错了这个检查会永远报
// 「没落后」——而那正是它要防的失败模式，所以先把它钉死。
func TestParsePinnedRef(t *testing.T) {
	got, err := ParsePinnedRef("v0.0.0-20260828071630-83ad74c46335")
	if err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	want := time.Date(2026, 8, 28, 7, 16, 30, 0, time.UTC)
	if !got.Time.Equal(want) {
		t.Errorf("时间 = %v, want %v", got.Time, want)
	}
	if got.Commit != "83ad74c46335" {
		t.Errorf("commit = %q, want 83ad74c46335", got.Commit)
	}
}

func TestParsePinnedRefRejectsPlainSemver(t *testing.T) {
	// 正式 tag 没有时间戳，不能被当成 pseudo-version 静默解析成零值。
	if _, err := ParsePinnedRef("v1.2.3"); err == nil {
		t.Fatal("v1.2.3 应当报错，而不是被当成 pseudo-version")
	}
}

func TestParsePinnedRefRejectsGarbage(t *testing.T) {
	for _, v := range []string{"", "v0.0.0-notadate-83ad74c46335", "v0.0.0-20260828071630"} {
		if _, err := ParsePinnedRef(v); err == nil {
			t.Errorf("%q 应当报错", v)
		}
	}
}

func TestDaysBehindCountsWholeDays(t *testing.T) {
	pinned := time.Date(2026, 8, 28, 7, 16, 30, 0, time.UTC)
	latest := time.Date(2026, 9, 10, 1, 18, 53, 0, time.UTC)
	if got := DaysBehind(pinned, latest); got != 12 {
		t.Fatalf("DaysBehind = %d, want 12", got)
	}
}

func TestDaysBehindIsZeroWhenPinIsNewer(t *testing.T) {
	pinned := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	latest := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if got := DaysBehind(pinned, latest); got != 0 {
		t.Fatalf("DaysBehind = %d, want 0（pin 比上游新时不该报负数）", got)
	}
}

// go.mod 里 replace 指向的 fork 也要能抽出来——这次落后的就是它。
func TestPinnedVersionFromGoMod(t *testing.T) {
	mod := `module example.com/x

require (
	github.com/xtls/xray-core v1.260327.0
	github.com/xtls/reality v0.0.0-20260322125925-9234c772ba8f // indirect
)

replace github.com/xtls/xray-core v1.260327.0 => github.com/wyx2685/xray-core v0.0.0-20260828071630-83ad74c46335
`
	if got := PinnedVersion(mod, "github.com/wyx2685/xray-core"); got != "v0.0.0-20260828071630-83ad74c46335" {
		t.Errorf("replace 目标的版本 = %q", got)
	}
	if got := PinnedVersion(mod, "github.com/xtls/reality"); got != "v0.0.0-20260322125925-9234c772ba8f" {
		t.Errorf("require 里的版本 = %q", got)
	}
	if got := PinnedVersion(mod, "github.com/nope/nope"); got != "" {
		t.Errorf("不存在的模块应返回空，得到 %q", got)
	}
}
