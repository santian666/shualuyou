package flasher

import (
	"archive/tar"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type FileInfo struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Materials struct {
	DeveloperFirmware FileInfo   `json:"developerFirmware"`
	MIBIB             FileInfo   `json:"mibib"`
	UBoot             FileInfo   `json:"uboot"`
	Firmware          FileInfo   `json:"firmware"`
	FirmwareBoard     string     `json:"firmwareBoard"`
	FirmwareLayout    string     `json:"firmwareLayout"`
	BackupFiles       []FileInfo `json:"backupFiles"`
}

func InspectMaterials(model, developerFirmwarePath, mibibPath, ubootPath, firmwarePath, backupDir string) (Materials, error) {
	if strings.EqualFold(model, "AX6000") {
		return inspectAX6000Materials(developerFirmwarePath, ubootPath, firmwarePath, backupDir)
	}
	developerFirmware, err := inspectFile(developerFirmwarePath)
	if err != nil {
		return Materials{}, fmt.Errorf("开发版固件：%w", err)
	}
	mibib, err := inspectFile(mibibPath)
	if err != nil {
		return Materials{}, fmt.Errorf("MIBIB 文件：%w", err)
	}
	uboot, err := inspectFile(ubootPath)
	if err != nil {
		return Materials{}, fmt.Errorf("U-Boot 文件：%w", err)
	}
	firmware, err := inspectFile(firmwarePath)
	if err != nil {
		return Materials{}, fmt.Errorf("OpenWrt 固件：%w", err)
	}
	if mibib.Size <= 0 || mibib.Size > 1024*1024 {
		return Materials{}, fmt.Errorf("MIBIB 大小异常：%d 字节，不能超过 mtd1 的 1 MiB", mibib.Size)
	}
	if uboot.Size <= 0 || uboot.Size > 1024*1024 {
		return Materials{}, fmt.Errorf("U-Boot 大小异常：%d 字节，不能超过 APPSBL 分区的 1 MiB", uboot.Size)
	}
	if firmware.Size < 8*1024*1024 {
		return Materials{}, errors.New("OpenWrt 固件小于 8 MiB，疑似选错文件")
	}
	developerName := strings.ToLower(developerFirmware.Name)
	if developerFirmware.Size < 8*1024*1024 || filepath.Ext(developerName) != ".bin" || !strings.Contains(developerName, "ra70") {
		return Materials{}, errors.New("开发版固件必须是文件名含 RA70 的 .bin 文件")
	}
	firmwareName := strings.ToLower(firmware.Name)
	if filepath.Ext(firmwareName) != ".ubi" || !strings.Contains(firmwareName, "factory") {
		return Materials{}, errors.New("U-Boot 必须选择文件名含 factory 的 .ubi 固件，禁止使用 sysupgrade 固件")
	}
	expected := []struct {
		label string
		file  FileInfo
		hash  string
	}{
		{"AX9000 开发版固件", developerFirmware, "dface6804928a174e40d6727c802249563234bb9c800493f5c8b75d3c582b2d4"},
		{"AX9000 MIBIB", mibib, "2d79c1b15b8946af733dba5fff5e33bf3ba18863ed734710bd2a5e48fc469c11"},
		{"AX9000 U-Boot", uboot, "80908e502add45ee6aa7ad83aa16ec2c3c8bf27b572ac0f9c3fe404b94fc0007"},
		{"AX9000 factory.ubi", firmware, "e9059e1d7a57a59df58b6bcdda8fd176645076934ac8e882750f40c6ab6c56aa"},
	}
	for _, item := range expected {
		if !strings.EqualFold(item.file.SHA256, item.hash) {
			return Materials{}, fmt.Errorf("%s 的 SHA256 与同目录工具包清单不一致，禁止使用", item.label)
		}
	}
	backups := []FileInfo{}
	if strings.TrimSpace(backupDir) != "" {
		backups, err = inspectBackups("AX9000", backupDir)
		if err != nil {
			return Materials{}, err
		}
	}
	return Materials{DeveloperFirmware: developerFirmware, MIBIB: mibib, UBoot: uboot, Firmware: firmware, BackupFiles: backups}, nil
}

func inspectAX6000Materials(stockFirmwarePath, fipPath, firmwarePath, backupDir string) (Materials, error) {
	stockFirmware := FileInfo{}
	if strings.TrimSpace(stockFirmwarePath) != "" {
		var err error
		stockFirmware, err = inspectFile(stockFirmwarePath)
		if err != nil {
			return Materials{}, fmt.Errorf("官方 1.2.8 固件：%w", err)
		}
		name := strings.ToLower(stockFirmware.Name)
		if stockFirmware.Size < 8*1024*1024 || filepath.Ext(name) != ".bin" || !strings.Contains(name, "ax6000") || !strings.Contains(name, "1.2.8") {
			return Materials{}, errors.New("AX6000 解锁固件必须是文件名含 AX6000 和 1.2.8 的 .bin 文件")
		}
	}
	fip, err := inspectFile(fipPath)
	if err != nil {
		return Materials{}, fmt.Errorf("AX6000 FIP：%w", err)
	}
	if err := validateAX6000FIP(fip); err != nil {
		return Materials{}, err
	}
	firmware, err := inspectFile(firmwarePath)
	if err != nil {
		return Materials{}, fmt.Errorf("AX6000 OpenWrt 固件：%w", err)
	}
	firmwareName := strings.ToLower(firmware.Name)
	ext := filepath.Ext(firmwareName)
	if firmware.Size < 8*1024*1024 || !strings.Contains(firmwareName, "ax6000") || ext != ".bin" && ext != ".itb" {
		return Materials{}, errors.New("AX6000 固件必须是文件名含 AX6000 的 .bin 或 .itb 文件")
	}
	board, layout, err := inspectAX6000FirmwareLayout(firmware.Path, ext)
	if err != nil {
		return Materials{}, err
	}
	backups := []FileInfo{}
	if strings.TrimSpace(backupDir) != "" {
		backups, err = inspectBackups("AX6000", backupDir)
		if err != nil {
			return Materials{}, err
		}
	}
	return Materials{DeveloperFirmware: stockFirmware, UBoot: fip, Firmware: firmware, FirmwareBoard: board, FirmwareLayout: layout, BackupFiles: backups}, nil
}

func inspectAX6000FirmwareLayout(path, ext string) (string, string, error) {
	if ext == ".itb" {
		return "FIT/ITB", "immortalwrt-110m", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	reader := tar.NewReader(file)
	board := ""
	hasKernel, hasRoot := false, false
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", "", errors.New("AX6000 .bin 不是 U-Boot 可识别的 sysupgrade TAR 固件")
		}
		name := strings.ToLower(header.Name)
		if !strings.HasPrefix(name, "sysupgrade-") {
			continue
		}
		switch filepath.Base(name) {
		case "control":
			data, readErr := io.ReadAll(io.LimitReader(reader, 1024))
			if readErr != nil {
				return "", "", fmt.Errorf("读取 AX6000 固件 CONTROL 失败：%w", readErr)
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "BOARD=") {
					board = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "BOARD="))
				}
			}
		case "kernel":
			hasKernel = header.Size > 0
		case "root":
			hasRoot = header.Size > 0
		}
	}
	if !hasKernel || !hasRoot || board == "" {
		return "", "", errors.New("AX6000 sysupgrade 固件缺少 CONTROL、kernel 或 root")
	}
	switch board {
	case "xiaomi_redmi-router-ax6000-stock":
		return board, "default", nil
	case "xiaomi_redmi-router-ax6000":
		return board, "immortalwrt-110m", nil
	default:
		return "", "", fmt.Errorf("AX6000 固件 BOARD 不兼容：%s", board)
	}
}

