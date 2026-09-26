package flasher

import (
	"archive/tar"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectAX6000FirmwareLayout(t *testing.T) {
	tests := []struct {
		board  string
		layout string
	}{
		{"xiaomi_redmi-router-ax6000", "immortalwrt-110m"},
		{"xiaomi_redmi-router-ax6000-stock", "default"},
	}
	for _, test := range tests {
		path := filepath.Join(t.TempDir(), "ax6000-sysupgrade.bin")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		writer := tar.NewWriter(file)
		base := "sysupgrade-" + test.board + "/"
		for name, data := range map[string][]byte{"CONTROL": []byte("BOARD=" + test.board + "\n"), "kernel": {1}, "root": {2}} {
			if err := writer.WriteHeader(&tar.Header{Name: base + name, Mode: 0644, Size: int64(len(data))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		board, layout, err := inspectAX6000FirmwareLayout(path, ".bin")
		if err != nil || board != test.board || layout != test.layout {
			t.Fatalf("board=%s layout=%s err=%v", board, layout, err)
		}
	}
}

func TestParseRouterInfo(t *testing.T) {
	raw := "MODEL=Xiaomi AX9000\n" +
		"mtd1: 00100000 00020000 \"0:MIBIB\"\n" +
		"mtd15: 00100000 00020000 \"0:APPSBL_1\"\n" +
		"mtd16: 00100000 00020000 \"0:APPSBL\"\n"
	info, err := parseRouterInfo("192.168.31.1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Compatible || info.MTD["mtd15"] != "0:APPSBL_1" {
		t.Fatalf("解析结果异常：%+v", info)
	}
}

func TestParseRouterInfoStockPlatformName(t *testing.T) {
	raw := "MODEL=Qualcomm Technologies, Inc. IPQ807x/AP-HK14\n" +
		"mtd1: 00100000 00020000 \"0:MIBIB\"\n" +
		"mtd15: 00100000 00020000 \"0:APPSBL_1\"\n" +
		"mtd16: 00100000 00020000 \"0:APPSBL\"\n"
	info, err := parseRouterInfo("192.168.31.1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Compatible {
		t.Fatal("原厂设备树平台名未识别为 AX9000")
	}
}

func TestParseRouterInfoAX6000(t *testing.T) {
	raw := "MODEL=Redmi Router AX6000\n" +
		"mtd4: 00200000 00020000 \"Factory\"\n" +
		"mtd5: 00200000 00020000 \"FIP\"\n"
	info, err := parseRouterInfo("192.168.31.1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Compatible || info.ModelKey != "AX6000" {
		t.Fatalf("AX6000 解析结果异常：%+v", info)
	}
}

func TestParseUploadForm(t *testing.T) {
	html := `<form action="/upload" method="post"><input type="hidden" name="token" value="abc"><input type="file" name="firmware"></form>`
	action, field, hidden, err := parseUploadForm("http://192.168.1.1/", html)
	if err != nil {
		t.Fatal(err)
	}
	if action != "http://192.168.1.1/upload" || field != "firmware" || hidden["token"] != "abc" {
		t.Fatalf("表单解析异常：%s %s %#v", action, field, hidden)
	}
}

func TestParseUpdateForm(t *testing.T) {
	html := `<form action="/update" method="post"><input type="hidden" name="token" value="abc"><input type="submit" name="submit" value="Update"></form>`
	action, values, err := parseUpdateForm("http://192.168.31.1/upload", html)
	if err != nil {
		t.Fatal(err)
	}
	if action != "http://192.168.31.1/update" || values.Get("token") != "abc" || values.Get("submit") != "Update" {
		t.Fatalf("Update 表单解析异常：%s %#v", action, values)
	}
}

func TestParseUpdateButtonLink(t *testing.T) {
	html := `<p>The firmware has been uploaded, please click "Update"</p><button class="button" onclick="location = '/flashing.html'">Update</button>`
	method, action, values, err := parseUpdateRequest("http://192.168.31.1/upload", html)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || action != "http://192.168.31.1/flashing.html" || len(values) != 0 {
		t.Fatalf("Update 按钮解析异常：method=%s action=%s values=%v", method, action, values)
	}
}

func TestUploadFirmwareConfirmsUpdate(t *testing.T) {
	firmwarePath := filepath.Join(t.TempDir(), "ax6000-test.bin")
	if err := os.WriteFile(firmwarePath, []byte("firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	var uploadCount, updateCount int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			_, _ = io.WriteString(writer, `<html>Firmware Update<form action="/upload" method="post"><input type="file" name="firmware"></form></html>`)
		case "/upload":
			uploadCount++
			_, _ = io.WriteString(writer, `<p>Click Update</p><form action="/update" method="post"><input type="hidden" name="token" value="ok"><input type="submit" name="submit" value="Update"></form>`)
		case "/update":
			updateCount++
			if request.FormValue("token") != "ok" || request.FormValue("submit") != "Update" {
				t.Errorf("Update 表单内容异常：%v", request.Form)
			}
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	if err := UploadFirmware(context.Background(), server.URL, firmwarePath, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if uploadCount != 1 || updateCount != 1 {
		t.Fatalf("请求次数异常：upload=%d update=%d", uploadCount, updateCount)
	}
}

func TestUploadFirmwareConfirmsButtonLink(t *testing.T) {
	firmwarePath := filepath.Join(t.TempDir(), "ax6000-test.bin")
	if err := os.WriteFile(firmwarePath, []byte("firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	var flashingCount int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			_, _ = io.WriteString(writer, `<html>Firmware Update<form action="/upload" method="post"><input type="file" name="firmware"></form></html>`)
		case "/upload":
			_, _ = io.WriteString(writer, `<p>The firmware has been uploaded, please click "Update"</p><button onclick="location = '/flashing.html'">Update</button>`)
		case "/flashing.html":
			if request.Method != http.MethodGet {
				t.Errorf("flashing 请求方法=%s", request.Method)
			}
			flashingCount++
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	if err := UploadFirmware(context.Background(), server.URL, firmwarePath, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if flashingCount != 1 {
		t.Fatalf("flashing 请求次数=%d", flashingCount)
	}
}

func TestUploadFirmwareWaitsForResult(t *testing.T) {
	firmwarePath := filepath.Join(t.TempDir(), "ax6000-test.bin")
	if err := os.WriteFile(firmwarePath, []byte("firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	var resultCount int
	var versionCount int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			_, _ = io.WriteString(writer, `<html>Firmware Update<form action="/upload" method="post"><input type="file" name="firmware"></form></html>`)
		case "/upload":
			_, _ = io.WriteString(writer, `<p>Click Update</p><button onclick="location = '/flashing.html'">Update</button>`)
		case "/flashing.html":
			_, _ = io.WriteString(writer, `<script>ajax({url: '/result'})</script>`)
		case "/version":
			versionCount++
			_, _ = io.WriteString(writer, "U-Boot")
		case "/result":
			if request.Referer() == "" || request.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Error("/result 缺少浏览器请求头")
			}
			resultCount++
			_, _ = io.WriteString(writer, "success")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	if err := UploadFirmware(context.Background(), server.URL, firmwarePath, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if versionCount != 1 || resultCount != 1 {
		t.Fatalf("请求次数：/version=%d /result=%d", versionCount, resultCount)
	}
}

func TestUploadFirmwareRetriesUpdateFailedOnce(t *testing.T) {
	firmwarePath := filepath.Join(t.TempDir(), "ax6000-test.bin")
	if err := os.WriteFile(firmwarePath, []byte("firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	var uploads int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			_, _ = io.WriteString(writer, `<html>Firmware Update<form action="/upload" method="post"><input type="file" name="firmware"></form></html>`)
			return
		}
		uploads++
		if uploads == 1 {
			_, _ = io.WriteString(writer, "Update Failed")
		}
	}))
	defer server.Close()
	if err := UploadFirmware(context.Background(), server.URL, firmwarePath, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if uploads != 2 {
		t.Fatalf("期望自动上传两次，实际 %d", uploads)
	}
}

func TestUploadFirmwareUsesContentLength(t *testing.T) {
	firmwareData := []byte("test-firmware-content")
	firmwarePath := filepath.Join(t.TempDir(), "factory.ubi")
	if err := os.WriteFile(firmwarePath, firmwareData, 0600); err != nil {
		t.Fatal(err)
	}
	var uploaded bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			_, _ = io.WriteString(writer, `<html>Firmware Update<form action="/upload" method="post"><input type="hidden" name="token" value="abc"><input type="file" name="firmware"></form></html>`)
			return
		}
		if request.ContentLength <= 0 || len(request.TransferEncoding) != 0 {
			t.Errorf("上传请求未使用明确 Content-Length：length=%d transfer=%v", request.ContentLength, request.TransferEncoding)
		}
		if err := request.ParseMultipartForm(1024 * 1024); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		file, _, err := request.FormFile("firmware")
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil || string(data) != string(firmwareData) || request.FormValue("token") != "abc" {
			t.Errorf("上传表单内容异常：data=%q token=%q err=%v", data, request.FormValue("token"), err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		uploaded = true
	}))
	defer server.Close()
	if err := UploadFirmware(context.Background(), server.URL, firmwarePath, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !uploaded {
		t.Fatal("服务端未收到固件上传")
	}
}

func TestParseMTDEntries(t *testing.T) {
	raw := "mtd1: 00100000 00020000 \"0:MIBIB\"\n" +
		"mtd15: 00100000 00020000 \"0:APPSBL_1\"\n"
	entries := parseMTDEntries(raw)
	if len(entries) != 2 || entries[0].Index != 1 || entries[0].Size != 1024*1024 || entries[0].EraseSize != 128*1024 {
		t.Fatalf("分区解析异常：%+v", entries)
	}
	if safeName("Xiaomi AX9000 / RA70") != "Xiaomi_AX9000___RA70" {
		t.Fatalf("文件名清理异常：%s", safeName("Xiaomi AX9000 / RA70"))
	}
}

func TestParseUBIVirtualMTDEntry(t *testing.T) {
	entries := parseMTDEntries("mtd27: 007df000 0001f000 \"rootfs_data\"\n")
	if len(entries) != 1 || entries[0].EraseSize != 0x1f000 || entries[0].Name != "rootfs_data" {
		t.Fatalf("UBI 虚拟卷解析异常：%+v", entries)
	}
}
