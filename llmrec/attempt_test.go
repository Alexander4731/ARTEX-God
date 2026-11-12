package llmrec

import (
	"fmt"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNativeEmptyRetryMetersEveryAttempt(t *testing.T) {
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	for _, stream := range []bool{false, true} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			content := ""
			if calls > 1 {
				content = "ok"
			}
			if stream {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n", content)
			} else {
				fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`, content)
			}
		}))
		defer server.Close()
		provider, err := llm.NewProvider(llm.Config{Format: llm.FormatOpenAI, BaseURL: server.URL, Model: "fixture", MaxRetries: -1, EmptyResponseRetries: 1, EmptyResponseInterval: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		profile := uuid.NewString()
		defer pg.Exec("DELETE FROM llm_usage WHERE profile_name=$1", profile)
		recorder := Wrap(provider, pg, "fixture", profile, "", "", func() bool { return false })
		if stream {
			for _, err := range recorder.Stream(t.Context(), llm.CompletionRequest{}) {
				if err != nil {
					t.Fatal(err)
				}
			}
		} else {
			if _, _, _, err := recorder.Complete(t.Context(), llm.CompletionRequest{}); err != nil {
				t.Fatal(err)
			}
		}
		var rows, callIDs, attempts, input, output int
		err = pg.QueryRow("SELECT count(*),count(DISTINCT call_id),count(DISTINCT attempt_id),sum(input_tokens),sum(output_tokens) FROM llm_usage WHERE profile_name=$1", profile).Scan(&rows, &callIDs, &attempts, &input, &output)
		if err != nil || rows != 2 || callIDs != 1 || attempts != 2 || input != 14 || output != 6 {
			t.Fatalf("rows=%d calls=%d attempts=%d usage=%d/%d err=%v", rows, callIDs, attempts, input, output, err)
		}
	}
}
