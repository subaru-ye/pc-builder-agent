package embedding

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

func TestClientUsesEmbeddingsEndpointAndValidatesDimensions(t *testing.T) {
	var path, auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":3,"total_tokens":3}}`)
	}))
	defer server.Close()
	client := NewClientWithOptions(Options{BaseURL: server.URL, APIKey: "secret-key", Model: "embed-model",
		Dimensions: 2, Timeout: time.Second, Provider: "bailian", Role: "embedding"})
	vector, err := client.EmbedOne(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/embeddings" || auth != "Bearer secret-key" || len(vector) != 2 {
		t.Fatalf("path=%q auth=%q vector=%v", path, auth, vector)
	}
}

func TestClientClassifiesAndSanitizesErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		kind   upstream.Kind
	}{
		{name: "quota", status: 403, body: `{"error":{"code":"AllocationQuota.FreeTierOnly","message":"secret-body"}}`, kind: upstream.KindQuota},
		{name: "rate limit", status: 429, body: `{"error":{"code":"rate_limit","message":"secret-body"}}`, kind: upstream.KindRateLimit},
		{name: "malformed", status: 200, body: `{`, kind: upstream.KindProtocol},
		{name: "dimension", status: 200, body: `{"data":[{"index":0,"embedding":[0.1]}]}`, kind: upstream.KindProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := NewClientWithOptions(Options{BaseURL: server.URL, APIKey: "secret-key", Model: "embed-model",
				Dimensions: 2, Timeout: time.Second, Provider: "bailian", Role: "embedding"})
			_, err := client.EmbedOne(context.Background(), "request-secret")
			var upstreamErr *upstream.Error
			if !errors.As(err, &upstreamErr) || upstreamErr.Kind != tc.kind {
				t.Fatalf("err=%T %v", err, err)
			}
			if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "secret-body") ||
				strings.Contains(err.Error(), "request-secret") {
				t.Fatalf("错误泄露敏感内容:%v", err)
			}
		})
	}
}
