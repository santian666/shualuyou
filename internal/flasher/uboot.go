package flasher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ProbeResult struct {
	URL     string `json:"url"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

var errUBootUpdateFailed = errors.New("U-Boot 报告 Update Failed")

var (
	formPattern       = regexp.MustCompile(`(?is)<form[^>]*>`)
	actionPattern     = regexp.MustCompile(`(?is)action=["']?([^"' >]+)`)
	filePattern       = regexp.MustCompile(`(?is)<input[^>]*type=["']?file["']?[^>]*name=["']([^"']+)["']`)
	filePattern2      = regexp.MustCompile(`(?is)<input[^>]*name=["']([^"']+)["'][^>]*type=["']?file["']?`)
	hiddenPattern     = regexp.MustCompile(`(?is)<input[^>]*type=["']?hidden["']?[^>]*name=["']([^"']+)["'][^>]*value=["']([^"']*)["']`)
	uploadFormPattern = regexp.MustCompile(`(?is)<form[^>]*>.*?<input[^>]*type=["']?file["']?[^>]*>.*?</form>`)
	formsPattern      = regexp.MustCompile(`(?is)<form[^>]*>.*?</form>`)
	inputPattern      = regexp.MustCompile(`(?is)<input[^>]*type=["']?submit["']?[^>]*>`)
	namePattern       = regexp.MustCompile(`(?is)name=["']([^"']+)["']`)
	valuePattern      = regexp.MustCompile(`(?is)value=["']([^"']*)["']`)
	onclickLocation   = regexp.MustCompile(`(?is)onclick\s*=\s*["'][^>]*?location\s*=\s*['"]([^'"]+)['"]`)
)

func ProbeWeb(ctx context.Context, address string) (ProbeResult, error) {
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		address = "http://" + address
	}
	client := &http.Client{Timeout: 4 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return ProbeResult{}, err
	}
	text := strings.ToLower(string(body))
	result := ProbeResult{URL: resp.Request.URL.String(), Kind: "unknown", Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	switch {
	case strings.Contains(text, "firmware update") && (filePattern.MatchString(string(body)) || filePattern2.MatchString(string(body))):
		result.Kind, result.Message = "uboot", "已检测到 U-Boot 固件上传页"
	case strings.Contains(text, "luci") || strings.Contains(text, "openwrt") || strings.Contains(text, "kwrt") || strings.Contains(text, "istoreos") || strings.Contains(text, "immortalwrt"):
		result.Kind, result.Message = "openwrt", "已检测到 OpenWrt/iStoreOS 后台"
	case strings.Contains(text, "miwifi"):
		result.Kind, result.Message = "miwifi", "已检测到小米路由器后台"
	}
	return result, nil
}

func UploadFirmware(ctx context.Context, pageURL, firmwarePath string, logf func(string)) error {
	for attempt := 1; attempt <= 2; attempt++ {
		err := uploadFirmwareOnce(ctx, pageURL, firmwarePath, logf)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errUBootUpdateFailed) || attempt == 2 {
			return err
		}
		logf("U-Boot 首次返回 Update Failed，按教程自动重新上传一次")
	}
	return nil
}

