package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ax9000-flash/internal/flasher"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type Adapter struct {
	Name  string   `json:"name"`
	IPv4  []string `json:"ipv4"`
	Flags string   `json:"flags"`
}

type DefaultPaths struct {
	Model             string `json:"model"`
	ProgramDir        string `json:"programDir"`
	ToolsDir          string `json:"toolsDir"`
	UnlockTool        string `json:"unlockTool"`
	DeveloperFirmware string `json:"developerFirmware"`
	MIBIB             string `json:"mibib"`
	UBoot             string `json:"uboot"`
	Firmware          string `json:"firmware"`
	BackupBaseDir     string `json:"backupBaseDir"`
}

type WorkflowState struct {
	Model             string `json:"model"`
	LastStep          int    `json:"lastStep"`
	UBootWritten      bool   `json:"ubootWritten"`
	PhysicalConfirmed bool   `json:"physicalConfirmed"`
	FirmwareUploaded  bool   `json:"firmwareUploaded"`
	BackupDir         string `json:"backupDir"`
	UpdatedAt         string `json:"updatedAt"`
}

type App struct {
	ctx      context.Context
	mu       sync.Mutex
	stateMu  sync.Mutex
	cancel   context.CancelFunc
	running  bool
	critical bool
	logger   *slog.Logger
	logFile  *os.File
	logPath  string
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.initLog()
}

func (a *App) shutdown(context.Context) {
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
}

func (a *App) beforeClose(context.Context) bool {
	a.mu.Lock()
	critical := a.critical
	a.mu.Unlock()
	if critical {
		a.emitLog("危险写入尚未结束，已阻止关闭程序")
	}
	return critical
}

func (a *App) SelectFile(title string, patterns []string) (string, error) {
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:   title,
		Filters: []wruntime.FileFilter{{DisplayName: "可选文件", Pattern: strings.Join(patterns, ";")}},
	})
}

func (a *App) SelectDirectory(title string) (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: title})
}

func (a *App) GetDefaultPaths(model string) (DefaultPaths, error) {
	executable, err := os.Executable()
	if err != nil {
		return DefaultPaths{}, fmt.Errorf("读取程序目录失败：%w", err)
	}
	paths := resolveDefaultPaths(filepath.Dir(executable), model)
	if err := os.MkdirAll(paths.BackupBaseDir, 0755); err != nil {
		return DefaultPaths{}, fmt.Errorf("创建备份目录失败：%w", err)
	}
	return paths, nil
}

func resolveDefaultPaths(programDir, model string) DefaultPaths {
	model = strings.ToUpper(strings.TrimSpace(model))
	if model != "AX6000" {
		model = "AX9000"
	}
	toolsDir := filepath.Join(programDir, model)
	paths := DefaultPaths{
		Model:             model,
		ProgramDir:        programDir,
		ToolsDir:          toolsDir,
		UnlockTool:        filepath.Join(toolsDir, "ssh", "xmir-patcher", "run.bat"),
		DeveloperFirmware: filepath.Join(toolsDir, "firmware", "miwifi_ra70_developer.bin"),
		MIBIB:             filepath.Join(toolsDir, "bootloader", "mibib.bin"),
		UBoot:             filepath.Join(toolsDir, "bootloader", "uboot.bin"),
		Firmware:          filepath.Join(toolsDir, "firmware", "factory.ubi"),
		BackupBaseDir:     filepath.Join(toolsDir, "backups"),
	}
	if model == "AX6000" {
		paths.DeveloperFirmware = ""
		paths.MIBIB = ""
		paths.UBoot = filepath.Join(toolsDir, "bootloader", "mt7986_redmi_ax6000-fip-fixed-parts.bin")
		paths.Firmware = filepath.Join(toolsDir, "firmware", "kwrt-01.06.2026-mediatek-filogic-xiaomi_redmi-router-ax6000-squashfs-sysupgrade.bin")
	}
	return paths
}

func (a *App) InspectMaterials(model, developerFirmware, mibib, uboot, firmware, backupDir string) (flasher.Materials, error) {
	return flasher.InspectMaterials(model, developerFirmware, mibib, uboot, firmware, backupDir)
}

