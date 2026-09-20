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
	for _, n := range fix.notes("预算替换") {
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

// CPU 档位以 TDP 与核显存在性证明：r10/r11 真值里 5600/5500 都是预算内的剪辑
// 方案，故同 TDP、核显形态不丢的更便宜 CPU 属于档位不降；TDP 更低或丢失核显
// （核显点亮场景）不参与确定性替换。
func TestBudgetSolverCPUTierSwap(t *testing.T) {
	base := Candidate{ID: "cpu-r5-5600", Category: schemas.CategoryCPU, Specs: json.RawMessage(`{"tdp_w":65,"socket":"AM4","has_igpu":false}`)}
	igpu := Candidate{ID: "cpu-r5-4600g", Category: schemas.CategoryCPU, Specs: json.RawMessage(`{"tdp_w":65,"socket":"AM4","has_igpu":true}`)}
	if !tierNotLower(schemas.CategoryCPU, base, Candidate{Specs: json.RawMessage(`{"tdp_w":65,"has_igpu":false}`)}) {
		t.Fatal("equal TDP with unchanged iGPU is tier-preserving")
	}
	if tierNotLower(schemas.CategoryCPU, base, Candidate{Specs: json.RawMessage(`{"tdp_w":45,"has_igpu":false}`)}) {
		t.Fatal("lower TDP must not count as tier-preserving")
	}
	if tierNotLower(schemas.CategoryCPU, igpu, Candidate{Specs: json.RawMessage(`{"tdp_w":105,"has_igpu":false}`)}) {
		t.Fatal("losing the integrated GPU breaks iGPU-powered builds")
	}
	if !tierNotLower(schemas.CategoryCPU, base, Candidate{Specs: json.RawMessage(`{"tdp_w":65,"has_igpu":true}`)}) {
		t.Fatal("gaining an integrated GPU is not a downgrade")
	}
	if tierNotLower(schemas.CategoryCPU, base, Candidate{Specs: json.RawMessage(`{"socket":"AM4"}`)}) {
		t.Fatal("missing tier fields stay incomparable")
	}

	x, draft := solverRecording(t, "4430") // 超支 149.90，夹具 CPU 629.00 → 同档 470.00 可省 159
	x.candidates = append(x.candidates, Candidate{
		ID: "cpu-solver", Category: schemas.CategoryCPU, Brand: "AMD", Model: "Ryzen 5 5600",
		Specs: byIDFallback(x.candidates, "cpu-r5-5600").Specs, Price: strPtr("470.00"),
	})
	fix, sacrifice, ok := x.solveBudget(context.Background(), draft)
	if !ok || fix == nil || sacrifice != nil {
		t.Fatalf("tier-equal cpu swap must be an executable fix: %+v %+v %v", fix, sacrifice, ok)
	}
	if len(fix.Replacements) != 1 || fix.Replacements[0].ToID != "cpu-solver" || fix.Replacements[0].SavingCNY != "159.00" {
		t.Fatalf("wrong replacement: %+v", fix.Replacements)
	}
	if !strings.Contains(fix.Replacements[0].Basis, "TDP 65W→65W") || !strings.Contains(fix.Replacements[0].Basis, "核显 false→false") {
		t.Fatalf("basis must prove the CPU tier argument: %q", fix.Replacements[0].Basis)
	}

	// 只有降 TDP 的更便宜 CPU 时不得替换，也不得静默降级为牺牲之外的交付。
	x2, draft2 := solverRecording(t, "4430")
	x2.candidates = append(x2.candidates, Candidate{
		ID: "cpu-45w", Category: schemas.CategoryCPU, Brand: "AMD", Model: "Ryzen 5 4500",
		Specs: json.RawMessage(`{"tdp_w":45,"socket":"AM4","has_igpu":false,"supported_chipsets":["A520","B450","B550","X470","X570"]}`),
		Price: strPtr("436.00"),
	})
	fix, _, ok = x2.solveBudget(context.Background(), draft2)
	if !ok || fix != nil {
		t.Fatalf("lower-TDP cpu must not be selected as a replacement: %+v %v", fix, ok)
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
	totalExhausted := &deliveryGateCounters{budget: 1, total: 3}
	if !x.budgetFixDue("ready", false, totalExhausted, 0, 8) {
		t.Fatal("gate quota consumed by other gates must still arm the solver")
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

// unknown 换选：缺字段候选在门耗尽后被字段完整候选确定性替换，重新通过校验。
func TestUnknownFixReplacesIncompleteCandidate(t *testing.T) {
	x, _ := solverRecording(t, "6000")
	x.result.Draft = json.RawMessage(strings.Replace(string(x.result.Draft), "cooler-deepcool-ag400", "cooler-hyper-blank", 1))
	x.candidates = append(x.candidates, Candidate{
		ID: "cooler-hyper-blank", Category: schemas.CategoryCooler, Brand: "Test", Model: "Hyper Blank",
		Specs: json.RawMessage(`{"type":"air","height_mm":150,"radiator_size_mm":null}`), Price: strPtr("54.50"),
	})
	x.evaluate(context.Background(), x.result.Draft)
	if x.result.Validation == nil || x.result.Validation.OverallStatus == schemas.OverallPass {
		t.Fatalf("incomplete candidate must fail validation first: %+v", x.result.Validation)
	}
	gates := &deliveryGateCounters{unknown: 2}
	if !x.unknownFixDue("proposal", false, gates, 0, 8) {
		t.Fatal("exhausted unknown gate must arm the fix")
	}
	x.applyUnknownFix(context.Background())
	if x.result.Validation == nil || x.result.Validation.OverallStatus != schemas.OverallPass {
		t.Fatalf("fix must restore full validation: %+v", x.result.Validation)
	}
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil || draft.Selection.Cooler != "cooler-deepcool-ag400" {
		t.Fatalf("cooler must be replaced with the field-complete candidate: %s", draft.Selection.Cooler)
	}
	joined := strings.Join(x.result.Issues, "\n")
	if !strings.Contains(joined, "unknown 修复换选") || !strings.Contains(joined, "字段补全") {
		t.Fatalf("fix must be recorded with basis: %q", joined)
	}
	if strings.Contains(joined, "字段缺失") {
		t.Fatalf("stale missing-field issue must be dropped: %q", joined)
	}
}

// must 静音锁定 cooler：unknown 无换选空间时如实保留，不得强行替换。
func TestUnknownFixRespectsLocks(t *testing.T) {
	x, _ := solverRecording(t, "6000")
	x.result.Draft = json.RawMessage(strings.Replace(string(x.result.Draft), "cooler-deepcool-ag400", "cooler-hyper-blank", 1))
	x.candidates = append(x.candidates, Candidate{
		ID: "cooler-hyper-blank", Category: schemas.CategoryCooler, Brand: "Test", Model: "Hyper Blank",
		Specs: json.RawMessage(`{"type":"air","height_mm":150}`), Price: strPtr("54.50"),
	})
	mustField(t, &x.input, "noise_pref", `"silent"`, "must", "constraint")
	x.evaluate(context.Background(), x.result.Draft)
	x.applyUnknownFix(context.Background())
	if x.result.Validation == nil || x.result.Validation.OverallStatus == schemas.OverallPass {
		t.Fatal("locked cooler must keep its unknown state")
	}
}

// unknown 门未耗尽时不触发修复。
func TestUnknownFixDueGating(t *testing.T) {
	x, _ := solverRecording(t, "6000")
	x.result.Draft = json.RawMessage(strings.Replace(string(x.result.Draft), "cooler-deepcool-ag400", "cooler-hyper-blank", 1))
	x.candidates = append(x.candidates, Candidate{
		ID: "cooler-hyper-blank", Category: schemas.CategoryCooler, Brand: "Test", Model: "Hyper Blank",
		Specs: json.RawMessage(`{"type":"air","height_mm":150}`), Price: strPtr("54.50"),
	})
	x.evaluate(context.Background(), x.result.Draft)
	gates := &deliveryGateCounters{}
	if x.unknownFixDue("proposal", false, gates, 0, 8) {
		t.Fatal("unexhausted unknown gate must not arm the fix")
	}
	if x.unknownFixDue("collect", false, gates, 6, 8) {
		t.Fatal("collect is not a delivery outcome")
	}
}

// 牺牲候选与用途硬条件冲突时被过滤：以无核显 CPU 替代核显点亮不是可执行牺牲。
func TestMinSacrificeFiltersDisplayConflict(t *testing.T) {
	x, _ := solverRecording(t, "2700") // 去独显后总价 2780.90，超支 80.90
	// 去掉独显、换带核显 CPU：无核显候选作为牺牲将触发 DISPLAY_OUTPUT 失败。
	x.result.Draft = json.RawMessage(strings.Replace(string(x.result.Draft), `"gpu-sapphire-6600-pulse"`, `null`, 1))
	x.result.Draft = json.RawMessage(strings.Replace(string(x.result.Draft), "cpu-r5-5600", "cpu-igpu", 1))
	x.candidates = append(x.candidates,
		Candidate{
			ID: "cpu-igpu", Category: schemas.CategoryCPU, Brand: "Test", Model: "Ryzen igpu",
			Specs: json.RawMessage(`{"has_igpu":true,"socket":"AM4","tdp_w":65,"supported_chipsets":["B550"]}`), Price: strPtr("629.00"),
		},
		Candidate{
			ID: "cpu-noigpu-cheap", Category: schemas.CategoryCPU, Brand: "Test", Model: "Ryzen no-igpu",
			Specs: json.RawMessage(`{"has_igpu":false,"socket":"AM4","tdp_w":65,"supported_chipsets":["B550"]}`), Price: strPtr("429.00"),
		},
	)
	x.evaluate(context.Background(), x.result.Draft)
	current, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil {
		t.Fatal(err)
	}
	if current.Selection.GPU != nil {
		t.Fatal("gpu must be removed for this scenario")
	}
	fix, sacrifice, ok := x.solveBudget(context.Background(), current)
	if !ok || fix != nil {
		t.Fatalf("no tier-preserving fix expected: %+v %v", fix, ok)
	}
	if sacrifice != nil && sacrifice.ToID == "cpu-noigpu-cheap" {
		t.Fatalf("display-output-conflicting sacrifice must be filtered: %+v", sacrifice)
	}
}

// 占位交付强转 clarify：已有件无精确匹配且模型自述替身时，proposal 不是可交付形态。
func TestPlaceholderDeliveryForcedToClarify(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"memory","model":"G.Skill Ripjaws V 32GB DDR4-3200","quantity":1}]`),
	}
	record.Outcome = "proposal"
	record.Reply = "目录未精确匹配用户已有的 G.Skill Ripjaws V 32GB，当前以同系列候选作为核验替身；实际装机沿用用户已有内存，服务端按品类核账、不计入采购合计。如用户希望改购新内存需另行确认。"
	record.Issues = []string{"已有件无精确匹配，以替身占位"}
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "clarify" {
		t.Fatalf("placeholder delivery must end as clarify: %+v", got)
	}
	if !strings.Contains(strings.Join(got.Issues, "\n"), "计价取舍") {
		t.Fatalf("forced clarify must record the tradeoff: %q", got.Issues)
	}
}

// 升级改单交付（无替身自述）不受占位强转影响。
func TestPlaceholderDeliveryKeepsUpgradeDelivery(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"memory","model":"G.Skill Ripjaws V 32GB (2x16GB) DDR4-3200 CL16","quantity":1}]`),
	}
	record.Outcome = "proposal"
	record.Reply = "已按预算完成选配，显卡品牌符合要求。"
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil || got.Outcome != "ready" {
		t.Fatalf("normal delivery must not be forced to clarify: %+v %v", got, err)
	}
}

