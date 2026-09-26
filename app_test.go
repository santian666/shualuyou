package main

import (
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestResolveDefaultPathsUsesModelDirectory(t *testing.T) {
	base := filepath.Join("C:", "release")
	paths := resolveDefaultPaths(base, "AX9000")
	wantTools := filepath.Join(base, "AX9000")
	if paths.ToolsDir != wantTools {
		t.Fatalf("工具目录 = %q，期望 %q", paths.ToolsDir, wantTools)
	}
	if paths.MIBIB != filepath.Join(wantTools, "bootloader", "mibib.bin") {
		t.Fatalf("MIBIB 路径异常：%q", paths.MIBIB)
	}
	if paths.BackupBaseDir != filepath.Join(wantTools, "backups") {
		t.Fatalf("备份目录异常：%q", paths.BackupBaseDir)
	}
}

func TestDecodeWindowsOutputGBK(t *testing.T) {
	want := "已在此接口上启用 DHCP。"
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeWindowsOutput(encoded); got != want {
		t.Fatalf("解码结果 = %q，期望 %q", got, want)
	}
}

func TestResolveDefaultPathsAX6000(t *testing.T) {
	base := filepath.Join("C:", "release")
	paths := resolveDefaultPaths(base, "AX6000")
	wantTools := filepath.Join(base, "AX6000")
	if paths.Model != "AX6000" || paths.ToolsDir != wantTools || paths.MIBIB != "" || paths.DeveloperFirmware != "" {
		t.Fatalf("AX6000 默认路径异常：%+v", paths)
	}
	if paths.UBoot != filepath.Join(wantTools, "bootloader", "mt7986_redmi_ax6000-fip-fixed-parts.bin") {
		t.Fatalf("AX6000 FIP 路径异常：%q", paths.UBoot)
	}
}
