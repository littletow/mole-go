//go:build darwin

package main

import "embed"

// 仅打包 macOS 平台的两个架构
//go:embed resources/bin/frpc_darwin_amd64 resources/bin/frpc_darwin_arm64
var frpcBin embed.FS

var frpcMap = map[string]string{
	"amd64": "resources/bin/frpc_darwin_amd64",
	"arm64": "resources/bin/frpc_darwin_arm64",
}

const frpcTargetName = "frpc"
