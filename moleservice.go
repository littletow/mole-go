package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type ServiceStatus struct {
	Success   bool        `json:"success"`
	IsRunning bool        `json:"isRunning"`
	Config    *UserConfig `json:"config"` // 关键：记录是否已完成配置
	Message   string      `json:"message"`
}

type MoleService struct {
	// --- 核心生命周期 ---
	ctx      context.Context
	initWait chan struct{}
	mu       sync.RWMutex

	// --- 连接与配置 ---
	config *UserConfig

	// --- 状态标识 (使用原子操作减少锁竞争) ---
	isRunning atomic.Bool // 仅记录 frp 进程是否在后台运行

	// --- FRP 进程管理 ---
	frpCmd *exec.Cmd

	// --- 日志缓冲区 ---
	logMu     sync.Mutex
	logBuffer []string // 建议在初始化时 make([]string, 0, 128)

}

type UserConfig struct {
	// --- 元数据与版本控制 ---
	ConfigVersion string `toml:"config_version" json:"-"` // 配置文件的格式版本 (如 "1.0.0")
	LastUpdated   string `toml:"last_updated" json:"-"`   // ISO时间戳，方便排查用户问题

	// --- 服务端全局连接信息 ---
	Server struct {
		Addr      string `toml:"addr" json:"addr"`
		Port      int    `toml:"port" json:"port"`
		Token     string `toml:"token" json:"token"`
		Remark    string `toml:"remark" json:"remark"`        // 用户给这台服务器起的别名
		AutoStart bool   `toml:"auto_start" json:"autoStart"` // 软件启动时是否自动开启穿透
	} `toml:"server" json:"server"`

	// --- 代理规则详情 (限制最大3条) ---
	// 使用 Slice 存储，方便前端循环渲染
	Proxies []ProxyRule `toml:"proxies" json:"proxies"`
}

type ProxyRule struct {
	ID        string `toml:"id" json:"id"`                // 前端生成唯一ID (UUID或随机串)，删除修改定位用
	Enabled   bool   `toml:"enabled" json:"enabled"`      // 是否启用当前代理
	ProxyType string `toml:"proxy_type" json:"proxyType"` // "http", "tcp", "udp"
	Name      string `toml:"name" json:"name"`            // 代理名称 (生成的frpc中的proxyName)

	// 局域网内目标
	LocalIP   string `toml:"local_ip" json:"localIP"` // 默认 127.0.0.1
	LocalPort int    `toml:"local_port" json:"localPort"`

	// 远程暴露参数
	RemotePort int      `toml:"remote_port,omitempty" json:"remotePort"` // TCP/UDP 必填
	Domains    []string `toml:"domains,omitempty" json:"domains"`        // HTTP 必填，使用数组方便以后扩展多域名
}

func NewMoleService() *MoleService {
	return &MoleService{
		initWait: make(chan struct{}),
		// 预分配 128 条日志空间，避免启动时频繁内存分配
		logBuffer: make([]string, 0, 128),
	}
}

// 实现了wails服务接口，启动后调用
func (s *MoleService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.ctx = ctx

	// 执行初始化任务
	go func() {
		defer close(s.initWait) // 无论加载成败，完成后必须关闭 channel

		if err := s.loadConfigFromDisk(); err != nil {
			log.Println("加载本地配置失败: " + err.Error())
			return
		}

		// 如果开启了自动启动，且配置存在，则启动
		s.mu.RLock()
		if s.config != nil && s.config.Server.AutoStart {
			s.mu.RUnlock()
			log.Println("检测到自动启动已开启，准备建立隧道...")
			s.startFrp()
		} else {
			s.mu.RUnlock()
		}
	}()

	return nil
}

func (s *MoleService) loadConfigFromDisk() error {
	configPath := filepath.Join(s.getAppConfigDir(), "config.toml")

	// 1. 检查文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return err
	}

	// 2. 读取文件内容
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	// 3. 解析 TOML
	var loadedConfig UserConfig
	err = toml.Unmarshal(data, &loadedConfig)
	if err != nil {
		// 如果解析失败（如文件损坏），建议返回错误，防止程序带着错误配置运行
		return err
	}

	s.config = &loadedConfig

	return nil
}

