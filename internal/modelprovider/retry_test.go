package modelprovider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

// 上游把 200 响应写断时 openai-go 自带重试不会重发，必须由本层按
// MODEL_MAX_RETRIES 补一次尝试；评估侧该值恒为 0，保持批次可复现。
func TestTransientRetryResendsTruncatedResponse(t *testing.T) {
	for _, tc := range []struct {
		name         string
		retries      int
		wantRequests int
		wantErr      bool
	}{
		{name: "zero_retries", retries: 0, wantRequests: 1, wantErr: true},
		{name: "one_retry_recovers", retries: 1, wantRequests: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				if requests == 1 {
					_, _ = io.WriteString(w, `{"id":"resp_cut","object":"response","created_at":1,"status":"compl`)
					return
				}
				_, _ = io.WriteString(w, completedResponseJSON)
			}))
			defer server.Close()
			chat, err := NewChat(t.Context(), Config{Provider: ProviderBailian, Role: RoleBuilder, APIKey: "key",
				BaseURL: server.URL, Model: "test-model", ReasoningEffort: "none", Timeout: 2 * time.Second,
				MaxRetries: tc.retries}, "test")
			if err != nil {
				t.Fatal(err)
			}
			err = consume(chat, "hi")
			if tc.wantErr {
				var ue *upstream.Error
				if !errors.As(err, &ue) || ue.Kind != upstream.KindProtocol {
					t.Fatalf("truncated response must surface as protocol error: %v", err)
				}
			} else if err != nil {
				t.Fatalf("retry must recover: %v", err)
			}
			if requests != tc.wantRequests {
				t.Fatalf("requests=%d want %d", requests, tc.wantRequests)
			}
		})
	}
}

// 额度类失败不重发：换模型或如实报错才是正确出路，重试只会重复计费。
func TestQuotaFailureIsNotRetried(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"body","code":"AllocationQuota.FreeTierOnly"}}`)
	}))
	defer server.Close()
	chat, err := NewChat(t.Context(), Config{Provider: ProviderBailian, Role: RoleBuilder, APIKey: "key",
		BaseURL: server.URL, Model: "test-model", ReasoningEffort: "none", Timeout: 2 * time.Second, MaxRetries: 1}, "test")
	if err != nil {
		t.Fatal(err)
	}
	err = consume(chat, "hi")
	var ue *upstream.Error
	if !errors.As(err, &ue) || ue.Kind != upstream.KindQuota {
		t.Fatalf("quota classification lost: %v", err)
	}
	if requests != 1 {
		t.Fatalf("quota failure must not be resent: requests=%d", requests)
	}
}
