package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

// 核验真实 ADK→Responses 适配边界：重复执行仍携带初筛规则，且不串入上一题历史。
func TestScreeningRequestsKeepInstructionsAndIsolateSessions(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","status":"completed","model":"screening-test","output":[{"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"请提供预算？","annotations":[]}]}]}`))
	}))
	defer server.Close()
	r, err := newADKScreeningRunner(context.Background(), modelprovider.Config{
		Provider: modelprovider.ProviderBailian, Role: modelprovider.RoleScreening,
		APIKey: "test-key", BaseURL: server.URL, Model: "screening-test", Timeout: 5 * time.Second,
		SessionCache: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []string{"第一题独立输入", "第二题独立输入", "第三题独立输入"}
	for _, input := range inputs {
		if reply, err := r.Run(context.Background(), input); err != nil || reply != "请提供预算？" {
			t.Fatalf("reply=%q err=%v", reply, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != len(inputs) {
		t.Fatalf("request count=%d", len(requests))
	}
	for i, request := range requests {
		var instruction string
		if err := json.Unmarshal(request["instructions"], &instruction); err != nil || !strings.Contains(instruction, "已有配件") || !strings.Contains(instruction, "游戏名称") || !strings.Contains(instruction, "所有新装机需求的内部草稿协议") {
			t.Fatalf("request %d lost screening instructions", i)
		}
		var input []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(request["input"], &input); err != nil || len(input) != 1 || input[0].Role != "user" || len(input[0].Content) != 1 || input[0].Content[0].Text != inputs[i] {
			t.Fatalf("request %d contains unexpected history: %s", i, request["input"])
		}
	}
}
