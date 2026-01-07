//go:build !windows

package main

import "syscall"

// 在非 Windows 平台，这个函数什么都不做
func setHideWindow(attr *syscall.SysProcAttr) {
	// 留空
}
