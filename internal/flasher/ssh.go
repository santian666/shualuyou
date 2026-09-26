package flasher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type SSHConfig struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type RouterInfo struct {
	Model      string            `json:"model"`
	ModelKey   string            `json:"modelKey"`
	MTD        map[string]string `json:"mtd"`
	RawMTD     string            `json:"rawMtd"`
	Host       string            `json:"host"`
	Compatible bool              `json:"compatible"`
}

type FlashInput struct {
	Model     string    `json:"model"`
	SSH       SSHConfig `json:"ssh"`
	MIBIBPath string    `json:"mibibPath"`
	UBootPath string    `json:"ubootPath"`
}

type FlashResult struct {
	MIBIBSHA256 string `json:"mibibSHA256"`
	UBootSHA256 string `json:"ubootSHA256"`
	Message     string `json:"message"`
}

type BackupFile struct {
	Device string `json:"device"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type BackupResult struct {
	Directory    string       `json:"directory"`
	ManifestPath string       `json:"manifestPath"`
	Model        string       `json:"model"`
	Host         string       `json:"host"`
	Files        []BackupFile `json:"files"`
}

type backupManifest struct {
	CreatedAt string       `json:"createdAt"`
	Model     string       `json:"model"`
	Host      string       `json:"host"`
	RawMTD    string       `json:"rawMtd"`
	Files     []BackupFile `json:"files"`
}

var mtdLine = regexp.MustCompile(`(?m)^mtd(\d+):\s+([0-9a-fA-F]+)\s+[0-9a-fA-F]+\s+"([^"]+)"`)
var mtdEntryLine = regexp.MustCompile(`(?m)^mtd(\d+):\s+([0-9a-fA-F]+)\s+([0-9a-fA-F]+)\s+"([^"]+)"`)

func InspectRouter(ctx context.Context, cfg SSHConfig) (RouterInfo, error) {
	client, err := dialSSH(ctx, cfg)
	if err != nil {
		return RouterInfo{}, err
	}
	defer client.Close()
	info, _, err := readRouterInfo(client, cfg.Host)
	return info, err
}

func BackupRouter(ctx context.Context, cfg SSHConfig, baseDir string, logf func(string)) (BackupResult, error) {
	client, err := dialSSH(ctx, cfg)
	if err != nil {
		return BackupResult{}, err
	}
	defer client.Close()
	info, raw, err := readRouterInfo(client, cfg.Host)
	if err != nil {
		return BackupResult{}, err
	}
	entries := parseMTDEntries(raw)
	if len(entries) == 0 {
		return BackupResult{}, errors.New("未发现可备份的 MTD 分区")
	}
	physicalEntries := entries[:0]
	for _, entry := range entries {
		if entry.EraseSize == 0x1f000 {
			logf(fmt.Sprintf("跳过 UBI 虚拟卷 mtd%d %s；其数据已包含在物理 MTD 原始备份中", entry.Index, entry.Name))
			continue
		}
		physicalEntries = append(physicalEntries, entry)
	}
	entries = physicalEntries
	if strings.TrimSpace(baseDir) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return BackupResult{}, err
		}
		baseDir = filepath.Join(home, "Downloads", info.ModelKey+"刷机备份")
	}
	directory := filepath.Join(baseDir, time.Now().Format("20060102-150405")+"_"+safeName(info.Model))
	if err := os.MkdirAll(directory, 0755); err != nil {
		return BackupResult{}, fmt.Errorf("创建备份目录失败：%w", err)
	}
	result := BackupResult{Directory: directory, Model: info.Model, Host: cfg.Host}
	for index, entry := range entries {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		filename := fmt.Sprintf("mtd%02d_%s.bin", entry.Index, safeName(entry.Name))
		path := filepath.Join(directory, filename)
		logf(fmt.Sprintf("备份 %d/%d：mtd%d %s（%.1f MiB）", index+1, len(entries), entry.Index, entry.Name, float64(entry.Size)/1024/1024))
		hash, size, err := streamMTD(client, entry.Index, path)
		if err != nil {
			_ = os.Remove(path)
			return result, fmt.Errorf("备份 mtd%d %s 失败：%w", entry.Index, entry.Name, err)
		}
		if size != entry.Size {
			_ = os.Remove(path)
			return result, fmt.Errorf("mtd%d 备份大小异常：期望 %d，实际 %d", entry.Index, entry.Size, size)
		}
		result.Files = append(result.Files, BackupFile{Device: fmt.Sprintf("mtd%d", entry.Index), Name: entry.Name, Path: path, Size: size, SHA256: hash})
	}
	manifest := backupManifest{CreatedAt: time.Now().Format(time.RFC3339), Model: info.Model, Host: cfg.Host, RawMTD: raw, Files: result.Files}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	result.ManifestPath = filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(result.ManifestPath, data, 0644); err != nil {
		return result, fmt.Errorf("写入备份清单失败：%w", err)
	}
	logf(fmt.Sprintf("全部 %d 个 MTD 分区备份完成：%s", len(result.Files), directory))
	return result, nil
}

