package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// 失败后按次数退避，而不是「只试一次」。
//
// 【为什么不是只试一次】只试一次会被一次网络抖动永久卡死：失败后即便运维把
// 期望版本清空再设回同一个值，也不会重试（清空走的是提前返回，不重置记忆）。
// 叠加「配置未变就不触发」那层门禁，重试窗口本来就很窄。
//
// 【为什么也不能无限重试】拉取周期 60 秒，反复下载一个装不上的版本会刷满
// 日志和带宽，而原因（版本号填错、架构不符）不会自己好转。
var (
	attemptMu     sync.Mutex
	attemptTarget string
	attemptFails  int
	attemptNext   time.Time
)

// 退避到 30 分钟封顶：第 1 次失败等 1 分钟，之后翻倍。
const maxAttemptBackoff = 30 * time.Minute

func shouldAttempt(target string) bool {
	attemptMu.Lock()
	defer attemptMu.Unlock()
	if target != attemptTarget {
		// 目标变了（含改回之前失败过的那个）——重新开始计数。
		attemptTarget, attemptFails, attemptNext = target, 0, time.Time{}
		return true
	}
	return !time.Now().Before(attemptNext)
}

func noteFailure(target string) {
	attemptMu.Lock()
	defer attemptMu.Unlock()
	if target != attemptTarget {
		return
	}
	attemptFails++
	backoff := time.Duration(1<<min(attemptFails-1, 5)) * time.Minute
	if backoff > maxAttemptBackoff {
		backoff = maxAttemptBackoff
	}
	attemptNext = time.Now().Add(backoff)
}

// ApplyTargetVersion 在控制台下发的期望版本与当前版本不同时切过去。
//
// 【也用于降级】判据是「不等」而不是「更新」，见 ShouldSwitchVersion。
//
// restart 为 nil 时使用 systemctl 重启；测试注入自己的实现。
func ApplyTargetVersion(current, target, repo string, restart func() error) error {
	if !ShouldSwitchVersion(current, target) {
		return nil
	}

	if !shouldAttempt(target) {
		return nil // 退避中
	}

	fail := func(err error) error { noteFailure(target); return err }

	release, err := FetchRelease(ResolveRepo(repo, ""), target)
	if err != nil {
		return fail(fmt.Errorf("取 release %s 失败: %w", target, err))
	}
	asset, err := AssetName(runtime.GOOS, runtime.GOARCH, armSuffix())
	if err != nil {
		return fail(err)
	}
	zipURL, dgstURL, err := release.AssetURLs(asset)
	if err != nil {
		return fail(err)
	}
	payload, err := DownloadBinary(zipURL, dgstURL)
	if err != nil {
		return fail(err)
	}
	self, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	// 先验证再替换：换坏了就再也收不到「换回去」的指令。
	if err := ReplaceVerified(self, payload, target); err != nil {
		return fail(err)
	}

	if restart == nil {
		restart = systemctlRestart
	}
	return restart()
}

func armSuffix() string {
	if runtime.GOARCH != "arm" {
		return ""
	}
	return "7"
}

// 我们的 unit 用 Restart=on-failure，正常退出不会被拉起，所以要显式重启。
//
// 【必须用 --no-block】默认 KillMode=control-group，systemd 停这个 unit 时会
// 连同 cgroup 里的 systemctl 子进程一起杀掉，于是 err 恒为 signal: terminated
// ——重启其实成功了，却永远报「升级失败」。--no-block 让 systemctl 提交请求
// 后立即返回，不等待也就不会被自己杀掉。
func systemctlRestart() error {
	out, err := exec.Command("systemctl", "restart", "--no-block", "ppanel-node").CombinedOutput()
	if err != nil {
		return fmt.Errorf("重启 ppanel-node 失败: %v: %s", err, out)
	}
	return nil
}

// ResetAttemptForTest 清掉退避状态，供测试使用。
func ResetAttemptForTest() {
	attemptMu.Lock()
	attemptTarget, attemptFails, attemptNext = "", 0, time.Time{}
	attemptMu.Unlock()
}