func (a *App) OpenURL(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("只允许打开 HTTP/HTTPS 地址")
	}
	a.emitLog("打开系统默认浏览器：" + url)
	if runtime.GOOS == "windows" {
		if err := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start(); err != nil {
			return fmt.Errorf("打开系统默认浏览器失败：%w", err)
		}
		return nil
	}
	wruntime.BrowserOpenURL(a.ctx, url)
	return nil
}

func (a *App) LaunchUnlockTool(path string) error {
	if runtime.GOOS != "windows" {
		return errors.New("解锁工具仅支持 Windows")
	}
	path = strings.TrimSpace(path)
	if strings.ToLower(filepath.Ext(path)) != ".bat" {
		return errors.New("请选择教程资源包内的 run.bat")
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	a.emitLog("启动用户选择的 SSH 解锁工具：" + path)
	command := exec.Command("cmd.exe", "/c", "start", "", filepath.Base(path))
	command.Dir = filepath.Dir(path)
	return command.Start()
}

func (a *App) GetAdapters() ([]Adapter, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]Adapter, 0, len(interfaces))
	for _, item := range interfaces {
		addresses, _ := item.Addrs()
		var ipv4 []string
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil {
				ipv4 = append(ipv4, ip.String())
			}
		}
		result = append(result, Adapter{Name: item.Name, IPv4: ipv4, Flags: item.Flags.String()})
	}
	return result, nil
}

func (a *App) InspectRouter(cfg flasher.SSHConfig) (flasher.RouterInfo, error) {
	ctx, cancel, err := a.beginTask(20 * time.Second)
	if err != nil {
		return flasher.RouterInfo{}, err
	}
	defer a.finishTask(cancel)
	a.emitLog("正在通过 SSH 核对设备型号和分区")
	info, err := flasher.InspectRouter(ctx, cfg)
	if err != nil {
		a.emitLog("SSH 检查失败：" + err.Error())
		return info, err
	}
	a.emitLog(fmt.Sprintf("设备校验通过：%s（%s）", info.Model, info.ModelKey))
	return info, nil
}

func (a *App) AutoUnlockSSH(model, runBat, developerFirmware, host, webPassword string) (flasher.RouterInfo, error) {
	ctx, cancel, err := a.beginTask(25 * time.Minute)
	if err != nil {
		return flasher.RouterInfo{}, err
	}
	defer a.finishTask(cancel)
	a.setCritical(true)
	defer a.setCritical(false)
	stock, err := flasher.InspectXiaomi(ctx, host)
	if err != nil {
		return flasher.RouterInfo{}, err
	}
	model = strings.ToUpper(strings.TrimSpace(model))
	expectedHardware := "RA70"
	if model == "AX6000" {
		expectedHardware = "RB06"
	}
	if !strings.EqualFold(stock.Model, expectedHardware) {
		return flasher.RouterInfo{}, fmt.Errorf("选择的是 %s，但检测到的设备型号是 %s", model, stock.Model)
	}
	a.emitLog(fmt.Sprintf("已识别 %s/%s，原厂固件版本 %s", model, expectedHardware, stock.Version))
	sshCfg := flasher.SSHConfig{Host: host, Username: "root", Password: "root"}
	if info, inspectErr := flasher.InspectRouter(ctx, sshCfg); inspectErr == nil {
		if info.ModelKey != model {
			return flasher.RouterInfo{}, fmt.Errorf("SSH 设备识别为 %s，与所选 %s 不一致", info.ModelKey, model)
		}
		a.emitLog("SSH 已经可用，无需重复解锁")
		return info, nil
	}
	tryUnlock := func() (flasher.RouterInfo, error) {
		a.emitLog("开始自动调用 XMiR 解锁 SSH，无需操作命令行窗口")
		if unlockErr := flasher.RunXMiRUnlock(ctx, runBat, host, webPassword, a.emitLog); unlockErr != nil {
			return flasher.RouterInfo{}, unlockErr
		}
		return flasher.WaitForSSH(ctx, sshCfg, 90*time.Second, a.emitLog)
	}
	info, unlockErr := tryUnlock()
	if unlockErr == nil {
		if info.ModelKey != model {
			return flasher.RouterInfo{}, fmt.Errorf("SSH 设备识别为 %s，与所选 %s 不一致", info.ModelKey, model)
		}
		a.emitLog("SSH 自动解锁成功")
		return info, nil
	}
	if errors.Is(unlockErr, flasher.ErrXMiRRuntime) {
		return flasher.RouterInfo{}, unlockErr
	}
	if model == "AX6000" {
		a.emitLog("当前固件直接解锁失败，按教程切换到红米 AX6000 1.2.8 固件后重试")
		if _, firmwareErr := flasher.InstallAX6000Firmware(ctx, host, webPassword, developerFirmware, a.emitLog); firmwareErr != nil {
			return flasher.RouterInfo{}, fmt.Errorf("自动安装 AX6000 1.2.8 固件失败：%w；首次解锁错误：%v", firmwareErr, unlockErr)
		}
	} else {
		a.emitLog("当前固件直接解锁失败，自动切换到教程开发版后重试")
		if _, firmwareErr := flasher.InstallDeveloperFirmware(ctx, host, webPassword, developerFirmware, a.emitLog); firmwareErr != nil {
			return flasher.RouterInfo{}, fmt.Errorf("自动安装开发版固件失败：%w；首次解锁错误：%v", firmwareErr, unlockErr)
		}
	}
	info, err = tryUnlock()
	if err != nil {
		return flasher.RouterInfo{}, fmt.Errorf("开发版固件下自动解锁仍然失败：%w", err)
	}
	a.emitLog("指定固件下 SSH 自动解锁成功")
	return info, nil
}

