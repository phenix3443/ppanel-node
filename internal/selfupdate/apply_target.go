package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
)

// 同一个目标版本只尝试一次。失败时不无限重试——拉取周期是 60 秒，
// 反复下载失败的版本会把日志和带宽刷满，而故障原因（比如版本号填错）
// 不会自己好转。改了期望版本才会再试。
var (
	attemptMu   sync.Mutex
	lastAttempt string
)

// ApplyTargetVersion 在控制台下发的期望版本与当前版本不同时切过去。
//
// 【也用于降级】判据是「不等」而不是「更新」，见 ShouldSwitchVersion。
//
// restart 为 nil 时使用 systemctl 重启；测试注入自己的实现。
func ApplyTargetVersion(current, target, repo string, restart func() error) error {
	if !ShouldSwitchVersion(current, target) {
		return nil
	}

	attemptMu.Lock()
	if lastAttempt == target {
		attemptMu.Unlock()
		return nil // 这个目标版本已经试过且失败了，等控制台改了再说
	}
	lastAttempt = target
	attemptMu.Unlock()

	release, err := FetchRelease(ResolveRepo("", repo), target)
	if err != nil {
		return fmt.Errorf("取 release %s 失败: %w", target, err)
	}
	asset, err := AssetName(runtime.GOOS, runtime.GOARCH, armSuffix())
	if err != nil {
		return err
	}
	zipURL, dgstURL, err := release.AssetURLs(asset)
	if err != nil {
		return err
	}
	payload, err := DownloadBinary(zipURL, dgstURL)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := Replace(self, payload); err != nil {
		return err
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
func systemctlRestart() error {
	out, err := exec.Command("systemctl", "restart", "ppanel-node").CombinedOutput()
	if err != nil {
		return fmt.Errorf("重启 ppanel-node 失败: %v: %s", err, out)
	}
	return nil
}

// ResetAttemptForTest 让测试能清掉「同一目标只试一次」的记忆。
func ResetAttemptForTest() {
	attemptMu.Lock()
	lastAttempt = ""
	attemptMu.Unlock()
}