func uploadFirmwareOnce(ctx context.Context, pageURL, firmwarePath string, logf func(string)) error {
	firmware, err := inspectFile(firmwarePath)
	if err != nil {
		return err
	}
	probe, err := ProbeWeb(ctx, pageURL)
	if err != nil {
		return fmt.Errorf("打开 U-Boot 页面失败：%w", err)
	}
	if probe.Kind != "uboot" {
		return fmt.Errorf("当前页面不是已识别的 U-Boot 上传页：%s", probe.Message)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, probe.URL, nil)
	getResp, err := client.Do(getReq)
	if err != nil {
		return err
	}
	html, err := io.ReadAll(io.LimitReader(getResp.Body, 1024*1024))
	getResp.Body.Close()
	if err != nil {
		return err
	}
	action, field, hidden, err := parseUploadForm(probe.URL, string(html))
	if err != nil {
		return err
	}
	file, err := os.Open(firmware.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	var header bytes.Buffer
	multi := multipart.NewWriter(&header)
	for key, value := range hidden {
		if err := multi.WriteField(key, value); err != nil {
			return err
		}
	}
	if _, err := multi.CreateFormFile(field, filepath.Base(firmware.Path)); err != nil {
		return err
	}
	contentType := multi.FormDataContentType()
	headerData := append([]byte(nil), header.Bytes()...)
	header.Reset()
	if err := multi.Close(); err != nil {
		return err
	}
	trailerData := append([]byte(nil), header.Bytes()...)
	body := io.MultiReader(bytes.NewReader(headerData), file, bytes.NewReader(trailerData))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, action, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	request.ContentLength = int64(len(headerData)) + firmware.Size + int64(len(trailerData))
	logf(fmt.Sprintf("正在上传 %s（%.1f MiB）", firmware.Name, float64(firmware.Size)/1024/1024))
	response, requestErr := client.Do(request)
	if requestErr != nil {
		if errors.Is(requestErr, context.Canceled) {
			return requestErr
		}
		logf("上传连接被路由器断开，正在确认 U-Boot 是否已经开始刷写")
		if waitForUBootLeave(ctx, pageURL, 45*time.Second) {
			return nil
		}
		return fmt.Errorf("上传连接异常且 U-Boot 页面仍可访问：%w", requestErr)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("U-Boot 返回 HTTP %d", response.StatusCode)
	}
	responseHTML, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return err
	}
	lowerHTML := strings.ToLower(string(responseHTML))
	if strings.Contains(lowerHTML, "update failed") {
		return fmt.Errorf("%w：固件校验失败", errUBootUpdateFailed)
	}
	if strings.Contains(lowerHTML, "click") && strings.Contains(lowerHTML, "update") {
		updateMethod, updateAction, updateValues, parseErr := parseUpdateRequest(action, string(responseHTML))
		if parseErr != nil {
			return parseErr
		}
		logf("固件上传和兼容性检查通过，自动确认 U-Boot Update")
		var updateBody io.Reader
		if updateMethod == http.MethodPost {
			updateBody = strings.NewReader(updateValues.Encode())
		}
		updateRequest, requestErr := http.NewRequestWithContext(ctx, updateMethod, updateAction, updateBody)
		if requestErr != nil {
			return requestErr
		}
		if updateMethod == http.MethodPost {
			updateRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		updateResponse, updateErr := client.Do(updateRequest)
		if updateErr != nil {
			logf("确认 Update 后连接已断开，继续等待设备刷写和重启")
			return nil
		}
		defer updateResponse.Body.Close()
		if updateResponse.StatusCode < 200 || updateResponse.StatusCode >= 400 {
			return fmt.Errorf("U-Boot 确认 Update 返回 HTTP %d", updateResponse.StatusCode)
		}
		updateHTML, readErr := io.ReadAll(io.LimitReader(updateResponse.Body, 1024*1024))
		if readErr != nil {
			return readErr
		}
		if strings.Contains(strings.ToLower(string(updateHTML)), "update failed") {
			return fmt.Errorf("%w：固件与当前引导布局不兼容", errUBootUpdateFailed)
		}
		if strings.Contains(string(updateHTML), "'/result'") || strings.Contains(string(updateHTML), `"/result"`) {
			versionURL, resolveErr := resolveURL(updateAction, "/version")
			if resolveErr != nil {
				return resolveErr
			}
			versionRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
			if requestErr != nil {
				return requestErr
			}
			versionRequest.Header.Set("Referer", updateAction)
			versionResponse, versionErr := client.Do(versionRequest)
			if versionErr != nil {
				return fmt.Errorf("读取 U-Boot 版本状态失败：%w", versionErr)
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(versionResponse.Body, 4096))
			versionResponse.Body.Close()
			if versionResponse.StatusCode < 200 || versionResponse.StatusCode >= 400 {
				return fmt.Errorf("U-Boot /version 返回 HTTP %d", versionResponse.StatusCode)
			}
			resultURL, resolveErr := resolveURL(updateAction, "/result")
			if resolveErr != nil {
				return resolveErr
			}
			logf("第二个 Update 已确认，正在等待 U-Boot /result 完成实际写入")
			resultRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, resultURL, nil)
			if requestErr != nil {
				return requestErr
			}
			resultRequest.Header.Set("Accept", "*/*")
			resultRequest.Header.Set("Referer", updateAction)
			resultRequest.Header.Set("X-Requested-With", "XMLHttpRequest")
			resultResponse, resultErr := client.Do(resultRequest)
			if resultErr != nil {
				logf("U-Boot 实际写入后连接已断开，继续等待路由器重启亮灯")
				return nil
			}
			resultBody, readErr := io.ReadAll(io.LimitReader(resultResponse.Body, 4096))
			resultResponse.Body.Close()
			if readErr != nil {
				return readErr
			}
			if resultResponse.StatusCode < 200 || resultResponse.StatusCode >= 400 {
				return fmt.Errorf("U-Boot /result 返回 HTTP %d", resultResponse.StatusCode)
			}
			if strings.Contains(strings.ToLower(string(resultBody)), "failed") {
				logf("U-Boot /result 返回：" + strings.TrimSpace(string(resultBody)))
				return fmt.Errorf("%w：/result 返回 %s", errUBootUpdateFailed, strings.TrimSpace(string(resultBody)))
			}
			logf("U-Boot /result 已确认实际写入完成，等待路由器自动重启亮灯")
		}
	}
	logf("U-Boot 已接收固件，等待设备自动刷写和重启")
	return nil
}

