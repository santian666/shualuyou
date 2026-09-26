package flasher

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type XiaomiInfo struct {
	Model   string `json:"model"`
	Version string `json:"version"`
}

type xiaomiPage struct {
	XiaomiInfo
	MAC         string
	NonceKey    string
	EncryptMode int
}

var (
	xiaomiHardwarePattern = regexp.MustCompile(`(?:hardware|hardwareVersion)\s*[:=]\s*['"]([^'"]+)`)
	xiaomiVersionPattern  = regexp.MustCompile(`romVersion\s*:\s*['"]([^'"]+)`)
	xiaomiMACPattern      = regexp.MustCompile(`deviceId\s*=\s*['"]([^'"]+)`)
	xiaomiKeyPattern      = regexp.MustCompile(`key\s*:\s*['"]([^'"]+)`)
	errUploadDisconnected = errors.New("上传后连接断开")
)

func InspectXiaomi(ctx context.Context, host string) (XiaomiInfo, error) {
	page, err := readXiaomiPage(ctx, host)
	return page.XiaomiInfo, err
}

func InstallDeveloperFirmware(ctx context.Context, host, webPassword, firmwarePath string, logf func(string)) (XiaomiInfo, error) {
	page, err := readXiaomiPage(ctx, host)
	if err != nil {
		return XiaomiInfo{}, err
	}
	if !strings.EqualFold(page.Model, "RA70") {
		return page.XiaomiInfo, fmt.Errorf("当前设备不是 AX9000/RA70，而是 %q", page.Model)
	}
	if page.Version == "1.0.108" {
		return page.XiaomiInfo, nil
	}
	firmware, err := inspectFile(firmwarePath)
	if err != nil {
		return page.XiaomiInfo, err
	}
	name := strings.ToLower(firmware.Name)
	if filepath.Ext(name) != ".bin" || !strings.Contains(name, "ra70") || firmware.Size < 8*1024*1024 {
		return page.XiaomiInfo, errors.New("开发版固件不是有效的 RA70 .bin 文件")
	}
	token, err := loginXiaomi(ctx, host, webPassword, page)
	if err != nil {
		return page.XiaomiInfo, err
	}
	logf(fmt.Sprintf("自动上传 AX9000 开发版固件（%.1f MiB）", float64(firmware.Size)/1024/1024))
	if err := uploadXiaomiROM(ctx, host, token, firmware.Path); err != nil {
		if !errors.Is(err, errUploadDisconnected) {
			return page.XiaomiInfo, err
		}
		logf("固件上传后连接已中断，继续按路由器版本变化确认结果")
	}
	info, err := waitXiaomiReboot(ctx, host, page.Version, "1.0.108", 6*time.Minute, logf)
	if err != nil {
		return page.XiaomiInfo, err
	}
	return info, nil
}

func InstallAX6000Firmware(ctx context.Context, host, webPassword, firmwarePath string, logf func(string)) (XiaomiInfo, error) {
	page, err := readXiaomiPage(ctx, host)
	if err != nil {
		return XiaomiInfo{}, err
	}
	if !strings.EqualFold(page.Model, "RB06") {
		return page.XiaomiInfo, fmt.Errorf("当前设备不是红米 AX6000/RB06，而是 %q", page.Model)
	}
	if page.Version == "1.2.8" {
		return page.XiaomiInfo, nil
	}
	firmware, err := inspectFile(firmwarePath)
	if err != nil {
		return page.XiaomiInfo, err
	}
	name := strings.ToLower(firmware.Name)
	if filepath.Ext(name) != ".bin" || !strings.Contains(name, "ax6000") || !strings.Contains(name, "1.2.8") || firmware.Size < 8*1024*1024 {
		return page.XiaomiInfo, errors.New("AX6000 解锁固件必须是文件名含 AX6000 和 1.2.8 的 .bin 文件")
	}
	token, err := loginXiaomi(ctx, host, webPassword, page)
	if err != nil {
		return page.XiaomiInfo, err
	}
	logf(fmt.Sprintf("自动上传红米 AX6000 官方 1.2.8 固件（%.1f MiB）", float64(firmware.Size)/1024/1024))
	if err := uploadXiaomiROM(ctx, host, token, firmware.Path); err != nil && !errors.Is(err, errUploadDisconnected) {
		return page.XiaomiInfo, err
	}
	return waitXiaomiReboot(ctx, host, page.Version, "1.2.8", 6*time.Minute, logf)
}

