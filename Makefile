# 开发用的常用目标。CI 不依赖这个文件，它是给人用的。
GO ?= go

# 【版本必须钉死】api/server/v1 的生成产物是用 protoc-gen-go v1.36.11 出的，
# 换个版本重新生成会得到风格不同的一大片 diff，甚至不兼容的代码。
# 校验方式：不改 .proto 直接跑 make proto，git diff 应当只有 protoc 版本注释那一行。
PROTOC_GEN_GO_VERSION ?= v1.36.11

.PHONY: tools proto build test lint

## tools: 安装生成代码所需的工具
tools:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@command -v protoc >/dev/null || { \
		echo "还需要 protoc：macOS 用 brew install protobuf，Debian 系用 apt-get install -y protobuf-compiler"; \
		exit 1; }

## proto: 重新生成 api/server/v1 下的 pb.go
##
## 【两个仓库的 proto 必须同步】ppanel-node 和 ppanel-server 各存一份
## PushServerStatusRequest 等消息，字段号一一对应才能保持 wire 兼容。
## 改了一边一定要同样改另一边，并两边都重新生成。
proto: tools
	protoc --go_out=. --go_opt=paths=source_relative -I. api/server/v1/*.proto

build:
	$(GO) build ./...

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...