type mtdEntry struct {
	Index     int
	Size      int64
	EraseSize int64
	Name      string
}

func parseMTDEntries(raw string) []mtdEntry {
	var result []mtdEntry
	for _, match := range mtdEntryLine.FindAllStringSubmatch(raw, -1) {
		index, indexErr := strconv.Atoi(match[1])
		size, sizeErr := strconv.ParseInt(match[2], 16, 64)
		eraseSize, eraseErr := strconv.ParseInt(match[3], 16, 64)
		if indexErr == nil && sizeErr == nil && eraseErr == nil && size > 0 {
			result = append(result, mtdEntry{Index: index, Size: size, EraseSize: eraseSize, Name: match[4]})
		}
	}
	return result
}

func streamMTD(client *ssh.Client, index int, localPath string) (string, int64, error) {
	file, err := os.Create(localPath)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	session, err := client.NewSession()
	if err != nil {
		file.Close()
		return "", 0, err
	}
	defer session.Close()
	var stderr bytes.Buffer
	session.Stdout = io.MultiWriter(file, hash)
	session.Stderr = &stderr
	err = session.Run(fmt.Sprintf("dd if=/dev/mtd%d bs=65536", index))
	closeErr := file.Close()
	if err != nil {
		return "", 0, fmt.Errorf("读取失败：%w；%s", err, strings.TrimSpace(stderr.String()))
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	stat, err := os.Stat(localPath)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), stat.Size(), nil
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	name := strings.Trim(builder.String(), "_")
	if name == "" {
		return "unknown"
	}
	return name
}

func parseRouterInfo(host, raw string) (RouterInfo, error) {
	info := RouterInfo{Host: host, MTD: map[string]string{}, RawMTD: raw}
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "MODEL=") {
			info.Model = strings.TrimSpace(strings.TrimPrefix(line, "MODEL="))
		}
	}
	for _, match := range mtdLine.FindAllStringSubmatch(raw, -1) {
		info.MTD["mtd"+match[1]] = match[3]
	}
	if len(info.MTD) == 0 {
		return RouterInfo{}, errors.New("未读取到 /proc/mtd，禁止继续")
	}
	model := strings.ToLower(info.Model)
	if info.MTD["mtd4"] == "Factory" && info.MTD["mtd5"] == "FIP" && (strings.Contains(model, "ax6000") || strings.Contains(model, "rb06") || strings.Contains(model, "mt7986")) {
		info.ModelKey = "AX6000"
		info.Compatible = true
		return info, nil
	}
	if info.MTD["mtd1"] == "0:MIBIB" {
		names := map[string]bool{info.MTD["mtd15"]: true, info.MTD["mtd16"]: true}
		if names["0:APPSBL"] && names["0:APPSBL_1"] && (strings.Contains(model, "ax9000") || strings.Contains(model, "ra70") || strings.Contains(model, "ipq807x/ap-hk14")) {
			info.ModelKey = "AX9000"
			info.Compatible = true
			return info, nil
		}
	}
	return info, fmt.Errorf("设备型号或引导分区不受支持：型号 %q，mtd4=%q，mtd5=%q，mtd15=%q，mtd16=%q", info.Model, info.MTD["mtd4"], info.MTD["mtd5"], info.MTD["mtd15"], info.MTD["mtd16"])
}