// 本轮刚被改动的槽位（draft 与 base_draft 同品类不同 SKU）必须冻结：
// 压价回退等于撤销用户/改单的方向性决定（C123-001 教训：刚指定的 5700X
// 升级被压价换回 5600，触发 changed_cpu 与 final_outcome 双失败）。
func TestBudgetSolverFreezesJustChangedSlots(t *testing.T) {
	x, draft := solverRecording(t, "4430")
	x.candidates = append(x.candidates, Candidate{
		ID: "cpu-solver", Category: schemas.CategoryCPU, Brand: "AMD", Model: "Ryzen 5 5600",
		Specs: byIDFallback(x.candidates, "cpu-r5-5600").Specs, Price: strPtr("470.00"),
	})
	// patchDraftJSON 产出 wire 格式（BuildDraft 结构体 marshal 是 Go 字段名，
	// 无法过 decodeStrict）。
	raw, err := patchDraftJSON(x.result.Draft, &budgetFix{Replacements: []budgetReplacement{{
		Category: schemas.CategoryCPU, SSDIndex: -1, ToID: "cpu-r7-5700x",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	x.input.BaseDraft = raw
	fix, sacrifice, ok := x.solveBudget(context.Background(), draft)
	if !ok || fix != nil || sacrifice != nil {
		t.Fatalf("just-changed cpu slot must stay locked: %+v %+v %v", fix, sacrifice, ok)
	}

	// base_draft 与当前 draft 一致时不冻结：同档替换照常成立。
	same, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	x2, draft2 := solverRecording(t, "4430")
	x2.candidates = append(x2.candidates, Candidate{
		ID: "cpu-solver", Category: schemas.CategoryCPU, Brand: "AMD", Model: "Ryzen 5 5600",
		Specs: byIDFallback(x2.candidates, "cpu-r5-5600").Specs, Price: strPtr("470.00"),
	})
	x2.input.BaseDraft = same
	fix, _, ok = x2.solveBudget(context.Background(), draft2)
	if !ok || fix == nil || len(fix.Replacements) != 1 || fix.Replacements[0].ToID != "cpu-solver" {
		t.Fatalf("unchanged slot must stay swappable: %+v %v", fix, ok)
	}
}
