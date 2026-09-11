package selfupdate

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// 每 60 秒一次的状态上报不能每次都去打 GitHub——未认证 API 是 60 次/小时，
// 几个节点就打满了。所以要缓存，而缓存的行为必须测出来。
func TestLatestCacheHitsUpstreamOncePerTTL(t *testing.T) {
	var calls int
	var mu sync.Mutex
	fetch := func(string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return "v1.1.14", nil
	}
	c := &LatestCache{TTL: time.Hour, fetch: fetch}

	for i := 0; i < 5; i++ {
		if got, _ := c.Get("owner/repo"); got != "v1.1.14" {
			t.Fatalf("第 %d 次拿到 %q", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("打了上游 %d 次，TTL 内应当只打 1 次", calls)
	}
}

func TestLatestCacheRefetchesAfterTTL(t *testing.T) {
	var calls int
	c := &LatestCache{TTL: time.Nanosecond, fetch: func(string) (string, error) {
		calls++
		return "v1.1.14", nil
	}}
	c.Get("owner/repo")
	time.Sleep(2 * time.Millisecond)
	c.Get("owner/repo")
	if calls != 2 {
		t.Fatalf("打了上游 %d 次，TTL 过后应当重取", calls)
	}
}

// 查不到最新版不能让上报失败——版本信息是锦上添花，
// 拖垮心跳就本末倒置了。
func TestLatestCacheReturnsEmptyOnErrorInsteadOfFailing(t *testing.T) {
	c := &LatestCache{TTL: time.Hour, fetch: func(string) (string, error) {
		return "", errors.New("network down")
	}}
	got, err := c.Get("owner/repo")
	if got != "" {
		t.Errorf("出错时应返回空串，得到 %q", got)
	}
	if err == nil {
		t.Error("错误要能拿到（供调用方决定记不记日志）")
	}
}

// 上游短暂不可用时，沿用上一次拿到的值比报空更有用。
func TestLatestCacheKeepsLastGoodValueOnError(t *testing.T) {
	var fail bool
	c := &LatestCache{TTL: time.Nanosecond, fetch: func(string) (string, error) {
		if fail {
			return "", errors.New("boom")
		}
		return "v1.1.14", nil
	}}
	c.Get("owner/repo")
	fail = true
	time.Sleep(2 * time.Millisecond)
	if got, _ := c.Get("owner/repo"); got != "v1.1.14" {
		t.Fatalf("应沿用上次的 v1.1.14，得到 %q", got)
	}
}