func (s *MoleService) SaveUserConfig(newCfg UserConfig) error {
	// 加锁防止修改时读取
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 更新内存状态
	s.config = &newCfg
	s.config.ConfigVersion = "1.0.0" // 当前版本，不添加自动更新，这个版本仅用于配置变更时升级使用
	s.config.LastUpdated = time.Now().Format(time.RFC3339)

	// 2. 序列化并保存到磁盘 (userConfig.toml)
	configPath := filepath.Join(s.getAppConfigDir(), "config.toml")
	data, err := toml.Marshal(s.config)
	if err != nil {
		return fmt.Errorf("配置文件格式化失败: %v", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("保存文件失败: %v", err)
	}

	// 3. 同时触发生成运行所需的 frpc.toml
	return s.generateFrpcToml()
}

func (s *MoleService) generateFrpcToml() error {
	if s.config == nil {
		return fmt.Errorf("未发现有效配置")
	}

	// 构建符合 frp 0.65 规范的结构
	// 注意：根据 2026 年 frp 最佳实践，我们直接构建 map 以方便 Marshal 为 TOML
	runCfg := make(map[string]any)

	// A. 服务端公共配置
	runCfg["serverAddr"] = s.config.Server.Addr
	runCfg["serverPort"] = s.config.Server.Port
	// B. 构建嵌套的 auth 结构
	authCfg := make(map[string]string)
	authCfg["method"] = "token"
	authCfg["token"] = s.config.Server.Token
	runCfg["auth"] = authCfg // 将子 map 放入主 map
	// C. 代理列表映射
	var proxies []map[string]any
	for _, p := range s.config.Proxies {
		if !p.Enabled {
			continue
		}

		item := map[string]any{
			"name":      p.Name,
			"type":      p.ProxyType,
			"localIP":   p.LocalIP,
			"localPort": p.LocalPort,
		}

		// 根据类型按需添加字段
		if p.ProxyType == "http" {
			item["customDomains"] = p.Domains
		} else {
			item["remotePort"] = p.RemotePort
		}
		proxies = append(proxies, item)
	}
	runCfg["proxies"] = proxies

	// D. 写入 frpc.toml 文件
	frpcPath := filepath.Join(s.getFrpBinDir(), "frpc.toml")
	out, err := toml.Marshal(runCfg)
	if err != nil {
		return err
	}

	return os.WriteFile(frpcPath, out, 0644)
}

func (s *MoleService) cleanup() {
	log.Println("退出前清理资源")
	// 1，关闭frp
	s.stopFrp()
}

// Connect 供前端调用的主方法
func (s *MoleService) Connect() ServiceStatus {
	s.mu.RLock()
	// 1. 检查配置状态
	if s.config == nil {
		s.mu.RUnlock()
		return ServiceStatus{
			Success:   false,
			IsRunning: false,
			Config:    nil,
			Message:   "错误：未发现有效配置。请先前往配置页保存服务器信息。",
		}
	}
	s.mu.RUnlock()

	// 2. 检查运行状态 (防止重复启动)
	if s.isRunning.Load() {
		return ServiceStatus{
			Success:   true,
			IsRunning: true,
			Config:    s.config,
			Message:   "提示：隧道已在运行中，无需重复连接。",
		}
	}

	// 3. 尝试异步启动进程
	go s.startFrp()

	return ServiceStatus{
		Success:   true,
		IsRunning: false, // 此时还在启动中，由事件通知后续状态
		Config:    s.config,
		Message:   "启动中...",
	}
}

func (s *MoleService) Disconnect() ServiceStatus {
	// 1. 检查是否真的在运行
	if !s.isRunning.Load() {
		return ServiceStatus{
			Success:   true,
			IsRunning: false,
			Message:   "服务本就处于停止状态",
		}
	}

	// 2. 停止进程逻辑
	if s.frpCmd != nil && s.frpCmd.Process != nil {
		// 在 Windows 下建议使用 TaskKill 或发送 Ctrl+C，这里使用跨平台最直接的 Kill
		err := s.frpCmd.Process.Kill()
		if err != nil {
			return ServiceStatus{
				Success:   false,
				IsRunning: true,
				Message:   "停止进程失败: " + err.Error(),
			}
		}
	}

	// 3. 更新状态
	s.isRunning.Store(false)
	s.emitLog("用户手动断开连接")

	return ServiceStatus{
		Success:   true,
		IsRunning: false,
		Message:   "服务已断开",
	}
}

func (s *MoleService) GetStatus() ServiceStatus {
	// 等待初始化完成（如果已经关闭，会立即通过）
	<-s.initWait

	s.mu.RLock()
	defer s.mu.RUnlock()

	return ServiceStatus{
		Success:   true,
		IsRunning: s.isRunning.Load(),
		Config:    s.config,
		Message:   s.getRunningSummary(), // 辅助方法返回简报
	}
}

// 辅助方法：生成当前状态的文字描述
func (s *MoleService) getRunningSummary() string {
	if s.isRunning.Load() {
		return "FRP 服务正在运行中"
	}
	if s.config == nil {
		return "尚未完成基础配置"
	}
	return "服务待命中"
}

// =====================核心逻辑，启动FRP ===============================

func (s *MoleService) getFrpBinDir() string {
	// 获取系统标准的配置/数据目录
	baseDir, _ := os.UserConfigDir()
	// 创建一个属于你应用的专用子目录
	// Windows: AppData/Roaming/MoleApp/bin
	// macOS: Library/Application Support/MoleApp/bin
	appBinDir := filepath.Join(baseDir, "mole", "bin")

	// 确保目录一定存在
	_ = os.MkdirAll(appBinDir, 0755)
	return appBinDir
}

func (s *MoleService) getAppConfigDir() string {
	// 获取系统标准的配置/数据目录
	baseDir, _ := os.UserConfigDir()
	// 创建一个属于你应用的专用子目录
	// Windows: AppData/Roaming/MoleApp/bin
	// macOS: Library/Application Support/MoleApp/bin
	appConfigDir := filepath.Join(baseDir, "mole", "config")

	log.Printf("配置目录: %v", appConfigDir)

	// 确保目录一定存在
	_ = os.MkdirAll(appConfigDir, 0755)
	return appConfigDir
}

func (s *MoleService) prepareFrpEnv() (string, string, error) {
	binDir := s.getFrpBinDir()

	// 1. 确定 frpc 路径
	frpcPath := filepath.Join(binDir, frpcTargetName) // frpcTargetName 是在条件编译文件里定义的名称

	// 如果文件不存在则从 embed 释放
	if _, err := os.Stat(frpcPath); os.IsNotExist(err) {
		arch := runtime.GOARCH
		data, err := frpcBin.ReadFile(frpcMap[arch])
		if err != nil {
			return "", "", err
		}
		if err := os.WriteFile(frpcPath, data, 0755); err != nil {
			return "", "", err
		}
	}

	// 2. 确定 toml 路径并写入
	tomlPath := filepath.Join(binDir, "frpc.toml")
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		s.generateFrpcToml()
	}

	return frpcPath, tomlPath, nil
}

