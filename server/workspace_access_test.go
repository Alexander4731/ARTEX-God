package server

import (
	"bytes"
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

func TestWorkspaceProtectsManagedFilesAndLinks(t *testing.T) {
	base := t.TempDir()
	s := &Server{m: &Manager{dir: base}}
	for _, dir := range []string{"traffic", "evidence", "archives", "transcripts", "sessions", "tasks/42/work"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	canary := filepath.Join(base, "evidence", "canary.txt")
	if err := os.WriteFile(canary, []byte("system data"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := []string{"evidence/canary.txt", "transcripts/execution-receipts/1/new.txt", "archives/task.tar.zst"}
	if runtime.GOOS == "windows" {
		paths = append(paths, "EvIdEnCe/canary.txt", "evidence./canary.txt", "evidence/canary.txt:stream")
	}
	link := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(base, "evidence"), link); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		if out, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, filepath.Join(base, "evidence")).CombinedOutput(); err != nil {
			t.Fatalf("junction: %v %s", err, out)
		}
	}
	paths = append(paths, "alias/canary.txt")
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			body := `{"path":"` + path + `","content":"changed"}`
			w := httptest.NewRecorder()
			s.wsWrite(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
			if w.Code < 400 {
				t.Fatalf("protected write accepted: %s", path)
			}
		})
	}
	for _, path := range []string{"evidence", "archives", "transcripts", "tasks", "tasks/42", "alias/canary.txt"} {
		w := httptest.NewRecorder()
		s.wsDelete(w, httptest.NewRequest("DELETE", "/?path="+url.QueryEscape(path), nil))
		if w.Code < 400 {
			t.Fatalf("protected deletion accepted: %s", path)
		}
	}
	w := httptest.NewRecorder()
	s.wsMkdir(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"path":"alias/new"}`)))
	if w.Code < 400 {
		t.Fatal("mkdir through alias")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, _ := form.CreateFormFile("file", "canary.txt")
	file.Write([]byte("upload"))
	form.Close()
	req := httptest.NewRequest("POST", "/?path=alias", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w = httptest.NewRecorder()
	s.wsUpload(w, req)
	if w.Code < 400 {
		t.Fatal("upload through alias")
	}
	// Replacing a user hard link must preserve the source system inode.
	hard := filepath.Join(base, "user-hard.txt")
	if err := os.Link(canary, hard); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.wsWrite(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"path":"user-hard.txt","content":"user edit"}`)))
	if w.Code != 200 {
		t.Fatalf("ordinary user replacement failed: %d %s", w.Code, w.Body.String())
	}
	got, err := os.ReadFile(canary)
	if err != nil || string(got) != "system data" {
		t.Fatalf("managed data changed: %q %v", got, err)
	}
	w = httptest.NewRecorder()
	s.wsWrite(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"path":"tasks/42/work/result.txt","content":"result"}`)))
	if w.Code != 200 {
		t.Fatalf("task artifact blocked: %d", w.Code)
	}
}