func (a *App) BackupRouter(cfg flasher.SSHConfig, baseDir string) (flasher.BackupResult, error) {
	ctx, cancel, err := a.beginTask(45 * time.Minute)
	if err != nil {
		return flasher.BackupResult{}, err
	}
	defer a.finishTask(cancel)
	a.emitLog("开始读取当前路由器的全部物理 MTD 分区；备份过程不会写入路由器")
	result, err := flasher.BackupRouter(ctx, cfg, baseDir, a.emitLog)
	if err != nil {
		a.emitLog("路由器备份失败：" + err.Error())
		return result, err
	}
	a.emitLog("备份清单：" + result.ManifestPath)
	return result, nil
}

func (a *App) FlashUBoot(input flasher.FlashInput) (flasher.FlashResult, error) {
	ctx, cancel, err := a.beginTask(8 * time.Minute)
	if err != nil {
		return flasher.FlashResult{}, err
	}
	defer a.finishTask(cancel)
	a.setCritical(true)
	defer a.setCritical(false)
	a.emitLog("开始上传并写入 U-Boot，引导分区写入期间严禁断电")
	result, err := flasher.FlashUBoot(ctx, input, a.emitLog)
	if err != nil {
		a.emitLog("U-Boot 写入失败：" + err.Error())
		return result, err
	}
	a.emitLog(result.Message)
	return result, nil
}

func (a *App) ProbeWeb(address string) (flasher.ProbeResult, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	return flasher.ProbeWeb(ctx, address)
}

func (a *App) WaitForUBoot(address string) (flasher.ProbeResult, error) {
	ctx, cancel, err := a.beginTask(3 * time.Minute)
	if err != nil {
		return flasher.ProbeResult{}, err
	}
	defer a.finishTask(cancel)
	a.emitLog("自动检测 U-Boot 上传页面，最长等待 2 分钟")
	return flasher.WaitForUBoot(ctx, address, 2*time.Minute, a.emitLog)
}

func (a *App) WaitForOpenWrt(model string) (flasher.ProbeResult, error) {
	ctx, cancel, err := a.beginTask(10 * time.Minute)
	if err != nil {
		return flasher.ProbeResult{}, err
	}
	defer a.finishTask(cancel)
	a.emitLog("按页面状态等待 OpenWrt 启动，最长 8 分钟")
	addresses := []string{"http://192.168.1.1"}
	if strings.EqualFold(model, "AX6000") {
		addresses = []string{"http://10.0.0.1"}
	}
	result, err := flasher.WaitForOpenWrtAny(ctx, addresses, 8*time.Minute, a.emitLog)
	if err != nil {
		a.emitLog(err.Error())
		return result, err
	}
	a.emitLog(result.Message)
	return result, nil
}

