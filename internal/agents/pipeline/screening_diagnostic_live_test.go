package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
)

type singleDiagnosticModel struct {
	model.LLM
	calls int
}

func (m *singleDiagnosticModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.calls >= 1 {
			yield(nil, errors.New("diagnostic call limit reached"))
			return
		}
		m.calls++
		m.LLM.GenerateContent(ctx, req, stream)(yield)
	}
}

// Explicit one-turn diagnosis, with no model chain, SDK retry, Builder or database writes.
func TestScreeningSingleDiagnostic(t *testing.T) {
	if os.Getenv("SCREENING_SINGLE_DIAGNOSTIC") != "1" {
		t.Skip("explicit diagnostic only; maximum one Screening call")
	}
	root := filepath.Join("..", "..", "..")
	dotenv.Load(filepath.Join(root, ".env"))
	cfg, err := modelprovider.Load(modelprovider.RoleScreening)
	if err != nil || len(cfg.ModelChain) != 0 {
		t.Fatal("invalid fixed-model configuration")
	}
	cfg.MaxRetries = 0
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	inner, err := modelprovider.NewChat(ctx, cfg, "screening-single-diagnostic")
	if err != nil {
		t.Fatal("cannot construct Screening")
	}
	counted := &requirementLiveModel{inner: &singleDiagnosticModel{LLM: inner}}
	state := schemas.NewRequirementState()
	ctx = WithRequirementState(ctx, state, schemas.RequirementSource{Kind: "chat", MessageID: "diagnostic", Quote: "预算 6000，主要剪 4K 视频，尽量安静"})
	var failure string
	for _, err := range (screeningGuard{LLM: counted}).GenerateContent(ctx, &model.LLMRequest{Model: cfg.Model}, false) {
		if err != nil {
			failure = safeRequirementLiveError(err)
			if strings.HasPrefix(err.Error(), "初筛需求更新") {
				failure = err.Error()
			}
			break
		}
	}
	report := map[string]any{"model": cfg.Model, "calls": counted.calls, "failure": failure, "call_limit": 1, "builder_calls": 0, "embedding_calls": 0}
	raw, _ := json.MarshalIndent(report, "", "  ")
	dir := filepath.Join(root, "artifacts", "screening-diagnostic")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, time.Now().UTC().Format("20060102T150405")+".json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("model=%s calls=%d failure=%s", cfg.Model, len(counted.calls), failure)
	if failure != "" {
		t.Fail()
	}
}
