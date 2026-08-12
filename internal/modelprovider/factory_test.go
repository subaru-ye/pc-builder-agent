package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

func TestNewChatResponsesRequestProviderOptions(t *testing.T) {
	for _, tc := range []struct {
		provider    Provider
		cache       bool
		wantCache   bool
		wantMiMoKey bool
	}{
		{provider: ProviderBailian, cache: true, wantCache: true},
		{provider: ProviderMiMo, wantMiMoKey: true},
		{provider: ProviderOpenAICompatible},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			var path string
			var header http.Header
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path, header = r.URL.Path, r.Header.Clone()
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, completedResponseJSON)
			}))
			defer server.Close()
			cfg := Config{
				Provider: tc.provider, Role: RoleScreening, APIKey: "secret-key", BaseURL: server.URL,
				Model: "test-model", ReasoningEffort: "none", SessionCache: tc.cache,
				MaxRetries: 0, Timeout: time.Second,
			}
			chat, err := NewChat(context.Background(), cfg, "test")
			if err != nil {
				t.Fatal(err)
			}
			if err := consume(chat, "hello"); err != nil {
				t.Fatal(err)
			}
			if path != "/responses" {
				t.Fatalf("path=%q", path)
			}
			if header.Get("x-dashscope-session-cache") == "enable" != tc.wantCache {
				t.Fatalf("cache header=%q", header.Get("x-dashscope-session-cache"))
			}
			if (header.Get("api-key") == "secret-key") != tc.wantMiMoKey {
				t.Fatalf("MiMo api-key header presence=%v", header.Get("api-key") != "")
			}
			if header.Get("Authorization") != "Bearer secret-key" {
				t.Fatalf("authorization=%q", header.Get("Authorization"))
			}
			reasoning, ok := body["reasoning"].(map[string]any)
			if !ok || reasoning["effort"] != "none" {
				t.Fatalf("reasoning=%#v body=%#v", body["reasoning"], body)
			}
		})
	}
}

func TestNewChatClassifiesAndSanitizesUpstreamErrors(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"response-secret-body","code":"AllocationQuota.FreeTierOnly"}}`)
	}))
	defer server.Close()
	cfg := Config{Provider: ProviderBailian, Role: RoleScreening, APIKey: "request-secret-key",
		BaseURL: server.URL, Model: "test-model", ReasoningEffort: "none", Timeout: time.Second}
	chat, err := NewChat(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	err = consume(chat, "request-secret-body")
	var upstreamErr *upstream.Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Kind != upstream.KindQuota {
		t.Fatalf("err=%T %v", err, err)
	}
	if strings.Contains(err.Error(), "request-secret") || strings.Contains(err.Error(), "response-secret") {
		t.Fatalf("错误泄露敏感正文:%v", err)
	}
	if requests != 1 {
		t.Fatalf("MODEL_MAX_RETRIES=0 时请求次数=%d want 1", requests)
	}
}

func TestMiMoNonReasoningFunctionCall(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
		  "id":"resp_tool","object":"response","created_at":1,"status":"completed","model":"mimo-v2.5",
		  "output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"search_parts","arguments":"{\"category\":\"gpu\",\"top_n\":5}","status":"completed"}],
		  "usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}
		}`)
	}))
	defer server.Close()
	cfg := Config{Provider: ProviderMiMo, Role: RoleBuilder, APIKey: "mimo-key", BaseURL: server.URL,
		Model: "mimo-v2.5", ReasoningEffort: "none", Timeout: time.Second}
	chat, err := NewChat(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{
		Model: chat.Name(), Contents: []*genai.Content{genai.NewContentFromText("查找显卡", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name: "search_parts", ParametersJsonSchema: map[string]any{"type": "object"},
		}}}}},
	}
	gotCall := false
	for resp, callErr := range chat.GenerateContent(context.Background(), req, false) {
		if callErr != nil {
			t.Fatal(callErr)
		}
		if resp != nil && resp.Content != nil {
			for _, part := range resp.Content.Parts {
				gotCall = gotCall || part.FunctionCall != nil && part.FunctionCall.Name == "search_parts"
			}
		}
	}
	if !gotCall {
		t.Fatal("MiMo Responses function_call 未被 ADK 适配")
	}
	reasoning, _ := requestBody["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Fatalf("reasoning=%#v", reasoning)
	}
	if tools, ok := requestBody["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("tools=%#v", requestBody["tools"])
	}
}

func consume(chat model.LLM, text string) error {
	var result error
	req := &model.LLMRequest{Model: chat.Name(), Contents: []*genai.Content{genai.NewContentFromText(text, genai.RoleUser)}}
	for _, err := range chat.GenerateContent(context.Background(), req, false) {
		if err != nil {
			result = err
		}
	}
	return result
}

const completedResponseJSON = `{
  "id":"resp_test","object":"response","created_at":1,"status":"completed","model":"test-model",
  "output":[{"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}],
  "usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
}`
