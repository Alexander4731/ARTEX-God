package server

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestStaticUICacheMatchesServedFile(t *testing.T) {
	root := fstest.MapFS{
		"index.html":                        {Data: []byte("root html")},
		"tasks/index.html":                  {Data: []byte("tasks html")},
		"_next/static/chunks/app-abc123.js": {Data: []byte("build asset")},
		"favicon.ico":                       {Data: []byte("icon")},
	}
	for _, tc := range []struct{ url, body, cache string }{
		{"/", "root html", "no-cache"},
		{"/tasks/", "tasks html", "no-cache"},
		{"/unknown-route", "root html", "no-cache"},
		{"/favicon.ico", "icon", "no-cache"},
		{"/_next/static/chunks/app-abc123.js", "build asset", "public, max-age=31536000, immutable"},
		{"/_next/static/chunks/old-build.js", "root html", "no-cache"},
		{"/nested/_next/static/missing.js", "root html", "no-cache"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			w := httptest.NewRecorder()
			staticUIHandler(root).ServeHTTP(w, httptest.NewRequest("GET", tc.url, nil))
			if w.Code != 200 || w.Body.String() != tc.body || w.Header().Get("Cache-Control") != tc.cache {
				t.Fatalf("status=%d body=%q cache=%q", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
			}
		})
	}
}
