//go:build windows

package main

import "syscall"

// 设置隐藏窗口属性
func setHideWindow(attr *syscall.SysProcAttr) {
	if attr == nil {
		return
	}
	attr.HideWindow = true
}
