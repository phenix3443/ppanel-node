package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/perfect-panel/ppanel-node/conf"
	"github.com/perfect-panel/ppanel-node/internal/selfupdate"
	"github.com/spf13/cobra"
)

var (
	upgradeRepo    string
	upgradeConfig  string
	upgradeCheck   bool
	upgradeRestart bool
)

var upgradeCommand = cobra.Command{
	Use:   "upgrade [version]",
	Short: "Upgrade ppanel-node to a release build",
	Long: `从 GitHub release 升级 ppanel-node。

不带参数升到最新版，也可以指定版本（如 upgrade v1.1.14）。
下载后会校验 SHA-256，校验和缺失或不匹配一律中止。
替换采用同目录临时文件 + rename，正在运行的二进制无法原地覆盖。`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		var tag string
		if len(args) == 1 {
			tag = args[0]
		}

		// 仓库来源：命令行 > 配置文件 > 内置默认。配置读不到不算错——
		// upgrade 在没有配置文件的机器上也该能用。
		var cfgRepo string
		if c := conf.New(); c.LoadFromPath(upgradeConfig) == nil {
			cfgRepo = c.UpgradeConfig.Repo
		}
		repo := selfupdate.ResolveRepo(upgradeRepo, cfgRepo)

		release, err := selfupdate.FetchRelease(repo, tag)
		if err != nil {
			return err
		}

		fmt.Printf("当前版本: %s\n最新版本: %s\n", version, release.TagName)
		if !selfupdate.IsNewer(version, release.TagName) && tag == "" {
			fmt.Println("已是最新，无需升级。")
			return nil
		}
		if upgradeCheck {
			fmt.Println("有可用升级（--check 只查不装）。")
			return nil
		}

		asset, err := selfupdate.AssetName(runtime.GOOS, runtime.GOARCH, goarmSuffix())
		if err != nil {
			return err
		}
		zipURL, dgstURL, err := release.AssetURLs(asset)
		if err != nil {
			return err
		}

		fmt.Printf("下载 %s …\n", asset)
		payload, err := selfupdate.DownloadBinary(zipURL, dgstURL)
		if err != nil {
			return err
		}

		self, err := os.Executable()
		if err != nil {
			return err
		}
		// 先验证再替换：换坏了的节点连不上面板，也就收不到「换回去」的指令。
		if err := selfupdate.ReplaceVerified(self, payload, release.TagName); err != nil {
			return err
		}
		fmt.Printf("已替换 %s → %s\n", self, release.TagName)

		if !upgradeRestart {
			fmt.Println("重启后生效：systemctl restart ppanel-node")
			return nil
		}
		fmt.Println("重启 ppanel-node …")
		out, err := exec.Command("systemctl", "restart", "ppanel-node").CombinedOutput()
		if err != nil {
			return fmt.Errorf("重启失败（可能需要 root）: %v: %s", err, out)
		}
		fmt.Println("已重启。")
		return nil
	},
}

// GOARM 在编译期确定，运行时没有直接的变量；arm 以外的架构映射表里也不带后缀。
func goarmSuffix() string {
	if runtime.GOARCH != "arm" {
		return ""
	}
	return "7" // release 只出 arm32-v7a
}

func init() {
	upgradeCommand.Flags().StringVar(&upgradeRepo, "repo", "", "release 所在仓库（覆盖配置文件；留空用配置或内置默认）")
	upgradeCommand.Flags().StringVarP(&upgradeConfig, "config", "c", "/etc/ppanel-node/config.yml", "配置文件路径")
	upgradeCommand.Flags().BoolVar(&upgradeCheck, "check", false, "只检查有无新版本，不下载不替换")
	upgradeCommand.Flags().BoolVar(&upgradeRestart, "restart", false, "升级后自动重启 systemd 服务")
	command.AddCommand(&upgradeCommand)
}
