package planningeval

// BV2-104 冻结 live 输出的零模型回放（等价服务级回归）：以诊断轮记录的 5 次
// 真实 Builder 响应为脚本，在冻结目录快照上重放，验证服务端确定性合同——
// 已有件无精确匹配且 draft 仍占该品类时，无论模型自述措辞或宣称的 outcome
// 是 proposal 还是 ready，都必须以"计价取舍"clarify 收口，不得以占位配置
// 交付 ready（修复前该序列交付 ready，账实不一致）。
import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const bv2104LiveSuite = "testdata/builder-v2-live-20260925/suite.json"
const bv2104FrozenResponses = "testdata/builder-v2-live-20260925/bv2-104-frozen-builder-responses.json"

type bv2104Suite struct {
	Catalog struct {
		Date       string `json:"date"`
		Candidates []struct {
			ID       string          `json:"id"`
			Category string          `json:"category"`
			Brand    string          `json:"brand"`
			Model    string          `json:"model"`
			Specs    json.RawMessage `json:"specs"`
			PriceCNY *json.Number    `json:"price_cny"`
		} `json:"candidates"`
	} `json:"catalog"`
	Cases []struct {
		ID    string `json:"id"`
		Steps []struct {
			Kind   string `json:"kind"`
			Text   string `json:"text"`
			Screen *struct {
				Operations []schemas.RequirementOperation `json:"operations"`
			} `json:"screen_oracle"`
		} `json:"steps"`
	} `json:"cases"`
}

type bv2104Frozen struct {
	Responses []struct {
		Parts []map[string]any `json:"parts"`
		Role  string           `json:"role"`
	} `json:"responses"`
}

// bv2104Catalog 把冻结目录快照适配为 planning.Catalog。
type bv2104Catalog struct{ snapshot store.CatalogSnapshot }

func (c bv2104Catalog) ActiveCatalogSnapshot(context.Context) (store.CatalogSnapshot, error) {
	return c.snapshot, nil
}

// bv2104Model 逐次重放冻结响应。
type bv2104Model struct {
	responses []*genai.Content
	calls     int
}

func (m *bv2104Model) Name() string { return "bv2-104-frozen-live-replay" }

func (m *bv2104Model) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		// 脚本耗尽后重复最终 proposal：修复后采购合计（含替身内存）超出预算，
		// 服务端预算压价回环会追加反馈轮；固执重申同一方案是诚实的模型行为，
		// 最终由 finalize 的计价取舍 clarify 收口。
		idx := m.calls - 1
		if idx >= len(m.responses) {
			idx = len(m.responses) - 1
		}
		yield(&model.LLMResponse{Content: m.responses[idx]}, nil)
	}
}

