//go:build windows

package main

import "embed"

// 仅打包 Windows 平台的两个架构
//go:embed resources/bin/frpc_windows_amd64.exe resources/bin/frpc_windows_arm64.exe
var frpcBin embed.FS

// 定义各架构对应的文件名
var frpcMap = map[string]string{
	"amd64": "resources/bin/frpc_windows_amd64.exe",
	"arm64": "resources/bin/frpc_windows_arm64.exe",
}

const frpcTargetName = "frpc.exe"