func (a *App) SetStaticIP(adapter, model string) error {
	if runtime.GOOS != "windows" {
		return errors.New("自动设置网卡仅支持 Windows")
	}
	if err := a.validateAdapter(adapter); err != nil {
		return err
	}
	address, target := "192.168.1.10", "192.168.1.1"
	if strings.EqualFold(model, "AX6000") {
		address, target = "192.168.31.2", "192.168.31.1"
	}
	if err := validateUBootNetwork(adapter, target); err != nil {
		return err
	}
	a.emitLog("设置网卡静态地址：" + adapter + " → " + address + "/24")
	if err := runNetsh("interface", "ipv4", "set", "address", "name="+adapter, "static", address, "255.255.255.0", "gateway=none"); err != nil {
		return err
	}
	return waitAdapterIPv4(adapter, address, 10*time.Second)
}

func validateUBootNetwork(selected, targetAddress string) error {
	target := net.ParseIP(targetAddress)
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, item := range interfaces {
		if item.Name == selected || item.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, _ := item.Addrs()
		for _, address := range addresses {
			ip, network, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsLinkLocalUnicast() && network.Contains(target) {
				return fmt.Errorf("网卡 %s（%s）与 U-Boot 地址 %s 冲突，请先断开该网卡或 WiFi", item.Name, address.String(), targetAddress)
			}
		}
	}
	return nil
}

func (a *App) SetDHCP(adapter string) error {
	if runtime.GOOS != "windows" {
		return errors.New("自动恢复网卡仅支持 Windows")
	}
	if err := a.validateAdapter(adapter); err != nil {
		return err
	}
	a.emitLog("恢复网卡自动获取 IP：" + adapter)
	enabled, checkErr := isDHCPEnabled(adapter)
	if checkErr == nil && enabled {
		a.emitLog("网卡已经启用 DHCP，无需重复设置 IP")
	} else {
		if err := runNetsh("interface", "ipv4", "set", "address", "name="+adapter, "source=dhcp"); err != nil {
			enabled, verifyErr := isDHCPEnabled(adapter)
			if verifyErr != nil || !enabled {
				return err
			}
			a.emitLog("netsh 返回非零状态，但已复核网卡 DHCP 确实启用")
		}
	}
	return runNetsh("interface", "ipv4", "set", "dnsservers", "name="+adapter, "source=dhcp")
}

func (a *App) SetOpenWrtIP(adapter, firmwarePath string) error {
	if runtime.GOOS != "windows" {
		return errors.New("自动设置网卡仅支持 Windows")
	}
	if err := a.validateAdapter(adapter); err != nil {
		return err
	}
	address := "192.168.1.2"
	if strings.Contains(strings.ToLower(filepath.Base(firmwarePath)), "kwrt") {
		address = "10.0.0.2"
	}
	a.emitLog("设置 OpenWrt 检测地址：" + adapter + " → " + address + "/24")
	return runNetsh("interface", "ipv4", "set", "address", "name="+adapter, "static", address, "255.255.255.0", "gateway=none")
}

func (a *App) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.critical {
		return
	}
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *App) setCritical(value bool) {
	a.mu.Lock()
	a.critical = value
	a.mu.Unlock()
}

func (a *App) GetLogPath() string { return a.logPath }

func (a *App) LoadWorkflowState() (WorkflowState, error) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	path, err := workflowStatePath()
	if err != nil {
		return WorkflowState{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return WorkflowState{}, nil
	}
	if err != nil {
		return WorkflowState{}, err
	}
	var state WorkflowState
	if err := json.Unmarshal(data, &state); err != nil {
		return WorkflowState{}, fmt.Errorf("读取上次刷机进度失败：%w", err)
	}
	return state, nil
}

