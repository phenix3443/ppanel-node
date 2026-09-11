package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

// 资产名必须和 release.yml 用的同一张表（.github/build/friendly-filenames.json）
// 对齐。手写一份映射迟早漂移，所以这里验的是「读的就是那张表」。
func TestAssetNameMatchesReleaseWorkflowTable(t *testing.T) {
	cases := map[[3]string]string{
		{"linux", "amd64", ""}:  "ppanel-node-linux-64.zip",
		{"linux", "arm64", ""}:  "ppanel-node-linux-arm64-v8a.zip",
		{"linux", "arm", "7"}:   "ppanel-node-linux-arm32-v7a.zip",
		{"darwin", "arm64", ""}: "ppanel-node-macos-arm64-v8a.zip",
	}
	for in, want := range cases {
		got, err := AssetName(in[0], in[1], in[2])
		if err != nil {
			t.Errorf("AssetName(%v) 报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("AssetName(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestAssetNameRejectsUnknownPlatform(t *testing.T) {
	if _, err := AssetName("plan9", "sparc64", ""); err == nil {
		t.Fatal("未知平台应当报错，而不是拼出一个不存在的文件名")
	}
}

// .dgst 是 OpenSSL 3.x 的输出，键名是 SHA2-256 而不是 SHA256，等号前没有空格。
// 这个格式我是下了真文件核对的——按 SHA256 去找会一无所获。
func TestParseSHA256HandlesOpenSSL3Naming(t *testing.T) {
	dgst := `MD5= e8a09fdec5c63991ab6b738dea48e106
SHA1= 49c02e6ffdf4286dae55eb7e4742256b48f187d3
SHA2-256= 36a7e8c90f2c007b9b17584c74f6b1aac67be88388f91073a84da67cb9e04a5c
SHA2-512= 3defbafa60e1640d795a589db4d775c105caae08d58bf36a0d2a1f085199377cd1f60edb8bc0040ec013d220bdcf4d788e6955737845c113f5dac9e51bbfd095
`
	got, err := ParseSHA256([]byte(dgst))
	if err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	want := "36a7e8c90f2c007b9b17584c74f6b1aac67be88388f91073a84da67cb9e04a5c"
	if got != want {
		t.Fatalf("ParseSHA256 = %q, want %q", got, want)
	}
}

// 校验和拿不到时必须报错。静默跳过校验等于把「升级」变成「从网上抓个文件就执行」。
func TestParseSHA256FailsLoudlyWhenAbsent(t *testing.T) {
	for _, in := range []string{"", "MD5= abc\n", "SHA2-512= dead\n", "SHA2-256=\n"} {
		if _, err := ParseSHA256([]byte(in)); err == nil {
			t.Errorf("%q 应当报错", in)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               bool
	}{
		{"v1.1.13", "v1.1.14", true},
		{"v1.1.14", "v1.1.14", false},
		{"v1.1.14", "v1.1.13", false},
		{"v1.1.9", "v1.1.10", true},      // 字典序会判错，必须按数字比
		{"v1.2.0", "v1.10.0", true},      // 同上
		{"TempVersion", "v1.1.14", true}, // 未注入版本的本地构建，一律视为可升级
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.candidate); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.candidate, got, c.want)
		}
	}
}

// 仓库来源有三层：命令行 > 配置文件 > 默认值。
// 弄错优先级会让「改了配置却不生效」，而且不报错。
func TestResolveRepoPrecedence(t *testing.T) {
	cases := []struct{ flag, cfg, want string }{
		{"a/b", "c/d", "a/b"},  // 命令行最高
		{"", "c/d", "c/d"},     // 退到配置
		{"", "", DefaultRepo},  // 都没有用默认
		{"  ", " c/d ", "c/d"}, // 空白要当成没填，并去掉两端空格
	}
	for _, c := range cases {
		if got := ResolveRepo(c.flag, c.cfg); got != c.want {
			t.Errorf("ResolveRepo(%q, %q) = %q, want %q", c.flag, c.cfg, got, c.want)
		}
	}
}

// 控制台下发的期望版本可能比当前更旧——新版出问题时要能回退，
// 回退和升级走同一条路。所以判据是「不等」，不是「更新」。
func TestShouldSwitchVersion(t *testing.T) {
	cases := []struct {
		current, target string
		want            bool
		why             string
	}{
		{"v1.1.13", "v1.1.14", true, "升级"},
		{"v1.1.14", "v1.1.13", true, "降级——必须也返回 true"},
		{"v1.1.14", "v1.1.14", false, "已是目标版本"},
		{"v1.1.14", "", false, "空串=控制台不干预"},
		{"", "v1.1.14", true, "本地版本未知时按目标版本装一次"},
		{"v1.1.14", " v1.1.14 ", false, "两端空格不该被当成不同版本"},
	}
	for _, c := range cases {
		if got := ShouldSwitchVersion(c.current, c.target); got != c.want {
			t.Errorf("ShouldSwitchVersion(%q, %q) = %v, want %v —— %s", c.current, c.target, got, c.want, c.why)
		}
	}
}

// 失败后要退避，但【不能永久放弃】：只试一次的话，一次网络抖动就会把这个
// 目标版本永久钉死——运维把期望版本清空再设回同一个值也不会重试。
func TestApplyTargetVersionBacksOffButResumesOnTargetChange(t *testing.T) {
	ResetAttemptForTest()
	restart := func() error { return nil }
	const bad = "phenix3443/definitely-not-a-repo-xyz"

	if err := ApplyTargetVersion("v1.1.13", "v9.9.9", bad, restart); err == nil {
		t.Fatal("第一次应当报错")
	}
	// 退避窗口内直接跳过
	if err := ApplyTargetVersion("v1.1.13", "v9.9.9", bad, restart); err != nil {
		t.Fatalf("退避期内应当静默跳过，得到 %v", err)
	}
	// 【关键】换一个目标（含清空后改回来）必须重新开始尝试，不能被钉死
	if err := ApplyTargetVersion("v1.1.13", "v9.9.8", bad, restart); err == nil {
		t.Fatal("换了目标版本应当重新尝试（并再次报错），而不是被退避挡住")
	}
}

func TestApplyTargetVersionDoesNotRetrySameTarget(t *testing.T) {
	ResetAttemptForTest()
	var restarts int
	restart := func() error { restarts++; return nil }

	// 用一个必定取不到 release 的仓库，让第一次就失败
	err1 := ApplyTargetVersion("v1.1.13", "v9.9.9", "phenix3443/definitely-not-a-repo-xyz", restart)
	if err1 == nil {
		t.Fatal("第一次应当报错")
	}
	err2 := ApplyTargetVersion("v1.1.13", "v9.9.9", "phenix3443/definitely-not-a-repo-xyz", restart)
	if err2 != nil {
		t.Fatalf("同一目标第二次应当直接跳过，得到 %v", err2)
	}
	if restarts != 0 {
		t.Fatalf("失败路径不该重启服务，重启了 %d 次", restarts)
	}
}

// 控制台没设期望版本，或已经是目标版本时，什么都不该发生。
func TestApplyTargetVersionNoopCases(t *testing.T) {
	ResetAttemptForTest()
	var restarts int
	restart := func() error { restarts++; return nil }
	for _, c := range [][2]string{{"v1.1.14", ""}, {"v1.1.14", "v1.1.14"}} {
		if err := ApplyTargetVersion(c[0], c[1], "", restart); err != nil {
			t.Errorf("ApplyTargetVersion(%q,%q) 应当无操作，得到 %v", c[0], c[1], err)
		}
	}
	if restarts != 0 {
		t.Fatalf("无操作时不该重启，重启了 %d 次", restarts)
	}
}

// 【先验证再替换】换坏了的代价极高：节点起不来就拉不到配置，也就再也收不到
// 「换回旧版本」的指令，只能人上机器。所以在动现役二进制之前，先把下载来的
// 那个跑一次 `version`，确认它能执行且自报的版本就是目标版本。
// 这能挡住架构不匹配（armv6 拿到 v7a 包会 SIGILL）和资产损坏。
func TestVerifyDownloadedBinaryRejectsNonExecutable(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "notabinary")
	if err := os.WriteFile(bad, []byte("#!/nonexistent\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBinary(bad, "v1.1.14"); err == nil {
		t.Fatal("跑不起来的文件应当被拒绝")
	}
}

func TestVerifyDownloadedBinaryRejectsWrongVersion(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake")
	// 一个能跑但自报别的版本的“二进制”
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'PPanel-node v0.0.1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBinary(fake, "v1.1.14"); err == nil {
		t.Fatal("自报版本与目标不符时应当被拒绝——否则会陷入每 60 秒一次的重启循环")
	}
}

func TestVerifyDownloadedBinaryAcceptsMatchingVersion(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\necho 'PPanel-node v1.1.14 (whatever)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBinary(ok, "v1.1.14"); err != nil {
		t.Fatalf("版本相符应当通过，得到 %v", err)
	}
}