func readRouterInfo(client *ssh.Client, host string) (RouterInfo, string, error) {
	const command = `model="$(tr -d '\000' </proc/device-tree/model 2>/dev/null)"; [ -n "$model" ] || model="$(cat /tmp/sysinfo/model 2>/dev/null)"; printf 'MODEL=%s\n' "$model"; cat /proc/mtd`
	var lastInfo RouterInfo
	var lastRaw string
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := runSSH(client, command)
		if err == nil {
			info, parseErr := parseRouterInfo(host, raw)
			if parseErr == nil {
				return info, raw, nil
			}
			lastInfo, lastErr = info, parseErr
		} else {
			lastErr = fmt.Errorf("读取路由器信息失败：%w", err)
		}
		lastRaw = raw
		if attempt < 2 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return lastInfo, lastRaw, fmt.Errorf("连续 3 次读取设备信息失败：%w", lastErr)
}

func FlashUBoot(ctx context.Context, input FlashInput, logf func(string)) (FlashResult, error) {
	if strings.EqualFold(input.Model, "AX6000") {
		return flashAX6000UBoot(ctx, input, logf)
	}
	mibib, err := inspectFile(input.MIBIBPath)
	if err != nil {
		return FlashResult{}, err
	}
	uboot, err := inspectFile(input.UBootPath)
	if err != nil {
		return FlashResult{}, err
	}
	if mibib.Size > 1024*1024 || uboot.Size > 1024*1024 {
		return FlashResult{}, errors.New("引导文件大于目标 1 MiB 分区，禁止写入")
	}
	client, err := dialSSH(ctx, input.SSH)
	if err != nil {
		return FlashResult{}, err
	}
	defer client.Close()
	info, _, err := readRouterInfo(client, input.SSH.Host)
	if err != nil {
		return FlashResult{}, err
	}
	if info.ModelKey != "AX9000" {
		return FlashResult{}, fmt.Errorf("当前选择 AX9000，但 SSH 设备是 %s", info.ModelKey)
	}
	logf("设备型号与 mtd1/mtd15/mtd16 分区校验通过")
	hashTool, err := detectRemoteSHA256(client)
	if err != nil {
		return FlashResult{}, err
	}
	logf("远端 SHA256 工具：" + hashTool)
	remoteMIBIB := "/tmp/ax9000flash-mibib.bin"
	remoteUBoot := "/tmp/ax9000flash-uboot.bin"
	if err := uploadSSHFile(client, mibib.Path, remoteMIBIB); err != nil {
		return FlashResult{}, fmt.Errorf("上传 MIBIB 失败：%w", err)
	}
	logf("MIBIB 已上传")
	if err := uploadSSHFile(client, uboot.Path, remoteUBoot); err != nil {
		return FlashResult{}, fmt.Errorf("上传 U-Boot 失败：%w", err)
	}
	logf("U-Boot 已上传")
	remoteMIBIBHash, err := remoteSHA256(client, hashTool, remoteMIBIB, 0)
	if err != nil {
		return FlashResult{}, fmt.Errorf("MIBIB 远端哈希校验失败：%w", err)
	}
	remoteUBootHash, err := remoteSHA256(client, hashTool, remoteUBoot, 0)
	if err != nil {
		return FlashResult{}, fmt.Errorf("U-Boot 远端哈希校验失败：%w", err)
	}
	if !strings.Contains(remoteMIBIBHash, mibib.SHA256) || !strings.Contains(remoteUBootHash, uboot.SHA256) {
		return FlashResult{}, errors.New("上传前后 SHA256 不一致，禁止写入")
	}
	logf("上传文件 SHA256 一致")
	commands := []struct{ name, command string }{
		{"MIBIB", "mtd write " + remoteMIBIB + " /dev/mtd1"},
		{"APPSBL(mtd15)", "mtd write " + remoteUBoot + " /dev/mtd15"},
		{"APPSBL(mtd16)", "mtd write " + remoteUBoot + " /dev/mtd16"},
	}
	for _, item := range commands {
		logf("正在写入 " + item.name)
		if out, err := runSSH(client, item.command); err != nil {
			return FlashResult{}, fmt.Errorf("写入 %s 失败：%w；输出：%s", item.name, err, out)
		}
	}
	checks := []struct {
		name, path, expected string
		size                 int64
	}{
		{"mtd1", "/dev/mtd1", mibib.SHA256, mibib.Size},
		{"mtd15", "/dev/mtd15", uboot.SHA256, uboot.Size},
		{"mtd16", "/dev/mtd16", uboot.SHA256, uboot.Size},
	}
	for _, check := range checks {
		out, err := remoteSHA256(client, hashTool, check.path, check.size)
		if err != nil || !strings.Contains(out, check.expected) {
			return FlashResult{}, fmt.Errorf("%s 写后校验失败，禁止断电", check.name)
		}
		logf(check.name + " 写后 SHA256 校验通过")
	}
	return FlashResult{MIBIBSHA256: mibib.SHA256, UBootSHA256: uboot.SHA256, Message: "三处引导分区写入并校验成功"}, nil
}

