package jev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/decision"
)

func validResponse(choice string) string {
	return fmt.Sprintf(`{"model":"jev-1.13.0","answers":{%q:{"type":"choice","choice":%q,"probabilities":{%q:0.7,%q:0.2,%q:0.06,%q:0.04},"confidence":0.81}},"usage":{"input_tokens":318,"output_tokens":34}}`,
		questionID, choice,
		string(decision.IntentCollect), string(decision.IntentConfirm), string(decision.IntentPlan), string(decision.IntentAmbiguous))
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "k-test", BaseURL: srv.URL, Model: ModelPin})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func classify(c *Client) (decision.IntentResult, error) {
	return c.Classify(context.Background(), decision.IntentInput{CurrentTurn: "预算8000，主要玩游戏"})
}

func TestClassifySuccess(t *testing.T) {
	var seenPath, seenAuth, seenBody string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validResponse(string(decision.IntentConfirm))))
	})
	res, err := classify(c)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if seenPath != Path {
		t.Fatalf("path = %q, want %q", seenPath, Path)
	}
	if seenAuth != "Bearer k-test" {
		t.Fatalf("auth header = %q", seenAuth)
	}
	for _, want := range []string{`"model":"jev-1.13.0"`, `"current_turn"`, `"type":"choice"`, string(decision.IntentAmbiguous)} {
		if !strings.Contains(seenBody, want) {
			t.Fatalf("request body missing %s in %s", want, seenBody)
		}
	}
	if res.Intent != decision.IntentConfirm || res.Confidence != 0.81 || res.SelectedProbability != 0.2 {
		t.Fatalf("unexpected result %+v", res)
	}
	if len(res.Probabilities) != 4 || res.Probabilities[decision.IntentAmbiguous] != 0.04 {
		t.Fatalf("probabilities = %+v", res.Probabilities)
	}
	if res.ResponseModel != "jev-1.13.0" || res.RequestedModel != ModelPin {
		t.Fatalf("models recorded: requested=%q response=%q", res.RequestedModel, res.ResponseModel)
	}
	if res.InputTokens != 318 || res.OutputTokens != 34 {
		t.Fatalf("usage = %d/%d", res.InputTokens, res.OutputTokens)
	}
}

func TestClassifyContractFailures(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		class string
	}{
		{"malformed json", `{"model":`, ClassDecode},
		{"answer missing", `{"model":"m","answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"answer type wrong", `{"model":"m","answers":{"next_action":{"type":"score","choice":"collect","probabilities":{"collect":1},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"unknown option", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"explain","probabilities":{"collect":1},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"probabilities missing option", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":1},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"probability out of range", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":1.5,"confirm":0,"plan":0,"ambiguous":0},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"probability sum drift", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":0.8,"confirm":0.05,"plan":0.05,"ambiguous":0.05},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"selected zero probability allowed", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"plan","probabilities":{"collect":0.34,"confirm":0.33,"plan":0.0,"ambiguous":0.33},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, ""},
		{"confidence out of range", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":1,"confirm":0,"plan":0,"ambiguous":0},"confidence":1.2}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"usage missing", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":1,"confirm":0,"plan":0,"ambiguous":0},"confidence":0.5}}}`, ClassContract},
		{"usage negative", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":1,"confirm":0,"plan":0,"ambiguous":0},"confidence":0.5}},"usage":{"input_tokens":-1,"output_tokens":1}}`, ClassContract},
		{"model missing", `{"answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":1,"confirm":0,"plan":0,"ambiguous":0},"confidence":0.5}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
		{"extra option", `{"model":"m","answers":{"next_action":{"type":"choice","choice":"collect","probabilities":{"collect":0.9,"confirm":0.05,"plan":0.03,"ambiguous":0.02,"explain":0},"confidence":0.5}},"usage":{"input_tokens":1,"output_tokens":1}}`, ClassContract},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := classify(c)
			if got := Class(err); got != tc.class {
				t.Fatalf("class = %q err=%v, want %q", got, err, tc.class)
			}
		})
	}
}

func TestClassifyStatusClassification(t *testing.T) {
	cases := []struct {
		status int
		class  string
	}{
		{http.StatusUnauthorized, ClassAuth},
		{http.StatusUnprocessableEntity, ClassInvalidRequest},
		{http.StatusTooManyRequests, ClassRateLimited},
		{529, ClassProviderOverloaded},
		{http.StatusInternalServerError, ClassUnexpectedStatus},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"x"}`))
			})
			_, err := classify(c)
			if got := Class(err); got != tc.class {
				t.Fatalf("class = %q, want %q (err %v)", got, tc.class, err)
			}
			var e *Error
			if !errors.As(err, &e) || e.StatusCode != tc.status {
				t.Fatalf("status code not recorded: %v", err)
			}
		})
	}
}

func TestClassifyTimeoutAndCancellation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(validResponse("collect")))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Classify(ctx, decision.IntentInput{}); Class(err) != ClassTimeout {
		t.Fatalf("class = %v, want timeout", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := c.Classify(ctx, decision.IntentInput{}); Class(err) != ClassCanceled {
		t.Fatalf("class = %v, want canceled", err)
	}
}

func TestClassifyTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	c, err := New(Config{APIKey: "k", BaseURL: url, Model: ModelPin})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err = classify(c); Class(err) != ClassTransport {
		t.Fatalf("class = %v, want transport", err)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{Model: "m"}); err == nil {
		t.Fatal("expected API key requirement")
	}
	if _, err := New(Config{APIKey: "k"}); err == nil {
		t.Fatal("expected model requirement")
	}
	if _, err := New(Config{APIKey: "k", Model: "m", BaseURL: "://broken"}); err == nil {
		t.Fatal("expected base URL validation")
	}
}

func TestQuestionHashStable(t *testing.T) {
	if len(QuestionHash()) != 64 {
		t.Fatalf("unexpected hash %q", QuestionHash())
	}
	again := QuestionHash()
	if again != QuestionHash() {
		t.Fatal("question hash is not deterministic")
	}
}