func readXiaomiPage(ctx context.Context, host string) (xiaomiPage, error) {
	address := normalizeHTTPHost(host) + "/cgi-bin/luci/web"
	client := &http.Client{Timeout: 8 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return xiaomiPage{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return xiaomiPage{}, fmt.Errorf("读取小米后台失败：%w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return xiaomiPage{}, err
	}
	text := string(body)
	page := xiaomiPage{}
	page.Model = firstMatch(xiaomiHardwarePattern, text)
	page.Version = firstMatch(xiaomiVersionPattern, text)
	page.MAC = firstMatch(xiaomiMACPattern, text)
	page.NonceKey = firstMatch(xiaomiKeyPattern, text)
	page.EncryptMode = parseNewEncryptMode(text)
	if page.Model == "" || page.Version == "" {
		return page, errors.New("无法识别小米路由器型号或固件版本")
	}
	return page, nil
}

func loginXiaomi(ctx context.Context, host, webPassword string, page xiaomiPage) (string, error) {
	if strings.TrimSpace(webPassword) == "" {
		return "", errors.New("小米后台密码不能为空")
	}
	if page.MAC == "" || page.NonceKey == "" {
		return "", errors.New("小米后台登录参数不完整")
	}
	nonce := fmt.Sprintf("0_%s_%d_%d", page.MAC, time.Now().Unix(), rand.Intn(9001)+1000)
	accountHash := xiaomiHash(webPassword+page.NonceKey, page.EncryptMode)
	passwordHash := xiaomiHash(nonce+accountHash, page.EncryptMode)
	form := url.Values{"username": {"admin"}, "password": {passwordHash}, "logtype": {"2"}, "nonce": {nonce}}
	address := normalizeHTTPHost(host) + "/cgi-bin/luci/api/xqsystem/login"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	response, err := (&http.Client{Timeout: 12 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("登录小米后台失败：%w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return "", err
	}
	var result struct {
		Code  int    `json:"code"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Code != 0 || result.Token == "" {
		return "", errors.New("小米后台密码错误或登录接口拒绝访问")
	}
	return result.Token, nil
}

func uploadXiaomiROM(ctx context.Context, host, token, firmwarePath string) error {
	file, err := os.Open(firmwarePath)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	var header bytes.Buffer
	multipartWriter := multipart.NewWriter(&header)
	if _, err := multipartWriter.CreateFormFile("image", filepath.Base(firmwarePath)); err != nil {
		return err
	}
	var trailer bytes.Buffer
	trailerWriter := multipart.NewWriter(&trailer)
	if err := trailerWriter.SetBoundary(multipartWriter.Boundary()); err != nil {
		return err
	}
	if err := trailerWriter.Close(); err != nil {
		return err
	}
	reader := io.MultiReader(bytes.NewReader(header.Bytes()), file, bytes.NewReader(trailer.Bytes()))
	address := fmt.Sprintf("%s/cgi-bin/luci/;stok=%s/api/xqsystem/upload_rom", normalizeHTTPHost(host), token)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.ContentLength = int64(header.Len()) + stat.Size() + int64(trailer.Len())
	response, requestErr := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if requestErr != nil {
		return fmt.Errorf("%w：%v", errUploadDisconnected, requestErr)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("小米后台返回 HTTP %d", response.StatusCode)
	}
	return nil
}

func waitXiaomiReboot(ctx context.Context, host, oldVersion, expectedVersion string, timeout time.Duration, logf func(string)) (XiaomiInfo, error) {
	deadline := time.Now().Add(timeout)
	seenOffline := false
	for time.Now().Before(deadline) {
		info, err := InspectXiaomi(ctx, host)
		if err != nil {
			seenOffline = true
		} else if info.Version == expectedVersion && (seenOffline || info.Version != oldVersion) {
			logf("指定固件启动完成，当前版本：" + info.Version)
			return info, nil
		}
		select {
		case <-ctx.Done():
			return XiaomiInfo{}, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return XiaomiInfo{}, errors.New("等待开发版固件重启超时，禁止继续解锁")
}

func normalizeHTTPHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")
	host = strings.TrimSuffix(host, "/")
	return "http://" + host
}

func firstMatch(pattern *regexp.Regexp, text string) string {
	match := pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func xiaomiHash(value string, mode int) string {
	if mode == 1 {
		hash := sha256.Sum256([]byte(value))
		return hex.EncodeToString(hash[:])
	}
	hash := sha1.Sum([]byte(value))
	return hex.EncodeToString(hash[:])
}

func parseNewEncryptMode(raw string) int {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "newEncryptMode") {
			continue
		}
		parts := strings.FieldsFunc(line, func(r rune) bool { return r < '0' || r > '9' })
		for _, part := range parts {
			if mode, err := strconv.Atoi(part); err == nil {
				return mode
			}
		}
	}
	return 0
}