func validateAX6000FIP(fip FileInfo) error {
	if fip.Size != 793129 {
		return fmt.Errorf("AX6000 FIP 大小异常：期望 793129 字节，实际 %d", fip.Size)
	}
	file, err := os.Open(fip.Path)
	if err != nil {
		return err
	}
	hash := md5.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != "7610a1722073748c3c3a860b75d94d5d" {
		return errors.New("AX6000 FIP 的 MD5 与教程固定文件不一致，禁止写入")
	}
	return nil
}

func inspectFile(path string) (FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileInfo{}, errors.New("未选择文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return FileInfo{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return FileInfo{}, err
	}
	if !stat.Mode().IsRegular() {
		return FileInfo{}, errors.New("不是普通文件")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Path: path, Name: stat.Name(), Size: stat.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func inspectBackups(model, dir string) ([]FileInfo, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("未选择原厂备份目录")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取备份目录失败：%w", err)
	}
	keywords := map[string]bool{"mibib": false, "appsbl": false, "appsbl1": false}
	expectedSize := int64(1024 * 1024)
	if strings.EqualFold(model, "AX6000") {
		keywords = map[string]bool{"factory": false, "fip": false}
		expectedSize = 2 * 1024 * 1024
	}
	var result []FileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		matched := false
		if strings.EqualFold(model, "AX6000") {
			if strings.Contains(lower, "factory") {
				keywords["factory"], matched = true, true
			}
			if strings.Contains(lower, "fip") {
				keywords["fip"], matched = true, true
			}
		} else if strings.Contains(lower, "mibib") {
			keywords["mibib"], matched = true, true
			if strings.Contains(lower, "appsbl1") || strings.Contains(lower, "appsbl_1") {
				keywords["appsbl1"], matched = true, true
			} else if strings.Contains(lower, "appsbl") {
				keywords["appsbl"], matched = true, true
			}
		}
		if matched {
			info, err := inspectFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, fmt.Errorf("检查备份 %s 失败：%w", entry.Name(), err)
			}
			if info.Size != expectedSize {
				return nil, fmt.Errorf("关键备份 %s 大小不是 %.0f MiB，疑似备份不完整", entry.Name(), float64(expectedSize)/1024/1024)
			}
			result = append(result, info)
		}
	}
	for key, ok := range keywords {
		if !ok {
			return nil, fmt.Errorf("备份目录缺少 %s 分区文件", key)
		}
	}
	return result, nil
}