func bv2104Setup(t *testing.T) (planning.Runner, schemas.PlanningInput) {
	t.Helper()
	raw, err := os.ReadFile(bv2104LiveSuite)
	if err != nil {
		t.Fatal(err)
	}
	var suite bv2104Suite
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	date, err := time.Parse("2006-01-02", suite.Catalog.Date)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: date}}
	for _, c := range suite.Catalog.Candidates {
		// 价格是 NUMERIC(10,2) 十进制文本；快照数字原样转两位小数文本。
		var price *string
		if c.PriceCNY != nil {
			text := c.PriceCNY.String()
			if !strings.Contains(text, ".") {
				text += ".00"
			}
			price = &text
		}
		snapshot.Candidates = append(snapshot.Candidates, store.Candidate{
			SKU: c.ID, Category: schemas.Category(c.Category), Brand: c.Brand, Model: c.Model,
			Specs: c.Specs, PriceCNY: price,
		})
	}
	frozenRaw, err := os.ReadFile(bv2104FrozenResponses)
	if err != nil {
		t.Fatal(err)
	}
	var frozen bv2104Frozen
	if err = json.Unmarshal(frozenRaw, &frozen); err != nil {
		t.Fatal(err)
	}
	modelResponses := make([]*genai.Content, 0, len(frozen.Responses))
	for _, r := range frozen.Responses {
		parts := make([]*genai.Part, 0, len(r.Parts))
		for _, p := range r.Parts {
			rawPart, _ := json.Marshal(p)
			var part genai.Part
			if err := json.Unmarshal(rawPart, &part); err != nil {
				t.Fatal(err)
			}
			parts = append(parts, &part)
		}
		modelResponses = append(modelResponses, &genai.Content{Role: r.Role, Parts: parts})
	}

	var caseData struct {
		Steps []struct {
			Text   string `json:"text"`
			Screen *struct {
				Operations []schemas.RequirementOperation `json:"operations"`
			} `json:"screen_oracle"`
		} `json:"steps"`
	}
	for _, c := range suite.Cases {
		if c.ID == "BV2-104" {
			rawCase, _ := json.Marshal(c)
			if err := json.Unmarshal(rawCase, &caseData); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 种子轮：screening oracle 操作经真实 Reducer 写入需求状态（与 harness
	// 的 scripted 种子轮同口径）。
	state := schemas.NewRequirementState()
	var ops []schemas.RequirementOperation
	for _, st := range caseData.Steps {
		if st.Screen == nil {
			continue
		}
		for _, op := range st.Screen.Operations {
			// 真实 reducer 拒绝了种子里的空 existing_parts（frozen spec 的
			// requirement_observations 留痕）；回放对齐真实冻结载荷。
			if op.Field == "existing_parts" && strings.TrimSpace(string(op.Value)) == "[]" {
				continue
			}
			ops = append(ops, op)
		}
	}
	// 种子轮是核定需求的脚本化适配器输入（非真实用户轮），与 harness 同口径
	// 用 edit 来源写入，不做 quote 归属校验。
	state, err = schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: ops},
		schemas.RequirementSource{Kind: "edit", MessageID: "bv2-104-seed", Quote: caseData.Steps[0].Text})
	if err != nil {
		t.Fatal(err)
	}
	// 冻结的有效选型约束：确认事务冻结的 v2 线上格式（预算 6000 new_purchase
	// + 已有件；弹性系统默认 0.1 展开——与单轮诊断的确认事务一致）。
	var ownedParts []schemas.OwnedPart
	if err := json.Unmarshal(state.Fields["owned_parts"].Value, &ownedParts); err != nil {
		t.Fatal(err)
	}
	// 真实冻结 spec：existing_parts 从 owned_parts 品类推导（确认事务口径）。
	existing := make([]schemas.Category, 0, len(ownedParts))
	for _, p := range ownedParts {
		existing = append(existing, p.Category)
	}
	flex := 0.1
	spec := schemas.RequirementSpec{
		SchemaVersion: 2, BudgetCNY: 6000, BudgetFlex: flex, BudgetBasis: "new_purchase",
		SizePref: "any", NoisePref: "any",
		BrandPref:     schemas.BrandPref{CPU: "any", GPU: "any"},
		UseCase:       schemas.UseCase{Type: "productivity", Titles: []string{"视频剪辑"}},
		OwnedParts:    ownedParts,
		ExistingParts: existing,
		ConstraintStrengths: map[string]string{
			"budget_cny": "must", "budget_basis": "must", "existing_parts": "must",
			"owned_parts": "must", "use_case.type": "must", "use_case.titles": "must",
		},
	}
	specRaw, err := schemas.EncodeRequirementSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	input := schemas.PlanningInput{
		SchemaVersion: 1, State: state, RunID: "bv2-104-frozen-replay",
		EffectiveConstraints: &schemas.EffectiveConstraints{Spec: specRaw},
	}
	runner := planning.Runner{
		Model: &bv2104Model{responses: modelResponses}, Catalog: bv2104Catalog{snapshot: snapshot},
	}
	return runner, input
}

func TestBV2104FrozenLiveReplayMustClarifyOwnershipTradeoff(t *testing.T) {
	runner, input := bv2104Setup(t)
	got, err := runner.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	// 合同 1：不得以占位配置交付正式版本（修复前该序列交付 ready）。
	if got.Outcome == "ready" || got.Outcome == "proposal" {
		t.Fatalf("占位交付未被拦截：outcome=%s reply=%.200s issues=%v", got.Outcome, got.Reply, got.Issues)
	}
	if got.Outcome != "clarify" {
		t.Fatalf("outcome = %s, want clarify", got.Outcome)
	}
	joined := strings.Join(got.Issues, "\n")
	// 合同 2：取舍说明必须点名品类与用户型号，说明计价取舍。
	for _, want := range []string{"内存", "计价取舍", "G.Skill Ripjaws V 32GB (2x16GB) DDR4-3200 CL16"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("clarify issues 缺少 %q：%v", want, got.Issues)
		}
	}
	// 合同 3：报价不再误导——选中替身 SKU 不再被品类核账豁免，采购合计
	// 必须包含内存价格（修复前 5096 元把 1759 元替身排除在外）。
	if got.Quote == nil {
		t.Fatal("quote missing")
	}
	memoryOwned := false
	for _, line := range got.Quote.Lines {
		if strings.Contains(line.SKU, "mem-corsair-lpx-32-3600") && line.Owned {
			memoryOwned = true
		}
	}
	if memoryOwned {
		t.Fatalf("替身内存仍被当作已有件免计价：%+v", got.Quote.Lines)
	}
	// 修复前误导性合计为 5096.00（1759 元替身被品类豁免剔除）；修复后内存
	// 计入采购，即使预算压价求解器做了目录内替换，合计也不应再是 5096。
	if got.Quote.PurchaseTotalCNY != nil && strings.HasPrefix(*got.Quote.PurchaseTotalCNY, "5096") {
		t.Fatalf("采购合计仍是排除替身内存的 5096，账实不一致未修复：%v", *got.Quote.PurchaseTotalCNY)
	}
}
