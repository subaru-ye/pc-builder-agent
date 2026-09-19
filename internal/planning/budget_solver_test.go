package planning

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func mustField(t *testing.T, input *schemas.PlanningInput, key, value, strength, kind string) {
	t.Helper()
	input.State.Fields[key] = schemas.RequirementField{
		Status: "active", Kind: kind, Strength: strength, Value: json.RawMessage(value),
	}
}

// solverRecording 在夹具上构造一个已 evaluate 的超预算 execution。
func solverRecording(t *testing.T, budget string) (*execution, schemas.BuildDraft) {
	t.Helper()
	input, record, catalog := completeRecording(t)
	mustBudget(t, &input, budget, "must")
	x := gateExecution(input, record, catalog)
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil {
		t.Fatal(err)
	}
	return x, draft
}

func TestBudgetSolverFindsTierPreservingReplacement(t *testing.T) {
	x, draft := solverRecording(t, "4430") // 超支 149.90，psu 同档替换可省 159
	x.candidates = append(x.candidates, Candidate{
		ID: "psu-solver", Category: schemas.CategoryPSU, Brand: "Test", Model: "Quiet 650W",
		Specs: byIDFallback(x.candidates, "psu-msi-mag-a650bn").Specs, Price: strPtr("100.00"),
	})
	fix, sacrifice, ok := x.solveBudget(context.Background(), draft)
	if !ok || fix == nil || sacrifice != nil {
		t.Fatalf("tier-preserving fix expected: %+v %+v %v", fix, sacrifice, ok)
	}
	if len(fix.Replacements) != 1 || fix.Replacements[0].ToID != "psu-solver" ||
		fix.Replacements[0].FromID != "psu-msi-mag-a650bn" || fix.Replacements[0].SavingCNY != "159.00" {
		t.Fatalf("wrong replacement: %+v", fix.Replacements)
	}
	if !strings.Contains(fix.Replacements[0].Basis, "650W→650W") {
		t.Fatalf("basis must prove tier preserved: %q", fix.Replacements[0].Basis)
	}
	if fix.TotalCNY != "4420.90" {
		t.Fatalf("fixed total must stay under ceiling: %s", fix.TotalCNY)
	}
	for _, n := range fix.notes() {
		if !strings.Contains(n, "服务端预算替换") || !strings.Contains(n, "依据") {
			t.Fatalf("note must record what/why/basis: %q", n)
		}
	}
}

// 档位守卫：功率更低或缺档位字段的更便宜候选不可作为替换（牺牲路径除外）。
func TestBudgetSolverRejectsTierDowngrades(t *testing.T) {
	x, draft := solverRecording(t, "4430")
	x.candidates = append(x.candidates,
		Candidate{ID: "psu-low", Category: schemas.CategoryPSU, Specs: json.RawMessage(`{"wattage_w":600}`), Price: strPtr("100.00")},
		Candidate{ID: "psu-blank", Category: schemas.CategoryPSU, Specs: json.RawMessage(`{}`), Price: strPtr("90.00")},
	)
	fix, _, ok := x.solveBudget(context.Background(), draft)
	if !ok || fix != nil {
		t.Fatalf("tier downgrades must not be selected as replacements: %+v %v", fix, ok)
	}
}

