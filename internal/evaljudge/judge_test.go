package evaljudge

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func testInput() Input {
	return Input{CaseID: "test", SourceRepeat: 1, Texts: map[string]string{"message": "当前目录资料不足，请补充准确型号。"}, Facts: map[string]string{"decision": "owned_model_unresolved"}}
}
func testJudgement() Judgement {
	j := Judgement{Ratings: map[string]Rating{}}
	for _, d := range Dimensions {
		j.Ratings[d] = Rating{Score: 2, Reason: "说明资料限制并请求必要型号", Evidence: []Evidence{{OutputKey: "message", OutputQuote: "当前目录资料不足", FactKey: "decision", FactQuote: "owned_model_unresolved"}}}
	}
	return j
}

func TestJudgeRejectsFabricatedEvidenceAndIncompleteScores(t *testing.T) {
	in := testInput()
	raw := marshal(testJudgement())
	if _, err := Decode(raw, in); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.ReplaceAll(raw, "当前目录资料不足", "全市场无解"),
		strings.ReplaceAll(raw, "owned_model_unresolved", "catalog_infeasible"),
		strings.ReplaceAll(raw, `"score":2`, `"score":3`),
		strings.ReplaceAll(raw, `"score":2,`, ``),
		strings.ReplaceAll(raw, `"score":2`, `"score":null`),
		raw + ` {"second":"object"}`, "```json\n" + raw + "\n```",
	} {
		if _, err := Decode(bad, in); err == nil {
			t.Fatalf("invalid rating accepted: %s", bad)
		}
	}
}

type judgeFake struct {
	calls  int
	raw    string
	prompt string
	mime   string
	schema any
}

func (*judgeFake) Name() string { return "judge-fake" }
func (m *judgeFake) GenerateContent(_ context.Context, r *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		m.prompt = r.Contents[0].Parts[0].Text
		m.mime = r.Config.ResponseMIMEType
		m.schema = r.Config.ResponseJsonSchema
		yield(&model.LLMResponse{Content: genai.NewContentFromText(m.raw, genai.RoleModel), UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 30, CandidatesTokenCount: 10, TotalTokenCount: 40}}, nil)
	}
}
func TestJudgePreservesAbsenceInvalidRawAndReplaysEvidence(t *testing.T) {
	llm := &judgeFake{raw: marshal(testJudgement())}
	in := testInput()
	r := Evaluate(context.Background(), llm, "rules", in, 1)
	if r.Status != "scored" || r.Usage.TotalTokens != 40 || llm.calls != 1 || !strings.Contains(llm.prompt, in.Texts["message"]) || llm.mime != "application/json" || llm.schema == nil {
		t.Fatalf("record=%+v", r)
	}
	if err := VerifyRecords([]Input{in}, []Record{r}, 1); err != nil {
		t.Fatal(err)
	}
	r.Judgement.Ratings["grounding"] = Rating{Score: 0}
	if err := VerifyRecords([]Input{in}, []Record{r}, 1); err == nil {
		t.Fatal("changed saved grade accepted")
	}
	llm.raw = "无法评分"
	r = Evaluate(context.Background(), llm, "rules", in, 1)
	if r.Status != "invalid_judgement" || r.Raw != "无法评分" || r.Judgement != nil {
		t.Fatalf("invalid judgement lost: %+v", r)
	}
	if err := VerifyRecords([]Input{in}, []Record{r}, 1); err != nil {
		t.Fatal(err)
	}
	before := llm.calls
	in.Texts = map[string]string{}
	r = Evaluate(context.Background(), llm, "rules", in, 1)
	if r.Status != "missing_explanation" || llm.calls != before || r.Usage.ModelCalls != 0 {
		t.Fatal("empty explanation was fabricated or model-called")
	}
	if err := VerifyRecords([]Input{in}, []Record{r}, 1); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecords([]Input{in}, nil, 1); err == nil {
		t.Fatal("missing repetition accepted")
	}
	if _, err := ReadRecords([]byte(marshal(r) + "\n}")); err == nil {
		t.Fatal("trailing corrupt record ignored")
	}
}

func TestJudgePresentsOriginalFactStringsWithoutDoubleEncoding(t *testing.T) {
	in := testInput()
	in.Facts["requirement"] = `{"noise_pref":"silent","notes":"安静办公"}`
	prompt := renderInput(in)
	if !strings.Contains(prompt, in.Facts["requirement"]) || !strings.Contains(prompt, in.Texts["message"]) {
		t.Fatal("original fact or explanation changed in prompt")
	}
	if prompt != renderInput(in) {
		t.Fatal("unstable evidence ordering")
	}
}

func TestPrepareKeepsRealTextAndSeparatesIndependentFacts(t *testing.T) {
	rows := []evalsuite.CaseRecord{
		{CaseID: "empty", Stage: evalsuite.StageBuild, Seed: 1, Requirement: json.RawMessage(`{"budget_cny":6000}`), Snapshot: evalsuite.SnapshotView{Catalog: &store.CatalogSnapshot{}}, Result: &buildharness.BuildResult{Succeeded: true, Draft: schemas.BuildDraft{}}},
		{CaseID: "decline", Stage: evalsuite.StageBuild, Seed: 1, Requirement: json.RawMessage(`{"budget_cny":6000}`), Result: &buildharness.BuildResult{Message: "请补型号", Decision: &buildharness.Decision{Kind: "clarify", Message: "请补型号"}}},
	}
	selection := Selection{SchemaVersion: 1, CaseIDs: []string{"empty", "decline"}, SourceRepeat: 1, Provenance: "test synthetic"}
	inputs, err := Prepare(rows, selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs[0].Texts) != 0 || inputs[1].Texts["message"] != "请补型号" || strings.Contains(inputs[1].Facts["decision"], "请补型号") {
		t.Fatalf("text/facts changed: %+v", inputs)
	}
	selection.CaseIDs = []string{"empty", "missing"}
	if _, err := Prepare(rows, selection); err == nil {
		t.Fatal("missing source case silently dropped")
	}
}

func TestJudgeFamilyRequiresFixedDifferentFamily(t *testing.T) {
	source := evalsuite.ReportMeta{Models: map[string]map[string]any{"builder": {"model": "qwen3.8-max-0902"}}}
	if err := CheckJudgeFamily(source, "deepseek-v4-flash-0731"); err != nil {
		t.Fatal(err)
	}
	if CheckJudgeFamily(source, "qwen-other") == nil || CheckJudgeFamily(source, "unknown-model") == nil {
		t.Fatal("unknown or same family accepted")
	}
	source.Models["builder"]["model_chain"] = []any{"qwen3.8-max-0902", "deepseek-v4-flash-0731"}
	if CheckJudgeFamily(source, "deepseek-v4-flash-0731") == nil {
		t.Fatal("mixed-family source chain accepted")
	}
}