func flashAX6000UBoot(ctx context.Context, input FlashInput, logf func(string)) (FlashResult, error) {
	fip, err := inspectFile(input.UBootPath)
	if err != nil {
		return FlashResult{}, err
	}
	if err := validateAX6000FIP(fip); err != nil {
		return FlashResult{}, err
	}
	client, err := dialSSH(ctx, input.SSH)
	if err != nil {
		return FlashResult{}, err
	}
	defer client.Close()
	info, _, err := readRouterInfo(client, input.SSH.Host)
	if err != nil {
		return FlashResult{}, err
	}
	if info.ModelKey != "AX6000" || info.MTD["mtd4"] != "Factory" || info.MTD["mtd5"] != "FIP" {
		return FlashResult{}, errors.New("当前设备不是 Factory=mtd4、FIP=mtd5 的红米 AX6000，禁止写入")
	}
	hashTool, err := detectRemoteSHA256(client)
	if err != nil {
		return FlashResult{}, err
	}
	remoteFIP := "/tmp/ax6000flash-fip.bin"
	if err := uploadSSHFile(client, fip.Path, remoteFIP); err != nil {
		return FlashResult{}, fmt.Errorf("上传 AX6000 FIP 失败：%w", err)
	}
	remoteHash, err := remoteSHA256(client, hashTool, remoteFIP, 0)
	if err != nil || !strings.Contains(remoteHash, fip.SHA256) {
		return FlashResult{}, errors.New("AX6000 FIP 上传前后 SHA256 不一致，禁止写入")
	}
	logf("AX6000 FIP 已上传并通过 SHA256 校验")
	if out, err := runSSH(client, "mtd erase FIP"); err != nil {
		return FlashResult{}, fmt.Errorf("擦除 FIP 失败：%w；输出：%s", err, out)
	}
	if out, err := runSSH(client, "mtd write "+remoteFIP+" FIP"); err != nil {
		return FlashResult{}, fmt.Errorf("写入 FIP 失败：%w；输出：%s", err, out)
	}
	writtenHash, err := remoteSHA256(client, hashTool, "/dev/mtd5", fip.Size)
	if err != nil || !strings.Contains(writtenHash, fip.SHA256) {
		return FlashResult{}, errors.New("AX6000 FIP 写后 SHA256 校验失败，禁止断电")
	}
	logf("AX6000 mtd5/FIP 写后 SHA256 校验通过")
	return FlashResult{UBootSHA256: fip.SHA256, Message: "AX6000 FIP 引导分区写入并校验成功"}, nil
}

