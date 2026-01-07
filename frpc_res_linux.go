//go:build linux

package main

import "embed"

// 仅打包 Linux 平台的架构（根据需要可增加 arm64）
//go:embed resources/bin/frpc_linux_amd64 resources/bin/frpc_linux_arm64
var frpcBin embed.FS

var frpcMap = map[string]string{
	"amd64": "resources/bin/frpc_linux_amd64",
	"arm64": "resources/bin/frpc_linux_arm64",
}

// Linux 下通常不需要扩展名
const frpcTargetName = "frpc"
