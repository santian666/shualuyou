package flasher

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectXiaomi(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/cgi-bin/luci/web" {
			t.Fatalf("请求路径 = %q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`hardware = 'RA70'; romVersion: '1.0.168'; var deviceId = 'AA:BB'; key: 'nonce-key',`))
	}))
	defer server.Close()
	info, err := InspectXiaomi(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "RA70" || info.Version != "1.0.168" {
		t.Fatalf("识别结果异常：%+v", info)
	}
}

func TestXiaomiHash(t *testing.T) {
	if got := xiaomiHash("abc", 0); got != "a9993e364706816aba3e25717850c26c9cd0d89d" {
		t.Fatalf("SHA1 = %s", got)
	}
	if got := xiaomiHash("abc", 1); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("SHA256 = %s", got)
	}
}

func TestUploadXiaomiROMHasContentLength(t *testing.T) {
	want := []byte("firmware-content")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength <= int64(len(want)) {
			t.Fatalf("Content-Length 未包含完整 multipart 内容：%d", request.ContentLength)
		}
		part, err := request.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		filePart, err := part.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(filePart)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("上传内容 = %q", got)
		}
	}))
	defer server.Close()
	firmware := filepath.Join(t.TempDir(), "ra70.bin")
	if err := os.WriteFile(firmware, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := uploadXiaomiROM(context.Background(), server.URL, "token", firmware); err != nil {
		t.Fatal(err)
	}
}
