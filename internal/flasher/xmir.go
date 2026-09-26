package flasher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrXMiRRuntime = errors.New("XMiR 运行环境不可用")

func RunXMiRUnlock(ctx context.Context, runBat, host, webPassword string, logf func(string)) error {
	runBat = strings.TrimSpace(runBat)
	root := filepath.Dir(runBat)
	python := filepath.Join(root, "python", "python.exe")
	connectScript := filepath.Join(root, "connect.py")
	for _, path := range []string{runBat, python, connectScript} {
		if stat, err := os.Stat(path); err != nil || !stat.Mode().IsRegular() {
			return fmt.Errorf("%w：文件不存在：%s", ErrXMiRRuntime, path)
		}
	}
	if strings.TrimSpace(webPassword) == "" {
		return errors.New("小米后台密码不能为空")
	}
	check := exec.CommandContext(ctx, python, "-c", `import os,sys; sys.path.insert(0,os.getcwd()); import gateway`)
	check.Dir = root
	if output, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("%w：%v；%s", ErrXMiRRuntime, err, strings.TrimSpace(string(output)))
	}
	wrapper := `import os,sys; sys.path.insert(0,os.getcwd()); from gateway import Gateway; gw=Gateway(detect_device=False,detect_ssh=False); gw.webpassword=os.environ.pop("AX9000_WEB_PASSWORD"); gw.ip_addr=sys.argv[1]; import connect`
	command := exec.CommandContext(ctx, python, "-c", wrapper, strings.TrimSpace(host))
	command.Dir = root
	command.Env = append(os.Environ(), "AX9000_WEB_PASSWORD="+webPassword)
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动 XMiR 失败：%w", err)
	}
	_, _ = io.WriteString(stdin, "root\nroot\n")
	_ = stdin.Close()
	var output strings.Builder
	var mu sync.Mutex
	var wait sync.WaitGroup
	copyOutput := func(reader io.Reader) {
		defer wait.Done()
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			mu.Lock()
			output.WriteString(line)
			output.WriteByte('\n')
			mu.Unlock()
			logf("XMiR：" + line)
		}
	}
	wait.Add(2)
	go copyOutput(stdout)
	go copyOutput(stderr)
	err = command.Wait()
	wait.Wait()
	if err != nil {
		text := strings.TrimSpace(output.String())
		if len(text) > 1200 {
			text = text[len(text)-1200:]
		}
		return fmt.Errorf("XMiR 自动解锁失败：%w；%s", err, text)
	}
	return nil
}

func WaitForSSH(ctx context.Context, cfg SSHConfig, timeout time.Duration, logf func(string)) (RouterInfo, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		info, err := InspectRouter(ctx, cfg)
		if err == nil {
			return info, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return RouterInfo{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	logf("SSH 等待超时")
	return RouterInfo{}, fmt.Errorf("等待 SSH 就绪超时：%w", lastErr)
}
