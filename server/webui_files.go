package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticUIHandler serves the exported UI with cache policy based on the file
// actually returned. Missing build assets must not cache the HTML fallback.
func staticUIHandler(root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		// 1) exact file (assets: _next/*, favicon.ico, ...)
		// 2) route dir → <p>/index.html (trailingSlash export)
		// 3) <p>.html
		// 4) SPA 兜底 → index.html（交给客户端路由）
		for _, cand := range []string{p, p + "/index.html", p + ".html"} {
			if serveFileIfExists(w, r, root, cand) {
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, root, "index.html")
	})
}

// serveFileIfExists serves name from fsys when it exists as a regular file.
func serveFileIfExists(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	st, statErr := f.Stat()
	_ = f.Close()
	if statErr != nil || st.IsDir() {
		return false
	}
	w.Header().Set("Cache-Control", "no-cache")
	if strings.HasPrefix(name, "_next/static/") && !strings.HasSuffix(name, ".html") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, fsys, name)
	return true
}