func (a *App) SaveWorkflowState(state WorkflowState) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	path, err := workflowStatePath()
	if err != nil {
		return err
	}
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (a *App) ClearWorkflowState() error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	path, err := workflowStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func workflowStatePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "AX9000自动刷机助手")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "workflow-state.json"), nil
}

func (a *App) TakeAutoRunPassword() string {
	for _, arg := range os.Args[1:] {
		const prefix = "--autorun-file="
		if !strings.HasPrefix(arg, prefix) {
			continue
		}
		path := filepath.Clean(strings.TrimPrefix(arg, prefix))
		if filepath.Dir(path) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(path), "ax9000-autorun-") || filepath.Ext(path) != ".token" {
			return ""
		}
		data, err := os.ReadFile(path)
		_ = os.Remove(path)
		if err != nil || len(data) > 256 {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	password := os.Getenv("AX9000_AUTORUN_PASSWORD")
	_ = os.Unsetenv("AX9000_AUTORUN_PASSWORD")
	return password
}

func (a *App) TakeAutoRunModel() string {
	for _, arg := range os.Args[1:] {
		const prefix = "--autorun-model="
		if strings.HasPrefix(arg, prefix) {
			return normalizeModel(strings.TrimPrefix(arg, prefix))
		}
	}
	model := normalizeModel(os.Getenv("ROUTER_AUTORUN_MODEL"))
	_ = os.Unsetenv("ROUTER_AUTORUN_MODEL")
	return model
}

func normalizeModel(model string) string {
	if strings.EqualFold(strings.TrimSpace(model), "AX6000") {
		return "AX6000"
	}
	if strings.EqualFold(strings.TrimSpace(model), "AX9000") {
		return "AX9000"
	}
	return ""
}

func (a *App) beginTask(timeout time.Duration) (context.Context, context.CancelFunc, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil, nil, errors.New("已有任务正在运行")
	}
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	a.running, a.cancel = true, cancel
	return ctx, cancel, nil
}

func (a *App) finishTask(cancel context.CancelFunc) {
	cancel()
	a.mu.Lock()
	a.running, a.cancel = false, nil
	a.mu.Unlock()
}

func (a *App) validateAdapter(name string) error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, item := range interfaces {
		if item.Name == name {
			return nil
		}
	}
	return errors.New("未找到所选网卡")
}

func runNetsh(args ...string) error {
	output, err := exec.Command("netsh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("设置网卡失败，请以管理员身份运行：%w；%s", err, decodeWindowsOutput(output))
	}
	return nil
}

func isDHCPEnabled(adapter string) (bool, error) {
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "[int](Get-NetIPInterface -InterfaceAlias $env:ROUTER_FLASH_ADAPTER -AddressFamily IPv4).Dhcp")
	command.Env = append(os.Environ(), "ROUTER_FLASH_ADAPTER="+adapter)
	output, err := command.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) == "1", nil
}

func decodeWindowsOutput(data []byte) string {
	if utf8.Valid(data) {
		return strings.TrimSpace(string(data))
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(data)
	if err != nil {
		return strings.TrimSpace(string(data))
	}
	return strings.TrimSpace(string(decoded))
}

func waitAdapterIPv4(adapter, expected string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		interfaces, err := net.Interfaces()
		if err == nil {
			for _, item := range interfaces {
				if item.Name != adapter {
					continue
				}
				addresses, _ := item.Addrs()
				for _, address := range addresses {
					ip, _, parseErr := net.ParseCIDR(address.String())
					if parseErr == nil && ip.String() == expected {
						return nil
					}
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("静态 IP 设置命令已执行，但网卡 %s 未出现地址 %s", adapter, expected)
}

func (a *App) initLog() {
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	dir := filepath.Join(base, "AX9000自动刷机助手", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	a.logPath = filepath.Join(dir, time.Now().Format("2006-01-02")+".log")
	file, err := os.OpenFile(a.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	a.logFile = file
	a.logger = slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a.emitLog("程序启动")
}

func (a *App) emitLog(message string) {
	if a.logger != nil {
		a.logger.Info(message)
	}
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, "task-log", message)
	}
}
