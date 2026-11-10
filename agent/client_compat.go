package agent

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
	"github.com/google/uuid"
)

// These compatibility identities are pinned together, rather than discovered by
// starting external CLI processes. Authentication and wire bodies stay with the
// selected provider; this does not turn Chat Completions into the Responses API.
const (
	codexUserAgent  = "codex_cli_rs/0.167.0"
	claudeUserAgent = "claude-cli/2.1.316 (external, cli)"
)

// ApplyClientCompat adds CLI identification headers to an owned request. Shared
// callers must clone the request first. It never replaces authentication headers.
func ApplyClientCompat(req *http.Request, format llm.Format) {
	sid := transcript.SessionIDFrom(req.Context())
	if sid == "" {
		sid = uuid.NewString()
	} else {
		// ARTEX session names are not UUIDs. Derive a stable, opaque identifier
		// without exposing task names or sharing one identity across sessions.
		sid = uuid.NewSHA1(uuid.NameSpaceURL, []byte("artex:"+sid)).String()
	}
	req.Header.Set("x-client-request-id", uuid.NewString())
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if format != llm.FormatAnthropic {
		req.Header.Set("User-Agent", codexUserAgent)
		req.Header.Set("originator", "codex_cli_rs")
		req.Header.Set("session_id", sid)
		return
	}
	req.Header.Set("User-Agent", claudeUserAgent)
	req.Header.Set("x-app", "cli")
	req.Header.Set("x-client-session-id", sid)
	req.Header.Set("x-stainless-lang", "js")
	req.Header.Set("x-stainless-runtime", "node")
	osName := map[string]string{"windows": "Windows", "darwin": "MacOS", "linux": "Linux"}[runtime.GOOS]
	if osName == "" {
		osName = "Other:" + runtime.GOOS
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	req.Header.Set("x-stainless-os", osName)
	req.Header.Set("x-stainless-arch", arch)
	if req.Header.Get("anthropic-version") == "" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	const beta = "claude-code-20250219"
	for _, value := range req.Header.Values("anthropic-beta") {
		for _, flag := range strings.Split(value, ",") {
			if strings.TrimSpace(flag) == beta {
				return
			}
		}
	}
	flags := req.Header.Values("anthropic-beta")
	req.Header.Set("anthropic-beta", strings.Join(append(flags, beta), ","))
}
