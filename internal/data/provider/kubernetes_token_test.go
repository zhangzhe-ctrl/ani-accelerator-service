package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestKubeReaderReloadsTokenFileForCurrentReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kube-token")
	credential := "first-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+credential {
			http.Error(w, "wrong token", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"resourceVersion":"rv"},"items":[]}`))
	}))
	defer server.Close()
	reader := &kubeReader{config: Connection{APIServer: server.URL, TokenFile: path}, client: server.Client()}
	for _, token := range []string{"first-token", "rotated-token"} {
		credential = token
		if err := os.WriteFile(path, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		if _, ref, err := reader.pods(context.Background()); err != nil || ref != "rv" {
			t.Fatalf("current token was not reloaded: %q, %v", ref, err)
		}
	}
}
