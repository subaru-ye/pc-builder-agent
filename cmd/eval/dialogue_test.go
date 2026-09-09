package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

func TestDialogueUsesActualRepliesAndIsolatesCases(t *testing.T) {
	var inputs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		inputs = append(inputs, string(body["input"]))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","status":"completed","model":"screening-test","output":[{"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"请提供预算金额。","annotations":[]}]}]}`))
	}))
	defer server.Close()
	r, err := newADKScreeningRunner(context.Background(), modelprovider.Config{Provider: modelprovider.ProviderBailian, Role: modelprovider.RoleScreening, APIKey: "test-key", BaseURL: server.URL, Model: "screening-test", Timeout: time.Second * 5})
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.RunDialogue(context.Background(), []string{"办公主机甲", "补充第二轮"})
	if err != nil || len(first) != 2 {
		t.Fatal(err)
	}
	if !strings.Contains(inputs[1], "办公主机甲") || !strings.Contains(inputs[1], "请提供预算金额") || !strings.Contains(inputs[1], "补充第二轮") {
		t.Fatalf("missing actual context: %s", inputs[1])
	}
	if len(first[1].UserSources) != 2 || first[1].UserSources[0] != "办公主机甲" {
		t.Fatalf("assistant included in sources: %v", first[1].UserSources)
	}
	c := evalsuite.Case{Turns: []evalsuite.ScreeningTurn{{Input: "办公主机甲"}, {Input: "补充第二轮"}}}
	if err := evalsuite.CheckDialogueEvidence(c, evalsuite.ScreeningOutput{Turns: first}); err != nil {
		t.Fatal(err)
	}
	first[1].Context += "额外答案"
	if err := evalsuite.CheckDialogueEvidence(c, evalsuite.ScreeningOutput{Turns: first}); err == nil {
		t.Fatal("injected context accepted")
	}
	if _, err := r.RunDialogue(context.Background(), []string{"全新对话乙", "预算待定"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inputs[2], "办公主机甲") || strings.Contains(inputs[2], "补充第二轮") {
		t.Fatal("cross-case history leak")
	}
}