func resolveURL(baseURL, relativeURL string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	relative, err := url.Parse(relativeURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(relative).String(), nil
}

func parseUpdateRequest(baseURL, html string) (string, string, url.Values, error) {
	if action, values, err := parseUpdateForm(baseURL, html); err == nil {
		return http.MethodPost, action, values, nil
	}
	match := onclickLocation.FindStringSubmatch(html)
	if len(match) >= 2 && strings.Contains(strings.ToLower(html), "update") {
		base, err := url.Parse(baseURL)
		if err != nil {
			return "", "", nil, err
		}
		relative, err := url.Parse(strings.TrimSpace(match[1]))
		if err != nil {
			return "", "", nil, err
		}
		return http.MethodGet, base.ResolveReference(relative).String(), url.Values{}, nil
	}
	return "", "", nil, errors.New("固件已上传，但无法识别 U-Boot 的 Update 确认按钮")
}

func parseUpdateForm(baseURL, html string) (string, url.Values, error) {
	for _, form := range formsPattern.FindAllString(html, -1) {
		lower := strings.ToLower(form)
		if !strings.Contains(lower, "update") || filePattern.MatchString(form) || filePattern2.MatchString(form) {
			continue
		}
		action := baseURL
		if tag := formPattern.FindString(form); tag != "" {
			if match := actionPattern.FindStringSubmatch(tag); len(match) >= 2 && strings.TrimSpace(match[1]) != "" {
				base, err := url.Parse(baseURL)
				if err != nil {
					return "", nil, err
				}
				relative, err := url.Parse(strings.TrimSpace(match[1]))
				if err != nil {
					return "", nil, err
				}
				action = base.ResolveReference(relative).String()
			}
		}
		values := url.Values{}
		for _, match := range hiddenPattern.FindAllStringSubmatch(form, -1) {
			values.Set(match[1], match[2])
		}
		if submit := inputPattern.FindString(form); submit != "" {
			name := namePattern.FindStringSubmatch(submit)
			value := valuePattern.FindStringSubmatch(submit)
			if len(name) >= 2 {
				buttonValue := "Update"
				if len(value) >= 2 {
					buttonValue = value[1]
				}
				values.Set(name[1], buttonValue)
			}
		}
		return action, values, nil
	}
	return "", nil, errors.New("固件已上传，但无法识别 U-Boot 的 Update 确认表单")
}

func WaitForUBoot(ctx context.Context, address string, timeout time.Duration, logf func(string)) (ProbeResult, error) {
	deadline := time.Now().Add(timeout)
	last := "尚未收到网页响应"
	for time.Now().Before(deadline) {
		result, err := ProbeWeb(ctx, address)
		if err == nil {
			last = result.Message
			if result.Kind == "uboot" {
				return result, nil
			}
		}
		select {
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	logf("等待 U-Boot 页面超时")
	return ProbeResult{}, fmt.Errorf("等待 U-Boot 页面超时：%s", last)
}

func waitForUBootLeave(ctx context.Context, address string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result, err := ProbeWeb(ctx, address)
		if err != nil || result.Kind != "uboot" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(1500 * time.Millisecond):
		}
	}
	return false
}

func WaitForOpenWrt(ctx context.Context, address string, timeout time.Duration, logf func(string)) (ProbeResult, error) {
	return WaitForOpenWrtAny(ctx, []string{address}, timeout, logf)
}

func WaitForOpenWrtAny(ctx context.Context, addresses []string, timeout time.Duration, logf func(string)) (ProbeResult, error) {
	deadline := time.Now().Add(timeout)
	last := "尚未收到网页响应"
	for time.Now().Before(deadline) {
		for _, address := range addresses {
			result, err := ProbeWeb(ctx, address)
			if err == nil {
				last = result.Message
				if result.Kind == "openwrt" {
					return result, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return ProbeResult{}, fmt.Errorf("等待 OpenWrt 超时：%s", last)
}

func parseUploadForm(baseURL, html string) (string, string, map[string]string, error) {
	uploadForm := uploadFormPattern.FindString(html)
	if uploadForm == "" {
		uploadForm = html
	}
	fileMatch := filePattern.FindStringSubmatch(uploadForm)
	if len(fileMatch) == 0 {
		fileMatch = filePattern2.FindStringSubmatch(uploadForm)
	}
	if len(fileMatch) < 2 || strings.TrimSpace(fileMatch[1]) == "" {
		return "", "", nil, errors.New("无法识别 U-Boot 文件上传字段")
	}
	action := baseURL
	if form := formPattern.FindString(uploadForm); form != "" {
		actionMatch := actionPattern.FindStringSubmatch(form)
		if len(actionMatch) >= 2 && strings.TrimSpace(actionMatch[1]) != "" {
			base, err := url.Parse(baseURL)
			if err != nil {
				return "", "", nil, err
			}
			relative, err := url.Parse(strings.TrimSpace(actionMatch[1]))
			if err != nil {
				return "", "", nil, err
			}
			action = base.ResolveReference(relative).String()
		}
	}
	hidden := map[string]string{}
	for _, match := range hiddenPattern.FindAllStringSubmatch(uploadForm, -1) {
		hidden[match[1]] = match[2]
	}
	return action, fileMatch[1], hidden, nil
}

func IsExpectedUploadPage(html string) bool {
	return bytes.Contains(bytes.ToLower([]byte(html)), []byte("firmware update"))
}
