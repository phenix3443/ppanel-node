package node

import "testing"

// install.sh 把服务名和路径统一成了小写那套，这个常量当时漏了。
// 不一致的后果是双重的：证书写进一个谁也不管的目录，而且新 unit 的
// ProtectSystem=strict 只对小写目录放行，大写那个是只读 —— ACME 续期会失败。
func TestCertificateDirectoryMatchesInstalledLayout(t *testing.T) {
	if certificateDirectory != "/etc/ppanel-node" {
		t.Fatalf("certificateDirectory = %q，应与 install.sh 写的 /etc/ppanel-node 一致",
			certificateDirectory)
	}
}
