// Package buildinfo 让构建期注入的版本号能被非 cmd 包读到。
//
// ldflags 打的是 `cmd.version`（release.yml 里写死的路径，不动它），
// 所以由 cmd 在启动时把值推进来，其余地方统一从这里读。
package buildinfo

import "sync/atomic"

var version atomic.Value

// Set 由 cmd 包在初始化时调用。
func Set(v string) { version.Store(v) }

// Version 返回构建期注入的版本号；没注入过时返回空串。
func Version() string {
	if v, ok := version.Load().(string); ok {
		return v
	}
	return ""
}