func detectRemoteSHA256(client *ssh.Client) (string, error) {
	out, err := runSSHNonEmpty(client, `if command -v sha256sum >/dev/null 2>&1; then echo sha256sum; elif command -v openssl >/dev/null 2>&1; then echo openssl; else exit 127; fi`)
	if err != nil {
		return "", errors.New("路由器缺少 sha256sum 和 openssl，禁止写入")
	}
	tool := strings.TrimSpace(out)
	if tool != "sha256sum" && tool != "openssl" {
		return "", fmt.Errorf("无法识别远端 SHA256 工具：%q", tool)
	}
	return tool, nil
}

func remoteSHA256(client *ssh.Client, tool, path string, size int64) (string, error) {
	source := shellQuote(path)
	if size > 0 {
		source = fmt.Sprintf("head -c %d %s | ", size, source)
		if tool == "sha256sum" {
			source += "sha256sum"
		} else {
			source += "openssl dgst -sha256"
		}
	} else if tool == "sha256sum" {
		source = "sha256sum " + source
	} else {
		source = "openssl dgst -sha256 " + source
	}
	return runSSHNonEmpty(client, source)
}

func dialSSH(ctx context.Context, cfg SSHConfig) (*ssh.Client, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "192.168.31.1"
	}
	if !strings.Contains(host, ":") {
		host += ":22"
	}
	config := &ssh.ClientConfig{
		User:            strings.TrimSpace(cfg.Username),
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 路由器重刷后主机密钥会变化，仅用于直连局域网设备。
		Timeout:         8 * time.Second,
	}
	dialer := net.Dialer{Timeout: 8 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("连接 SSH 失败：%w", err)
	}
	sshConn, channels, requests, err := ssh.NewClientConn(conn, host, config)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH 登录失败：%w", err)
	}
	return ssh.NewClient(sshConn, channels, requests), nil
}

func runSSH(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var output bytes.Buffer
	session.Stdout = &output
	session.Stderr = &output
	err = session.Run(command)
	return output.String(), err
}

func uploadSSHFile(client *ssh.Client, localPath, remotePath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	var stderr bytes.Buffer
	session.Stdin = file
	session.Stderr = &stderr
	if err := session.Run("cat > " + shellQuote(remotePath)); err != nil {
		return fmt.Errorf("SSH 文件传输失败：%w；%s", err, strings.TrimSpace(stderr.String()))
	}
	out, err := runSSHNonEmpty(client, "wc -c < "+shellQuote(remotePath))
	if err != nil {
		return fmt.Errorf("读取远端文件大小失败：%w", err)
	}
	remoteSize, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil || remoteSize != stat.Size() {
		return fmt.Errorf("远端文件大小异常：期望 %d，实际 %q", stat.Size(), strings.TrimSpace(out))
	}
	return nil
}

func runSSHNonEmpty(client *ssh.Client, command string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		out, err := runSSH(client, command)
		if err == nil && strings.TrimSpace(out) != "" {
			return out, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = errors.New("SSH 命令返回空内容")
		}
		if attempt < 2 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return "", lastErr
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func ParseMTDSize(raw, device string) (int64, error) {
	for _, match := range mtdLine.FindAllStringSubmatch(raw, -1) {
		if "mtd"+match[1] == device {
			return strconv.ParseInt(match[2], 16, 64)
		}
	}
	return 0, fmt.Errorf("未找到 %s", device)
}
