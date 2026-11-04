package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCurrentListsRequireAnExplicitCompleteArray(t *testing.T) {
	for _, test := range []struct {
		name  string
		body  string
		valid bool
	}{
		{name: "empty", body: `{"metadata":{"resourceVersion":"rv"},"items":[]}`, valid: true},
		{name: "missing", body: `{"metadata":{"resourceVersion":"rv"}}`},
		{name: "null", body: `{"metadata":{"resourceVersion":"rv"},"items":null}`},
		{name: "wrong_type", body: `{"metadata":{"resourceVersion":"rv"},"items":{}}`},
		{name: "truncated", body: `{"metadata":{"resourceVersion":"rv"},"items":[`},
		{name: "continued", body: `{"metadata":{"resourceVersion":"rv","continue":"next"},"items":[]}`},
		{name: "trailing_document", body: `{"metadata":{"resourceVersion":"rv"},"items":[]} {}`},
		{name: "trailing_invalid", body: `{"metadata":{"resourceVersion":"rv"},"items":[]} invalid`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			reader := &kubeReader{config: Connection{APIServer: server.URL}, client: server.Client()}
			_, _, podErr := reader.pods(context.Background())
			_, nodeErr := reader.nodes(context.Background())
			if (podErr == nil) != test.valid || (nodeErr == nil) != test.valid {
				t.Fatalf("list validity: pods=%v nodes=%v; want valid=%v", podErr, nodeErr, test.valid)
			}
		})
	}
}