// 锁定保护：已有件品类、must 静音、更小容量的内存都不允许作为替换。
func TestBudgetSolverProtectsLockedSlots(t *testing.T) {
	x, draft := solverRecording(t, "4430")
	psu := byIDFallback(x.candidates, "psu-msi-mag-a650bn")
	x.candidates = append(x.candidates,
		Candidate{ID: "psu-solver", Category: schemas.CategoryPSU, Specs: psu.Specs, Price: strPtr("100.00")},
	)
	// 用户已有该电源：psu 品类锁定，既不替换也不作为牺牲。
	input := x.input
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"psu","model":"MAG A650BN 650W 80+ Bronze","quantity":1}]`),
	}
	x.input = input
	fix, sacrifice, ok := x.solveBudget(context.Background(), draft)
	if !ok || fix != nil || sacrifice != nil {
		t.Fatalf("owned-category slot must stay locked: %+v %+v %v", fix, sacrifice, ok)
	}

	// 内存 16GB 更便宜但容量降档：不参与替换，替换应落在合格的电源上。
	x2, draft2 := solverRecording(t, "4430")
	x2.candidates = append(x2.candidates,
		Candidate{ID: "mem-small", Category: schemas.CategoryMemory, Specs: json.RawMessage(`{"generation":"ddr4","speed_mts":3200}`), Price: strPtr("199.00")},
		Candidate{ID: "psu-solver", Category: schemas.CategoryPSU, Specs: psu.Specs, Price: strPtr("100.00")},
	)
	fix, _, ok = x2.solveBudget(context.Background(), draft2)
	if !ok || fix == nil {
		t.Fatalf("tier-preserving psu fix must exist: %+v %v", fix, ok)
	}
	for _, r := range fix.Replacements {
		if r.Category == schemas.CategoryMemory {
			t.Fatalf("capacity downgrade must not be selected: %+v", fix.Replacements)
		}
	}

	// must 静音：电源/机箱/散热锁定。
	x3, draft3 := solverRecording(t, "4430")
	mustField(t, &x3.input, "noise_pref", `"silent"`, "must", "constraint")
	x3.candidates = append(x3.candidates, Candidate{ID: "psu-solver", Category: schemas.CategoryPSU, Specs: psu.Specs, Price: strPtr("100.00")})
	fix, _, ok = x3.solveBudget(context.Background(), draft3)
	if !ok || fix != nil {
		t.Fatalf("must-silent slots must stay locked: %+v %v", fix, ok)
	}
}

// 无可行组合时交付最小牺牲标注：这里内存降到 16GB 是唯一能覆盖超支的牺牲。
func TestBudgetSolverMinSacrifice(t *testing.T) {
	x, draft := solverRecording(t, "4430")
	x.candidates = append(x.candidates, Candidate{
		ID: "mem-small", Category: schemas.CategoryMemory,
		Specs: json.RawMessage(`{"generation":"ddr4","speed_mts":3200}`), Price: strPtr("199.00"),
	})
	fix, sacrifice, ok := x.solveBudget(context.Background(), draft)
	if !ok || fix != nil || sacrifice == nil {
		t.Fatalf("memory downgrade is the only covering sacrifice: %+v %+v %v", fix, sacrifice, ok)
	}
	if sacrifice.Category != schemas.CategoryMemory || sacrifice.ToID != "mem-small" || sacrifice.SavingCNY != "200.00" {
		t.Fatalf("wrong sacrifice: %+v", sacrifice)
	}
	if issue := sacrifice.issue(); !strings.Contains(issue, "最小牺牲") || !strings.Contains(issue, "内存") || !strings.Contains(issue, "需用户确认") {
		t.Fatalf("sacrifice issue must name the trade: %q", issue)
	}

	// 省额不足以覆盖超支时不构成牺牲方案。
	x2, _ := solverRecording(t, "1000")
	fix, sacrifice, ok = x2.solveBudget(context.Background(), draft)
	if !ok || fix != nil || sacrifice != nil {
		t.Fatalf("insufficient savings must yield no sacrifice: %+v %+v %v", fix, sacrifice, ok)
	}
	x2.applyBudgetFix(context.Background())
	if x2.result.Outcome != "proposal" || !strings.Contains(strings.Join(x2.result.Issues, "\n"), "预算压价求解") {
		t.Fatalf("unfixable overrun must end as annotated proposal: %+v", x2.result)
	}
}

// 触发门：终局超预算 + 回环耗尽/已核验低价才出手；free.* must 约束停用。
func TestBudgetFixDueGating(t *testing.T) {
	input, record, catalog := completeRecording(t)
	mustBudget(t, &input, "1000", "must")
	x := gateExecution(input, record, catalog)
	x.priceAsc = map[string]bool{}
	gates := &deliveryGateCounters{}
	if x.budgetFixDue("proposal", false, gates, 0, 8) {
		t.Fatal("unexhausted loop without price evidence must not trigger")
	}
	x.priceAsc["psu"] = true
	if !x.budgetFixDue("proposal", false, gates, 0, 8) {
		t.Fatal("verified low-price evidence must arm the solver")
	}
	x.priceAsc = nil
	if !x.budgetFixDue("clarify", true, gates, 6, 8) {
		t.Fatal("final turns must arm the solver for over-budget clarify")
	}
	if x.budgetFixDue("clarify", false, gates, 6, 8) {
		t.Fatal("clarify without an evaluated draft is not a terminal delivery")
	}
	exhausted := &deliveryGateCounters{budget: 2}
	if !x.budgetFixDue("ready", false, exhausted, 0, 8) {
		t.Fatal("exhausted budget gate must arm the solver")
	}
	if x.budgetFixDue("collect", false, exhausted, 6, 8) {
		t.Fatal("collect is not a delivery outcome")
	}
	mustBudget(t, &input, "6000", "must")
	x3 := gateExecution(input, record, catalog)
	if x3.budgetFixDue("proposal", false, exhausted, 6, 8) {
		t.Fatal("within-budget delivery must not trigger the solver")
	}

	// free.* must 约束无法程序映射到品类，求解器保守停用。
	freeInput, freeRecord, _ := completeRecording(t)
	mustBudget(t, &freeInput, "1000", "must")
	mustField(t, &freeInput, "free.extra", `"更贵也行"`, "must", "constraint")
	x2 := gateExecution(freeInput, freeRecord, catalog)
	if x2.budgetFixDue("proposal", false, exhausted, 6, 8) {
		t.Fatal("free-form must constraint must disable the solver")
	}
}

// Runner 级：模型回环耗尽后仍以超预算终局交付，服务端在回放内完成替换并交付。
func TestBudgetSolverReplacesInRunnerLoop(t *testing.T) {
	input, record, catalog := completeRecording(t)
	mustBudget(t, &input, "4430", "must")
	psu := catalogCandidates(catalog)
	var base Candidate
	for _, c := range psu {
		if c.ID == "psu-msi-mag-a650bn" {
			base = c
		}
	}
	catalog.Candidates = append(catalog.Candidates, store.Candidate{
		SKU: "psu-solver", Category: schemas.CategoryPSU, Brand: "Test", Model: "Quiet 650W",
		Specs: base.Specs, PriceCNY: strPtr("100.00"),
	})
	record.Outcome = "proposal"
	record.Issues = []string{"新增采购总价4579.9元超出4430元预算硬上限149.9元，需用户确认接受超预算或授权调整"}
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 8}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "proposal" || got.ModelOutcome != "proposal" {
		t.Fatalf("solver must deliver annotated proposal: %+v", got)
	}
	draft, err := schemas.DecodeBuildDraft(got.Draft)
	if err != nil || draft.Selection.PSU != "psu-solver" {
		t.Fatalf("psu must be replaced server-side: %s %v", draft.Selection.PSU, err)
	}
	if got.Quote == nil || got.Quote.TotalCNY != "4420.90" {
		t.Fatalf("quote must reflect the replacement: %+v", got.Quote)
	}
	joined := strings.Join(got.Issues, "\n")
	if !strings.Contains(joined, "服务端预算替换") || !strings.Contains(joined, "650W→650W") {
		t.Fatalf("replacement must be recorded with basis: %q", joined)
	}
	if strings.Contains(joined, "超出") {
		t.Fatalf("stale over-budget issue must be dropped: %q", joined)
	}
	if got.ModelCalls != 3 {
		t.Fatalf("solver must not add model calls (gate loop ×2 + terminal): %d", got.ModelCalls)
	}
	if got.Validation == nil || got.Validation.OverallStatus != schemas.OverallPass {
		t.Fatalf("replacement must pass full validation: %+v", got.Validation)
	}
}

// 求解器反向核验 authored 真值（零模型调用）：对冻结目录判定
// C123-003 step2 的 sub-6000 组合存在性与 B2-002 step1 的预算可行性。
func TestBudgetSolverTruthAuditAgainstFrozenCatalog(t *testing.T) {
	raw, err := os.ReadFile("../planningeval/testdata/current-178-live-20260918/suite.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Catalog struct {
			Date       string      `json:"date"`
			Candidates []Candidate `json:"candidates"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	newExec := func(owned string) *execution {
		input := schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()}
		mustField(t, &input, "budget_cny", "6000", "must", "constraint")
		mustField(t, &input, "budget_basis", `"new_purchase"`, "must", "constraint")
		mustField(t, &input, "owned_parts", owned, "must", "fact")
		return &execution{input: input, candidates: suite.Catalog.Candidates, date: suite.Catalog.Date, snapshotID: 1}
	}

	// C123-003 step2（r9 实测 draft）：保已有 5600，其余新购 6561.5 > 6000。
	// 求解器结论：档位不降约束下无可行替换（各未锁定品类均已目录最低价），
	// 最小牺牲是内存 32GB→16GB（省 1190 覆盖超支 561.5）。
	x := newExec(`[{"category":"cpu","model":"AMD Ryzen 5 5600","quantity":1}]`)
	c123 := `{"schema_version":1,"requirement_ref":"current","build_ref":"proposal","selection":{"cpu":"cpu-r5-5600","gpu":"gpu-msi-3060-ventus2x","motherboard":"mb-msi-b550m-pro-vdh-wifi","memory":"mem-corsair-lpx-32-3600","ssd":[{"sku":"ssd-zhitai-ti600-1tb","quantity":1}],"psu":"psu-msi-mag-a650bn","case":"case-asus-prime-ap201","cooler":"cooler-coolermaster-hyper212-black"}}`
	x.evaluate(context.Background(), json.RawMessage(c123))
	draft, err := schemas.DecodeBuildDraft(json.RawMessage(c123))
	if err != nil {
		t.Fatal(err)
	}
	fix, sacrifice, ok := x.solveBudget(context.Background(), draft)
	if !ok || fix != nil {
		t.Fatalf("C123-003 step2 must have no tier-preserving fix: %+v %v", fix, ok)
	}
	if sacrifice == nil || sacrifice.Category != schemas.CategoryMemory || sacrifice.ToID != "mem-corsair-lpx-16-3200" || sacrifice.SavingCNY != "610.00" {
		t.Fatalf("cheapest covering memory downgrade must be the minimal sacrifice: %+v", sacrifice)
	}

	// B2-002 step1：r9 交付卡在 DISPLAY_OUTPUT（无独显且 5700X3D 无核显）。
	// 加目录最低独显 3060（2789）后校验通过、新增采购 4667 ≤ 6000——预算
	// 不是必须用户取舍的问题，authored 期望据此从 clarify 定版为标注 proposal。
	x2 := newExec(`[{"category":"cpu","model":"5600","quantity":1},{"category":"motherboard","model":"B550M","quantity":1}]`)
	b2 := `{"schema_version":1,"requirement_ref":"current","build_ref":"proposal","selection":{"cpu":"cpu-r7-5700x3d","gpu":"gpu-msi-3060-ventus2x","motherboard":"mb-msi-b550m-pro-vdh-wifi","memory":"mem-crucial-ballistix-16-3200","ssd":[{"sku":"ssd-crucial-bx500-1tb","quantity":1}],"psu":"psu-msi-mag-a650bn","case":"case-asus-prime-ap201","cooler":"cooler-deepcool-ag400"}}`
	x2.evaluate(context.Background(), json.RawMessage(b2))
	if x2.result.Validation == nil || x2.result.Validation.OverallStatus != schemas.OverallPass {
		t.Fatalf("b2-002 candidate build must fully pass: %+v", x2.result.Validation)
	}
	amount := quoteAmount(*x2.result.Quote, "new_purchase")
	if amount == nil || amount.Cmp(new(big.Rat).SetInt64(6000)) > 0 || amount.FloatString(2) != "4667.00" {
		t.Fatalf("b2-002 purchase total must stay within budget: %v", amount)
	}
}
