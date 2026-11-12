package selfupdate

import (
	"archive/zip"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseMetadataBoundaries(t *testing.T) {
	valid := `{"tag_name":"v1"}`
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"exact", valid + strings.Repeat(" ", maxReleaseJSON-len(valid)), true},
		{"overflow", valid + strings.Repeat(" ", maxReleaseJSON-len(valid)+1), false},
		{"trailing", valid + ` {"tag_name":"v2"}`, false},
		{"incomplete", `{"tag_name":`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: updateTransportFunc(func(r *http.Request) (*http.Response, error) { return updateResponse(r, 200, tc.body), nil })}
			_, err := FetchLatest(t.Context(), client)
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestExtractRejectsAmbiguousAndInvalidMembers(t *testing.T) {
	want := "artex"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	for _, tc := range []struct {
		name    string
		members []string
		mode    os.FileMode
	}{
		{"duplicate", []string{"pkg/" + want, "other/" + want}, 0},
		{"same-name", []string{"pkg/" + want, "pkg/" + want}, 0},
		{"traversal", []string{"../" + want}, 0},
		{"absolute", []string{"/" + want}, 0},
		{"backslash", []string{"pkg\\" + want}, 0},
		{"symlink", []string{"pkg/" + want}, os.ModeSymlink | 0o777},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "release.zip")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(f)
			for _, name := range tc.members {
				h := &zip.FileHeader{Name: name}
				if tc.mode != 0 {
					h.SetMode(tc.mode)
				}
				w, err := zw.CreateHeader(h)
				if err != nil {
					t.Fatal(err)
				}
				w.Write([]byte("binary"))
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			dst := filepath.Join(dir, "existing")
			if err := os.WriteFile(dst, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := extractBinary(archive, dst); err == nil {
				t.Fatal("invalid package accepted")
			}
			raw, err := os.ReadFile(dst)
			if err != nil || string(raw) != "original" {
				t.Fatalf("destination changed: %q %v", raw, err)
			}
		})
	}
}
