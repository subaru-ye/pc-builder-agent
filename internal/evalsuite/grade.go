package evalsuite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const CurrentGraderVersion = "independent-v2"

func ValidateGraderVersion(version string) error {
	if version != "" && version != CurrentGraderVersion {
		return fmt.Errorf("不支持的判卷版本 %q", version)
	}
	return nil
}

// GradeBuild 按显式版本判卷。空版本保留历史口径；新版本从证据独立重建交付事实。
// 重新评分不能覆盖旧记录，调用方须同时保留原成绩与新成绩。
func GradeBuild(c Case, r CaseRecord, version string) (Verdict, error) {
	if err := ValidateGraderVersion(version); err != nil {
		return Verdict{}, err
	}
	if r.Result == nil {
		return Verdict{}, fmt.Errorf("%s 缺少构建结果", c.ID)
	}
	v := AssertCase(c, *r.Result, r.Snapshot)
	if version == "" || !r.Result.Succeeded {
		return v, nil
	}
	add := func(id, name, detail string, veto bool) {
		v.Passed = false
		v.Failures = append(v.Failures, AssertionFailure{ID: id, Name: name, Detail: detail, Veto: veto})
	}
	if r.Candidates == nil || r.Snapshot.Catalog == nil {
		add("E1", "评估证据不完整", "独立判卷需要本次实际候选包和完整冻结目录", false)
		return v, nil
	}
	if r.Candidates.SnapshotDate != r.Snapshot.SnapshotDate || r.Snapshot.Catalog.Snapshot.SnapshotDate.Format("2006-01-02") != r.Snapshot.SnapshotDate {
		add("E1", "评估证据不完整", "候选包或完整目录的快照日期不一致", false)
		return v, nil
	}
	allowed := map[schemas.Category]map[string]bool{}
	for _, g := range r.Candidates.Groups {
		if allowed[g.Category] == nil {
			allowed[g.Category] = map[string]bool{}
		}
		for _, candidate := range g.Candidates {
			allowed[g.Category][candidate.SKU] = true
		}
	}
	sel := r.Result.Draft.Selection
	for _, category := range schemas.AllCategories {
		for _, sku := range categorySKUs(sel, category) {
			if !allowed[category][sku] {
				add("A6", "本轮候选溯源", fmt.Sprintf("%s 的 SKU %q 不在本次实际提供的对应品类候选内", category, sku), true)
			}
		}
	}
	if r.Result.Result.Report.OverallStatus == schemas.OverallFail {
		add("A10", "交付兼容性独立复验", "Succeeded=true 但保存的兼容报告为 fail", true)
	}
	resolver, err := newFrozenResolver(*r.Snapshot.Catalog)
	if err != nil {
		add("E1", "评估证据不完整", err.Error(), false)
		return v, nil
	}
	actual, err := validate.New(resolver).Evaluate(context.Background(), sel)
	if err != nil {
		add("A10", "交付兼容性独立复验", "不能从冻结目录解析交付配置："+err.Error(), true)
		return v, nil
	}
	if actual.Report.OverallStatus == schemas.OverallFail {
		for _, check := range actual.Report.Checks {
			if check.Outcome == schemas.OutcomeFail {
				add("A10", "交付兼容性独立复验", string(check.RuleID)+"："+check.Detail, true)
			}
		}
	}
	if !sameJSON(actual.Report, r.Result.Result.Report) {
		add("A10", "兼容报告与事实不符", "保存的规则结果与冻结目录独立重算不一致", true)
	}
	actual.Quote = validate.WithOwnership(actual.Quote, c.Requirement)
	if !sameJSON(actual.Quote, r.Result.Result.Quote) {
		add("A7", "报价与实际选件不符", "保存的分项、单价、数量、已有件标记或合计与冻结目录重算不一致", false)
	}
	return v, nil
}

