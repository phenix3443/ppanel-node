package selfupdate

import (
	"sync"
	"time"
)

// LatestCache 缓存「上游最新版本号」。
//
// 【为什么要缓存】状态上报是每 60 秒一次，直接透传到 GitHub 的话，
// 未认证 API 的 60 次/小时额度几个节点就打满了，之后全部 403。
type LatestCache struct {
	TTL time.Duration

	mu        sync.Mutex
	value     string
	fetchedAt time.Time
	fetch     func(repo string) (string, error) // 测试注入点
}

// NewLatestCache 返回一个走真实 GitHub API 的缓存。
func NewLatestCache(ttl time.Duration) *LatestCache {
	return &LatestCache{TTL: ttl, fetch: fetchLatestTag}
}

// Get 返回最新版本号。取不到时返回上一次的好值（没有就返回空串）和错误——
// 版本信息是锦上添花，不能因为它拖垮心跳。
func (c *LatestCache) Get(repo string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.value != "" && time.Since(c.fetchedAt) < c.TTL {
		return c.value, nil
	}
	v, err := c.fetch(repo)
	if err != nil {
		return c.value, err // 沿用上次的好值；从未成功过时是空串
	}
	c.value, c.fetchedAt = v, time.Now()
	return v, nil
}

func fetchLatestTag(repo string) (string, error) {
	r, err := FetchRelease(repo, "")
	if err != nil {
		return "", err
	}
	return r.TagName, nil
}