func (s *MoleService) startFrp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 1. 防抖：如果已经启动，直接返回
	if s.isRunning.Load() {
		return
	}
	frpcPath, tomlPath, err := s.prepareFrpEnv()
	if err != nil {
		log.Printf("准备 FRP 环境失败: %v", err)
		return
	}
	// 启动前生成或覆盖最新的 frpc.toml
	err = s.generateFrpcToml()
	if err != nil {
		log.Printf("配置生成失败: %v", err)
		return
	}
	// 如果已经在运行，先停止
	if s.frpCmd != nil && s.frpCmd.Process != nil {
		s.stopFrp()
	}
	s.isRunning.Store(false) // 重置标记

	// 1. 创建命令
	s.frpCmd = exec.Command(frpcPath, "-c", tomlPath)

	s.frpCmd.SysProcAttr = &syscall.SysProcAttr{}
	setHideWindow(s.frpCmd.SysProcAttr) // 直接调用，编译器会根据平台自动选择对应的实现

	// 创建管道获取输出
	stdout, _ := s.frpCmd.StdoutPipe()
	stderr, _ := s.frpCmd.StderrPipe()

	// 启动一个定时器，每 500ms 检查一次缓存并发送
	ticker := time.NewTicker(500 * time.Millisecond)
	go func() {
		for {
			select {
			case <-ticker.C:
				s.flushLogs()
			case <-s.ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()

	// 2. 合并读取日志的函数
	readLog := func(reader io.ReadCloser) {
		// 关键点：函数结束时关闭 reader，确保系统资源释放
		defer reader.Close()

		scanner := bufio.NewScanner(reader)
		// 当进程退出，管道关闭时，Scan() 会自动返回 false，循环结束
		for scanner.Scan() {
			line := scanner.Text()

			s.logMu.Lock()
			s.logBuffer = append(s.logBuffer, line) // 将日志存入切片
			s.logMu.Unlock()
		}

		log.Println("日志协程正常退出")
	}

	// 启动进程
	if err := s.frpCmd.Start(); err != nil {
		// 发送通知到前端
		s.emitLog("frpc 进程启动失败：", err.Error())
		log.Printf("启动 frpc 失败: %v", err)
		return
	}

	s.emitFrpStatus("start")
	// 4. 关键：启动成功后立即设置 isStarted
	s.isRunning.Store(true)

	go readLog(stdout)
	go readLog(stderr)

	go func() {
		// Wait 会阻塞直到进程结束
		_ = s.frpCmd.Wait()

		s.mu.Lock()
		defer s.mu.Unlock()

		// 清理句柄并重置运行状态
		s.frpCmd = nil
		s.isRunning.Store(false)

		s.emitLog("警告：frpc 进程已退出")
		// 这里可以触发 Wails 事件通知前端 UI 变更为“停止”状态
		s.emitFrpStatus("stop")

	}()

	// 发送自定义事件，通知前端关闭弹窗
	log.Printf("frpc 已启动，PID: %d，配置文件: %s", s.frpCmd.Process.Pid, tomlPath)
}

func (s *MoleService) stopFrp() {
	s.mu.Lock()
	if s.frpCmd == nil || s.frpCmd.Process == nil {
		s.mu.Unlock()
		return
	}

	pid := strconv.Itoa(s.frpCmd.Process.Pid)
	s.mu.Unlock() // 先解锁，避免 taskkill 阻塞时占用锁

	if runtime.GOOS == "windows" {
		// Windows: /F 强制, /T 包含子进程, /PID 进程号
		cmd := exec.Command("taskkill", "/F", "/T", "/PID", pid)

		// 关键：在 Windows 下隐藏控制台窗口
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		setHideWindow(cmd.SysProcAttr) // 直接调用，编译器会根据平台自动选择对应的实现

		cmd.Run()
	} else {
		// Linux & macOS: 使用 kill -9 强制杀死
		// 注意：如果启动时没设进程组，这里杀的是主进程
		_ = exec.Command("kill", "-9", pid).Run()
	}

	s.isRunning.Store(false)
}

func (s *MoleService) flushLogs() {
	s.logMu.Lock()
	if len(s.logBuffer) == 0 {
		s.logMu.Unlock()
		return
	}

	// 拷贝并清空缓存
	logsToSend := make([]string, len(s.logBuffer))
	copy(logsToSend, s.logBuffer)
	s.logBuffer = s.logBuffer[:0]
	s.logMu.Unlock()

	s.emitLog(logsToSend...)
}

func (s *MoleService) emitLog(logs ...string) {
	if len(logs) == 0 {
		return
	}
	// 一次性发送数组，前端通过 v-for 循环渲染
	manager.App.Event.Emit("frp-logs", logs)
}

func (s *MoleService) emitFrpStatus(status string) {
	manager.App.Event.Emit("frp-status", status)
}