func sameJSON(a, b any) bool {
	x, ex := json.Marshal(a)
	y, ey := json.Marshal(b)
	if ex != nil || ey != nil {
		return false
	}
	hx, ex := JSONHash(x)
	hy, ey := JSONHash(y)
	return ex == nil && ey == nil && hx == hy
}

// frozenResolver 完全从运行产物取规格与价格，不连接当前数据库。
type frozenResolver struct {
	catalog store.CatalogSnapshot
	parts   map[string]store.Candidate
}

func newFrozenResolver(catalog store.CatalogSnapshot) (frozenResolver, error) {
	r := frozenResolver{catalog: catalog, parts: map[string]store.Candidate{}}
	for _, p := range catalog.Candidates {
		if p.SKU == "" {
			return r, fmt.Errorf("冻结目录含空 SKU")
		}
		if _, exists := r.parts[p.SKU]; exists {
			return r, fmt.Errorf("冻结目录含重复 SKU %q", p.SKU)
		}
		r.parts[p.SKU] = p
	}
	return r, nil
}

func (r frozenResolver) LatestSnapshot(context.Context) (store.Snapshot, error) {
	return r.catalog.Snapshot, nil
}

func (r frozenResolver) PricesBySnapshot(_ context.Context, id int64) ([]store.Price, error) {
	if id != r.catalog.Snapshot.ID {
		return nil, fmt.Errorf("冻结价格批次不一致")
	}
	var prices []store.Price
	for _, p := range r.catalog.Candidates {
		if p.PriceCNY != nil {
			prices = append(prices, store.Price{SKU: p.SKU, PriceCNY: *p.PriceCNY})
		}
	}
	return prices, nil
}

func resolveFrozen[T any](r frozenResolver, sku string, category schemas.Category, decode func([]byte) (T, error), dst *T) error {
	p, ok := r.parts[sku]
	if !ok || p.Category != category {
		return fmt.Errorf("%s 的 SKU %q 不存在或品类不符", category, sku)
	}
	value, err := decode(p.Specs)
	if err != nil {
		return fmt.Errorf("%s 规格无效：%w", sku, err)
	}
	*dst = value
	return nil
}

func (r frozenResolver) ResolveBuild(_ context.Context, sel schemas.BuildSelection) (schemas.ResolvedBuild, error) {
	out := schemas.ResolvedBuild{BuildRef: sel.BuildRef}
	checks := []error{
		resolveFrozen(r, sel.CPU, schemas.CategoryCPU, schemas.DecodeCPUSpec, &out.CPU),
		resolveFrozen(r, sel.Motherboard, schemas.CategoryMotherboard, schemas.DecodeMotherboardSpec, &out.Motherboard),
		resolveFrozen(r, sel.Memory, schemas.CategoryMemory, schemas.DecodeMemorySpec, &out.Memory),
		resolveFrozen(r, sel.PSU, schemas.CategoryPSU, schemas.DecodePSUSpec, &out.PSU),
		resolveFrozen(r, sel.Case, schemas.CategoryCase, schemas.DecodeCaseSpec, &out.Case),
		resolveFrozen(r, sel.Cooler, schemas.CategoryCooler, schemas.DecodeCoolerSpec, &out.Cooler),
	}
	for _, err := range checks {
		if err != nil {
			return out, err
		}
	}
	if sel.GPU != nil {
		out.GPU = new(schemas.GPUSpec)
		if err := resolveFrozen(r, *sel.GPU, schemas.CategoryGPU, schemas.DecodeGPUSpec, out.GPU); err != nil {
			return out, err
		}
	}
	for _, ssd := range sel.SSDs {
		var spec schemas.SSDSpec
		if err := resolveFrozen(r, ssd.SKU, schemas.CategorySSD, schemas.DecodeSSDSpec, &spec); err != nil {
			return out, err
		}
		out.SSDs = append(out.SSDs, schemas.ResolvedSSD{Spec: spec, Quantity: ssd.Quantity})
	}
	return out, nil
}
