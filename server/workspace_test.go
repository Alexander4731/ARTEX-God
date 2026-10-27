package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceConfinesLinkedPaths(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	canary := filepath.Join(outside, "canary.txt")
	if err := os.WriteFile(canary, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "escape")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Fatalf("junction: %v %s", err, output)
		}
	}
	s := &Server{m: &Manager{dir: base}}
	for _, name := range []string{"read", "download", "write", "upload", "mkdir"} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			path := "escape/canary.txt"
			switch name {
			case "read":
				s.wsRead(w, httptest.NewRequest("GET", "/?path="+url.QueryEscape(path), nil))
			case "download":
				s.wsDownload(w, httptest.NewRequest("GET", "/?path="+url.QueryEscape(path), nil))
			case "write":
				s.wsWrite(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"path":"escape/canary.txt","content":"changed"}`)))
			case "mkdir":
				s.wsMkdir(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"path":"escape/new"}`)))
			case "upload":
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				file, err := form.CreateFormFile("file", "canary.txt")
				if err != nil {
					t.Fatal(err)
				}
				file.Write([]byte("changed"))
				form.Close()
				r := httptest.NewRequest("POST", "/?path=escape", &body)
				r.Header.Set("Content-Type", form.FormDataContentType())
				s.wsUpload(w, r)
			}
			if w.Code < 400 {
				t.Fatalf("escaped root: status=%d", w.Code)
			}
			data, err := os.ReadFile(canary)
			if err != nil || string(data) != "outside" {
				t.Fatalf("canary changed: %q %v", data, err)
			}
		})
	}
	w := httptest.NewRecorder()
	s.wsWrite(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"path":"inside/note.txt","content":"hello"}`)))
	if w.Code != 200 {
		t.Fatalf("normal write: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.wsRead(w, httptest.NewRequest("GET", "/?path=inside/note.txt", nil))
	var value struct {
		Content string `json:"content"`
	}
	json.Unmarshal(w.Body.Bytes(), &value)
	if w.Code != 200 || value.Content != "hello" {
		t.Fatalf("normal read: %d %s", w.Code, w.Body.String())
	}
}
