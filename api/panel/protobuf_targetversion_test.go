package panel

import (
	"testing"

	serverv1 "github.com/perfect-panel/ppanel-node/api/server/v1"
)

// 节点拉配置时无条件请求 protobuf（见 setProtobufResponseAccept），
// 所以真实部署里走的永远是这条解码路径。控制台下发的期望版本必须能过来，
// 否则整套自动升级是死的——而且不会有任何报错。
func TestServerConfigFromProtobufCarriesTargetVersion(t *testing.T) {
	got := serverConfigResponseFromProtobuf(&serverv1.QueryServerProtocolConfigResponse{
		Code: 200,
		Data: &serverv1.QueryServerProtocolConfigData{
			PullInterval:  60,
			TargetVersion: "v1.1.14",
		},
	})
	if got == nil || got.Data == nil {
		t.Fatal("解码结果为空")
	}
	if got.Data.TargetVersion != "v1.1.14" {
		t.Fatalf("TargetVersion = %q, want v1.1.14", got.Data.TargetVersion)
	}
}
