package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type unusualTransport struct{}

func (unusualTransport) RoundTrip(*http.Request) (*http.Response, error) { panic("unused") }

func TestNotificationRouteDialGuards(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	for _, base := range []http.RoundTripper{http.DefaultTransport, unusualTransport{}} {
		for _, environment := range []bool{false, true} {
			transport := newNotifyTransport(base, environment)
			defer transport.CloseIdleConnections()
			if (transport.Proxy != nil) != environment {
				t.Fatal("unexpected proxy selection")
			}
			conn, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:9")
			if conn != nil {
				conn.Close()
			}
			if err == nil {
				t.Fatal("loopback dial must be blocked")
			}
		}
	}
}

func TestNotificationDirectAndInvalidRoute(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	received := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received++; w.Write([]byte(`{"ok":true}`)) }))
	defer target.Close()
	if _, err := doJSON(WithHTTPRoute(t.Context(), "direct"), "POST", target.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := doJSON(WithHTTPRoute(t.Context(), "invalid"), "POST", target.URL, nil, nil); err == nil {
		t.Fatal("unknown route accepted")
	}
	if received != 1 {
		t.Fatalf("received %d requests", received)
	}
	if directHTTPClient.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("direct client inherited proxy")
	}
	for _, client := range []*http.Client{directHTTPClient, environmentHTTPClient} {
		old, _ := http.NewRequest("POST", "https://example.com/send", nil)
		for _, address := range []string{"http://example.com/send", "https://other.example/send"} {
			next, _ := http.NewRequest("GET", address, nil)
			if client.CheckRedirect(next, []*http.Request{old}) == nil {
				t.Fatal("unsafe redirect accepted")
			}
		}
	}
}
