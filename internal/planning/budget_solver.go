package planning

// 服务端确定性压价求解器。预算门回环耗尽后模型仍以超预算 draft 终局交付时，
// 服务端在本地下落内做一次约束满足搜索：只动未锁定品类、替换后容量/性能档位
// 不降、替换组合必须重新通过全部校验规则；找不到时给出可证明的最小牺牲。
// 纯服务端执行，不发出任何模型请求，保证既有录制 replay 不发生错位。

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func categoryLabel(c schemas.Category) string {
	labels := map[schemas.Category]string{
		schemas.CategoryCPU: "CPU", schemas.CategoryGPU: "显卡", schemas.CategoryMotherboard: "主板",
		schemas.CategoryMemory: "内存", schemas.CategorySSD: "固态硬盘", schemas.CategoryPSU: "电源",
		schemas.CategoryCase: "机箱", schemas.CategoryCooler: "散热器",
	}
	if label := labels[c]; label != "" {
		return label
	}
	return string(c)
}

func parsePriceRat(raw *string) *big.Rat {
	if raw == nil {
		return nil
	}
	v, ok := new(big.Rat).SetString(*raw)
	if !ok || v.Sign() < 0 {
		return nil
	}
	return v
}

func priceText(c Candidate) string {
	if c.Price == nil {
		return "?"
	}
	return *c.Price
}

func displayName(c Candidate) string {
	if c.Model != "" {
		return strings.TrimSpace(c.Brand + " " + c.Model)
	}
	return c.ID
}

// ponytail: canonical 目录规格不含容量字段，内存/SSD 容量只能从型号文本解析
// （与 planningeval.memoryCapacityGB 同一局限）；型号不含容量时该候选不可比，
// 保守跳过。升级路径是给目录规格字典补 capacity 字段。
var capacityPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(tb|gb)`)

func capacityGB(text string) int {
	m := capacityPattern.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	if strings.EqualFold(m[2], "tb") {
		v *= 1024
	}
	return int(v)
}

func candidateCapacityGB(c Candidate) int {
	if n := capacityGB(c.Model); n > 0 {
		return n
	}
	return capacityGB(c.ID)
}

func candidateSpecs(c Candidate) map[string]json.RawMessage {
	specs := map[string]json.RawMessage{}
	_ = json.Unmarshal(c.Specs, &specs)
	return specs
}

func specInt(specs map[string]json.RawMessage, key string) *int {
	raw := specs[key]
	if len(raw) == 0 {
		return nil
	}
	var v int
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return &v
}

func specString(specs map[string]json.RawMessage, key string) *string {
	raw := specs[key]
	if len(raw) == 0 {
		return nil
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return &v
}

func specIntText(specs map[string]json.RawMessage, key string) string {
	if v := specInt(specs, key); v != nil {
		return strconv.Itoa(*v)
	}
	return "?"
}

func specStringText(specs map[string]json.RawMessage, key string) string {
	if v := specString(specs, key); v != nil {
		return *v
	}
	return "?"
}

// tierNotLower 判定替换候选在程序可证明的容量/性能档位上不低于原选择；
// 任一关键档位字段缺失即视为不可比，保守拒绝替换。canonical 规格不含
// CPU 核心数与 GPU 显存，这两类的档位无法程序证明，不参与确定性替换。
func tierNotLower(cat schemas.Category, old, new Candidate) bool {
	o, n := candidateSpecs(old), candidateSpecs(new)
	switch cat {
	case schemas.CategoryPSU:
		ow, nw := specInt(o, "wattage_w"), specInt(n, "wattage_w")
		return ow != nil && nw != nil && *nw >= *ow
	case schemas.CategoryMotherboard:
		om, nm := specInt(o, "m2_slots"), specInt(n, "m2_slots")
		os, ns := specInt(o, "memory_speed_max_mts"), specInt(n, "memory_speed_max_mts")
		return om != nil && nm != nil && *nm >= *om && os != nil && ns != nil && *ns >= *os
	case schemas.CategoryCooler:
		ot, nt := specString(o, "type"), specString(n, "type")
		if ot == nil || nt == nil || *ot != *nt {
			return false
		}
		if *ot == "air" {
			oc, nc := specInt(o, "cooling_capacity_w"), specInt(n, "cooling_capacity_w")
			return oc != nil && nc != nil && *nc >= *oc
		}
		or, nr := specInt(o, "radiator_size_mm"), specInt(n, "radiator_size_mm")
		return or != nil && nr != nil && *nr >= *or
	case schemas.CategoryMemory:
		og, ng := specString(o, "generation"), specString(n, "generation")
		os, ns := specInt(o, "speed_mts"), specInt(n, "speed_mts")
		oc, nc := candidateCapacityGB(old), candidateCapacityGB(new)
		return oc > 0 && nc >= oc && og != nil && ng != nil && strings.EqualFold(*og, *ng) &&
			os != nil && ns != nil && *ns >= *os
	case schemas.CategorySSD:
		of, nf := specString(o, "form_factor"), specString(n, "form_factor")
		oc, nc := candidateCapacityGB(old), candidateCapacityGB(new)
		return oc > 0 && nc >= oc && of != nil && nf != nil && *of == *nf
	}
	return false
}

// tierBasis 生成留痕用的档位依据文本（换成什么、依据是什么）。
func tierBasis(cat schemas.Category, old, new Candidate) string {
	o, n := candidateSpecs(old), candidateSpecs(new)
	switch cat {
	case schemas.CategoryPSU:
		return "电源功率 " + specIntText(o, "wattage_w") + "W→" + specIntText(n, "wattage_w") + "W"
	case schemas.CategoryMotherboard:
		return "M.2 槽位 " + specIntText(o, "m2_slots") + "→" + specIntText(n, "m2_slots") +
			"、内存频率上限 " + specIntText(o, "memory_speed_max_mts") + "→" + specIntText(n, "memory_speed_max_mts") + " MT/s"
	case schemas.CategoryCooler:
		return "散热类型 " + specStringText(o, "type") + "、散热能力 " + specIntText(o, "cooling_capacity_w") + "→" + specIntText(n, "cooling_capacity_w") + "W"
	case schemas.CategoryMemory:
		return "容量 " + strconv.Itoa(candidateCapacityGB(old)) + "GB→" + strconv.Itoa(candidateCapacityGB(new)) + "GB、代际 " +
			specStringText(o, "generation") + "、频率 " + specIntText(o, "speed_mts") + "→" + specIntText(n, "speed_mts") + " MT/s"
	case schemas.CategorySSD:
		return "容量 " + strconv.Itoa(candidateCapacityGB(old)) + "GB→" + strconv.Itoa(candidateCapacityGB(new)) + "GB、接口形态 " + specStringText(n, "form_factor")
	}
	return "档位不降"
}

// budgetReplacement 一笔替换的留痕：旧→新、价格、省额、依据。
type budgetReplacement struct {
	Category  schemas.Category `json:"category"`
	SSDIndex  int              `json:"ssd_index"`
	FromID    string           `json:"from_id"`
	FromName  string           `json:"from_name"`
	FromPrice string           `json:"from_price_cny"`
	ToID      string           `json:"to_id"`
	ToName    string           `json:"to_name"`
	ToPrice   string           `json:"to_price_cny"`
	SavingCNY string           `json:"saving_cny"`
	Quantity  int              `json:"quantity"`
	Basis     string           `json:"basis"`
}

// budgetFix 求解成功的完整替换组合（总价已按预算口径压回上限内）。
type budgetFix struct {
	Replacements []budgetReplacement `json:"replacements"`
	TotalCNY     string              `json:"total_cny"`
}

// budgetSacrifice 求解失败时唯一能压回预算的最小单笔牺牲（档位/容量降级）。
type budgetSacrifice struct {
	Category  schemas.Category `json:"category"`
	FromName  string           `json:"from_name"`
	FromPrice string           `json:"from_price_cny"`
	ToID      string           `json:"to_id"`
	ToName    string           `json:"to_name"`
	ToPrice   string           `json:"to_price_cny"`
	SavingCNY string           `json:"saving_cny"`
}

func (f *budgetFix) notes(kind string) []string {
	out := []string{}
	for _, r := range f.Replacements {
		plural := ""
		if r.Quantity > 1 {
			plural = fmt.Sprintf("×%d", r.Quantity)
		}
		delta := ""
		if v, ok := new(big.Rat).SetString(r.SavingCNY); ok {
			switch {
			case v.Sign() > 0:
				delta = fmt.Sprintf("，省 %s 元", r.SavingCNY)
			case v.Sign() < 0:
				delta = fmt.Sprintf("，涨价 %s 元", new(big.Rat).Neg(v).FloatString(2))
			}
		}
		out = append(out, fmt.Sprintf("服务端%s：%s 从 %s(%s元) 换为 %s(%s元)%s%s；依据：目录内同品类候选、%s、替换组合已重新通过全部兼容与报价校验。",
			kind, categoryLabel(r.Category), r.FromName, r.FromPrice, r.ToName, r.ToPrice, plural, delta, r.Basis))
	}
	return out
}

func (s *budgetSacrifice) issue() string {
	return fmt.Sprintf("预算压价求解：各未锁定品类在容量与性能档位不降的约束下已无目录内可行替换；最小牺牲方案是把 %s 从 %s(%s元) 降为 %s(%s元) 可省 %s 元压回预算，这涉及用途相关容量/档位牺牲，需用户确认后执行。",
		categoryLabel(s.Category), s.FromName, s.FromPrice, s.ToName, s.ToPrice, s.SavingCNY)
}

// quoteAmount 取报价的预算口径金额（new_purchase 时用新增采购合计）。
func quoteAmount(q validate.Quote, basis string) *big.Rat {
	amount := q.TotalCNY
	if basis == "new_purchase" && q.PurchaseTotalCNY != nil {
		amount = *q.PurchaseTotalCNY
	}
	v, ok := new(big.Rat).SetString(amount)
	if !ok || v.Sign() < 0 {
		return nil
	}
	return v
}

// budgetOverrun 返回按预算口径的超支额与上限；未超支时 ok=false。
func (x *execution) budgetOverrun() (over, upper *big.Rat, ok bool) {
	upper, ok = x.budgetCeiling()
	if !ok || x.result.Quote == nil {
		return nil, nil, false
	}
	amount := quoteAmount(*x.result.Quote, x.accountingSpec().BudgetBasis)
	if amount == nil || amount.Cmp(upper) <= 0 {
		return nil, nil, false
	}
	return new(big.Rat).Sub(amount, upper), upper, true
}

// solverLockedCategories 服务端压价不可触碰的品类：已有件按品类核账的替身、
// must 品牌（替换可能违背品牌要求）与静音/外观（替换后无法程序保证）。
func (x *execution) solverLockedCategories(draft schemas.BuildDraft) map[schemas.Category]bool {
	locked := map[schemas.Category]bool{}
	for _, p := range x.verifiedOwnership(draft).OwnedParts {
		locked[p.Category] = true
	}
	activeMust := func(key string) bool {
		f := x.input.State.Fields[key]
		return f.Status == "active" && f.Strength == "must"
	}
	if activeMust("brand_pref.cpu") {
		locked[schemas.CategoryCPU] = true
	}
	if activeMust("brand_pref.gpu") {
		locked[schemas.CategoryGPU] = true
	}
	if f := x.input.State.Fields["noise_pref"]; activeMust("noise_pref") && func() bool {
		var v string
		return json.Unmarshal(f.Value, &v) == nil && v == "silent"
	}() {
		locked[schemas.CategoryCooler], locked[schemas.CategoryCase], locked[schemas.CategoryPSU] = true, true, true
	}
	if activeMust("appearance") {
		locked[schemas.CategoryCase] = true
	}
	return locked
}

// budgetSlot 一个可替换槽位：原选择、数量与按价格降序排列的合格更低价候选。
type budgetSlot struct {
	Category schemas.Category
	SSDIndex int // -1 表示非 SSD 槽
	OldID    string
	OldName  string
	OldPrice *big.Rat
	Quantity int
	Options  []Candidate
	MaxSave  *big.Rat
}

// budgetSlots 展开 draft 的全部未锁定槽位。forSacrifice=true 时不做档位过滤
//（牺牲路径本就是降档），但锁定品类仍排除。
func (x *execution) budgetSlots(draft schemas.BuildDraft, locked map[schemas.Category]bool, forSacrifice bool) []budgetSlot {
	byID := map[string]Candidate{}
	for _, c := range x.candidates {
		byID[c.ID] = c
	}
	var slots []budgetSlot
	addSlot := func(cat schemas.Category, oldID string, qty, ssdIndex int) {
		if locked[cat] || oldID == "" {
			return
		}
		old, ok := byID[oldID]
		if !ok {
			return
		}
		oldPrice := parsePriceRat(old.Price)
		if oldPrice == nil {
			return
		}
		slot := budgetSlot{Category: cat, SSDIndex: ssdIndex, OldID: oldID, OldName: displayName(old), OldPrice: oldPrice, Quantity: qty}
		for _, c := range x.candidates {
			if c.Category != cat || c.ID == oldID || c.External {
				continue
			}
			p := parsePriceRat(c.Price)
			if p == nil || p.Cmp(oldPrice) >= 0 {
				continue
			}
			if !forSacrifice && !tierNotLower(cat, old, c) {
				continue
			}
			slot.Options = append(slot.Options, c)
		}
		if len(slot.Options) == 0 {
			return
		}
		sort.Slice(slot.Options, func(i, j int) bool {
			pi, pj := parsePriceRat(slot.Options[i].Price), parsePriceRat(slot.Options[j].Price)
			if pi == nil || pj == nil {
				return pj == nil
			}
			if pi.Cmp(pj) != 0 {
				// 价格降序：最小降幅优先，解尽量贴近原配置。
				return pi.Cmp(pj) > 0
			}
			return slot.Options[i].ID < slot.Options[j].ID
		})
		lowest := parsePriceRat(slot.Options[len(slot.Options)-1].Price)
		slot.MaxSave = new(big.Rat).Sub(oldPrice, lowest)
		slot.MaxSave.Mul(slot.MaxSave, new(big.Rat).SetInt64(int64(qty)))
		slots = append(slots, slot)
	}
	sel := draft.Selection
	addSlot(schemas.CategoryCPU, sel.CPU, 1, -1)
	if sel.GPU != nil {
		addSlot(schemas.CategoryGPU, *sel.GPU, 1, -1)
	}
	addSlot(schemas.CategoryMotherboard, sel.Motherboard, 1, -1)
	addSlot(schemas.CategoryMemory, sel.Memory, 1, -1)
	for i, s := range sel.SSDs {
		addSlot(schemas.CategorySSD, s.SKU, s.Quantity, i)
	}
	addSlot(schemas.CategoryPSU, sel.PSU, 1, -1)
	addSlot(schemas.CategoryCase, sel.Case, 1, -1)
	addSlot(schemas.CategoryCooler, sel.Cooler, 1, -1)
	catIndex := func(c schemas.Category) int {
		for i, k := range schemas.AllCategories {
			if k == c {
				return i
			}
		}
		return len(schemas.AllCategories)
	}
	// 贪心序：最大可省额降序，平手按品类固定序与旧件 ID，保证结果确定。
	sort.SliceStable(slots, func(i, j int) bool {
		if slots[i].MaxSave.Cmp(slots[j].MaxSave) != 0 {
			return slots[i].MaxSave.Cmp(slots[j].MaxSave) > 0
		}
		if catIndex(slots[i].Category) != catIndex(slots[j].Category) {
			return catIndex(slots[i].Category) < catIndex(slots[j].Category)
		}
		return slots[i].OldID < slots[j].OldID
	})
	return slots
}

// searchBudgetFix 贪心（按省额降序）+ 回溯：每槽先试保留再试更低价候选
//（最小降幅优先），首个总价回到上限内且重新通过全部校验的组合即中选，
// 因此解的总降幅贴近超支额、改动最小。
func (x *execution) searchBudgetFix(ctx context.Context, draft schemas.BuildDraft, slots []budgetSlot, upper, over *big.Rat) *budgetFix {
	if len(slots) == 0 || over.Sign() <= 0 {
		return nil
	}
	suffix := make([]*big.Rat, len(slots)+1)
	suffix[len(slots)] = new(big.Rat)
	for i := len(slots) - 1; i >= 0; i-- {
		suffix[i] = new(big.Rat).Add(suffix[i+1], slots[i].MaxSave)
	}
	chosen := make([]int, len(slots))
	for i := range chosen {
		chosen[i] = -1
	}
	nodes := 0
	// ponytail: 每品类 <30 候选、可替换品类 ≤8，实际搜索远小于上限；防御极端目录。
	const maxNodes = 200000
	var dfs func(i int, remaining *big.Rat) *budgetFix
	dfs = func(i int, remaining *big.Rat) *budgetFix {
		if remaining.Sign() <= 0 {
			return x.verifyBudgetSelection(ctx, draft, slots, chosen, upper)
		}
		if i == len(slots) || suffix[i].Cmp(remaining) < 0 {
			return nil
		}
		if nodes++; nodes > maxNodes {
			return nil
		}
		if fix := dfs(i+1, remaining); fix != nil {
			return fix
		}
		for oi, c := range slots[i].Options {
			p := parsePriceRat(c.Price)
			if p == nil {
				continue
			}
			save := new(big.Rat).Sub(slots[i].OldPrice, p)
			save.Mul(save, new(big.Rat).SetInt64(int64(slots[i].Quantity)))
			chosen[i] = oi
			if fix := dfs(i+1, new(big.Rat).Sub(remaining, save)); fix != nil {
				return fix
			}
			chosen[i] = -1
		}
		return nil
	}
	return dfs(0, over)
}

// applySlotSwap 把一个槽位替换为新 SKU。
func applySlotSwap(sel *schemas.BuildSelection, cat schemas.Category, ssdIndex int, id string) {
	switch {
	case ssdIndex >= 0:
		sel.SSDs[ssdIndex].SKU = id
	case cat == schemas.CategoryCPU:
		sel.CPU = id
	case cat == schemas.CategoryGPU:
		gpu := id
		sel.GPU = &gpu
	case cat == schemas.CategoryMotherboard:
		sel.Motherboard = id
	case cat == schemas.CategoryMemory:
		sel.Memory = id
	case cat == schemas.CategoryPSU:
		sel.PSU = id
	case cat == schemas.CategoryCase:
		sel.Case = id
	case cat == schemas.CategoryCooler:
		sel.Cooler = id
	}
}

// verifyBudgetSelection 把选中组合写回 selection 并重新通过全部校验规则；
// 校验未全过或总价仍未压回时该组合作废，搜索继续。
func (x *execution) verifyBudgetSelection(ctx context.Context, draft schemas.BuildDraft, slots []budgetSlot, chosen []int, upper *big.Rat) *budgetFix {
	sel := draft.Selection
	// SSD 槽位替换不能共享底层 slice。
	sel.SSDs = append([]schemas.SSDSelection(nil), draft.Selection.SSDs...)
	replacements := []budgetReplacement{}
	for si, slot := range slots {
		if chosen[si] < 0 {
			continue
		}
		c := slot.Options[chosen[si]]
		old := byIDFallback(x.candidates, slot.OldID)
		applySlotSwap(&sel, slot.Category, slot.SSDIndex, c.ID)
		save := new(big.Rat).Sub(slot.OldPrice, parsePriceRat(c.Price))
		save.Mul(save, new(big.Rat).SetInt64(int64(slot.Quantity)))
		replacements = append(replacements, budgetReplacement{
			Category: slot.Category, SSDIndex: slot.SSDIndex,
			FromID: slot.OldID, FromName: slot.OldName, FromPrice: priceText(old),
			ToID: c.ID, ToName: displayName(c), ToPrice: priceText(c),
			SavingCNY: save.FloatString(2), Quantity: slot.Quantity,
			Basis: tierBasis(slot.Category, old, c),
		})
	}
	res, err := validate.New(snapshotResolver{snapshotID: x.snapshotID, candidates: x.candidates, date: x.date}).Evaluate(ctx, sel)
	if err != nil || res.Report.OverallStatus != schemas.OverallPass || res.Quote.MissingCount != 0 {
		return nil
	}
	total := quoteAmount(res.Quote, x.accountingSpec().BudgetBasis)
	if total == nil || total.Cmp(upper) > 0 {
		return nil
	}
	return &budgetFix{Replacements: replacements, TotalCNY: total.FloatString(2)}
}

func byIDFallback(candidates []Candidate, id string) Candidate {
	for _, c := range candidates {
		if c.ID == id {
			return c
		}
	}
	return Candidate{ID: id}
}

// minBudgetSacrifice 档位不降约束下无解时，找唯一能单笔压回预算的最小牺牲：
// 在所有未锁定槽位的更低价候选中取省额最小且覆盖超支额的一笔。牺牲候选必须
// 重新通过全部校验规则——与用途硬条件冲突的降档（如以无核显 CPU 替代核显
// 点亮）不是可执行的牺牲方案，直接跳过。
func (x *execution) minBudgetSacrifice(ctx context.Context, draft schemas.BuildDraft, locked map[schemas.Category]bool, over *big.Rat) *budgetSacrifice {
	var best *budgetSacrifice
	var bestSaving *big.Rat
	for _, slot := range x.budgetSlots(draft, locked, true) {
		for _, c := range slot.Options {
			p := parsePriceRat(c.Price)
			if p == nil {
				continue
			}
			save := new(big.Rat).Sub(slot.OldPrice, p)
			save.Mul(save, new(big.Rat).SetInt64(int64(slot.Quantity)))
			if save.Cmp(over) < 0 {
				continue
			}
			if best != nil && bestSaving != nil && save.Cmp(bestSaving) > 0 {
				continue // 已有更小省额的合格牺牲
			}
			if !x.sacrificePasses(ctx, draft, slot, c) {
				continue
			}
			if bestSaving == nil || save.Cmp(bestSaving) < 0 || (save.Cmp(bestSaving) == 0 && c.ID < best.ToID) {
				best = &budgetSacrifice{
					Category: slot.Category, FromName: slot.OldName, FromPrice: priceText(byIDFallback(x.candidates, slot.OldID)),
					ToID: c.ID, ToName: displayName(c), ToPrice: priceText(c), SavingCNY: save.FloatString(2),
				}
				bestSaving = save
			}
		}
	}
	return best
}

// sacrificePasses 验证单笔牺牲不引入新的校验失败或新的 unknown——与用途硬
// 条件冲突的降档（如以无核显 CPU 替代核显点亮）被过滤；原 draft 已有的
// unknown 不阻断牺牲方案的成立。
func (x *execution) sacrificePasses(ctx context.Context, draft schemas.BuildDraft, slot budgetSlot, c Candidate) bool {
	if x.result.Validation == nil {
		return false
	}
	baseFails, baseUnknowns := validationCounts(x.result.Validation)
	sel := draft.Selection
	sel.SSDs = append([]schemas.SSDSelection(nil), draft.Selection.SSDs...)
	applySlotSwap(&sel, slot.Category, slot.SSDIndex, c.ID)
	res, err := validate.New(snapshotResolver{snapshotID: x.snapshotID, candidates: x.candidates, date: x.date}).Evaluate(ctx, sel)
	if err != nil || res.Quote.MissingCount != 0 {
		return false
	}
	fails, unknowns := validationCounts(&res.Report)
	return fails <= baseFails && unknowns <= baseUnknowns
}

func validationCounts(report *schemas.ValidationReport) (fails, unknowns int) {
	for _, check := range report.Checks {
		switch check.Outcome {
		case schemas.OutcomeFail:
			fails++
		case schemas.OutcomeUnknown:
			unknowns++
		}
	}
	return fails, unknowns
}

// solveBudget 求解入口：返回可行替换组合；无解时返回最小牺牲。
func (x *execution) solveBudget(ctx context.Context, draft schemas.BuildDraft) (*budgetFix, *budgetSacrifice, bool) {
	over, upper, ok := x.budgetOverrun()
	if !ok {
		return nil, nil, false
	}
	locked := x.solverLockedCategories(draft)
	if fix := x.searchBudgetFix(ctx, draft, x.budgetSlots(draft, locked, false), upper, over); fix != nil {
		return fix, nil, true
	}
	return nil, x.minBudgetSacrifice(ctx, draft, locked, over), true
}

// patchDraftJSON 只改写 selection 中被替换的槽位，rationale 等其余字段原样保留。
func patchDraftJSON(base json.RawMessage, fix *budgetFix) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(base, &doc) != nil {
		return nil, fmt.Errorf("draft 须为 JSON 对象")
	}
	var sel map[string]json.RawMessage
	if json.Unmarshal(doc["selection"], &sel) != nil {
		return nil, fmt.Errorf("selection 须为 JSON 对象")
	}
	ssdPatch := false
	for _, r := range fix.Replacements {
		raw, _ := json.Marshal(r.ToID)
		switch r.Category {
		case schemas.CategoryCPU:
			sel["cpu"] = raw
		case schemas.CategoryGPU:
			sel["gpu"] = raw
		case schemas.CategoryMotherboard:
			sel["motherboard"] = raw
		case schemas.CategoryMemory:
			sel["memory"] = raw
		case schemas.CategoryPSU:
			sel["psu"] = raw
		case schemas.CategoryCase:
			sel["case"] = raw
		case schemas.CategoryCooler:
			sel["cooler"] = raw
		case schemas.CategorySSD:
			ssdPatch = true
		}
	}
	if ssdPatch {
		var ssds []map[string]any
		if json.Unmarshal(sel["ssd"], &ssds) != nil {
			return nil, fmt.Errorf("ssd 须为对象数组")
		}
		for _, r := range fix.Replacements {
			if r.Category == schemas.CategorySSD {
				if r.SSDIndex < 0 || r.SSDIndex >= len(ssds) {
					return nil, fmt.Errorf("ssd 槽位越界")
				}
				ssds[r.SSDIndex]["sku"] = r.ToID
			}
		}
		sel["ssd"], _ = json.Marshal(ssds)
	}
	doc["selection"], _ = json.Marshal(sel)
	return json.Marshal(doc)
}

// dropStaleBudgetIssues 移除已被压价解决的"超预算待取舍"议题。
// ponytail: 用窄文本特征识别，不做语义判定；升级路径是结构化 issue 分类。
func dropStaleBudgetIssues(issues []string) []string {
	out := make([]string, 0, len(issues))
	for _, s := range issues {
		if strings.Contains(s, "预算") && (strings.Contains(s, "超出") || strings.Contains(s, "超过")) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// budgetFixDue 判定是否轮到服务端确定性压价：终局交付（含待取舍的 clarify）
// 超预算，且预算回环已耗尽或模型已用 price_asc 核验过目录低价。
func (x *execution) budgetFixDue(outcome string, clarifiesEvaluatedDraft bool, gates *deliveryGateCounters, turn, turns int) bool {
	if outcome != "ready" && outcome != "proposal" && !clarifiesEvaluatedDraft {
		return false
	}
	// 含 active must 约束的 free.* 字段无法程序映射到品类，无法保证"只动
	// 未锁定品类"，求解器保守停用（升级路径：结构化的品类级锁定表达）。
	for key, f := range x.input.State.Fields {
		if schemas.FreeField(key) && f.Status == "active" && f.Strength == "must" && f.Kind == "constraint" {
			return false
		}
	}
	if _, _, ok := x.budgetOverrun(); !ok {
		return false
	}
	// 总门耗尽（可能被其他门占用配额）同样构成预算回环耗尽。
	return turn >= turns-2 || gates.budget >= 2 || gates.total >= 3 || len(x.priceAsc) > 0
}

// applyBudgetFix 预算回环耗尽后的确定性压价终局：找到目录内可行替换则改写
// draft 并重新 evaluate 后交付（保留替换留痕），找不到则交付最小牺牲标注。
// 本函数不发出任何模型请求。
func (x *execution) applyBudgetFix(ctx context.Context) {
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil {
		return
	}
	fix, sacrifice, ok := x.solveBudget(ctx, draft)
	if !ok {
		return
	}
	x.result.Outcome = "proposal"
	if fix != nil {
		raw, err := patchDraftJSON(x.result.Draft, fix)
		if err != nil {
			return
		}
		if _, err := schemas.DecodeBuildDraft(raw); err != nil {
			return
		}
		x.evaluate(ctx, raw)
		x.result.Issues = dropStaleBudgetIssues(x.result.Issues)
		x.result.Issues = append(x.result.Issues, fix.notes("预算替换")...)
		return
	}
	if sacrifice != nil {
		x.result.Issues = append(x.result.Issues, sacrifice.issue())
		return
	}
	x.result.Issues = append(x.result.Issues, "预算压价求解：当前配置超出预算硬上限，且各未锁定品类已无目录内更低价候选与可牺牲项。")
}

// unknownCategoryFields 汇总校验报告中 unknown 检查涉及的品类与缺失字段。
func unknownCategoryFields(report *schemas.ValidationReport) map[schemas.Category][]string {
	if report == nil {
		return nil
	}
	out := map[schemas.Category][]string{}
	for _, check := range report.Checks {
		if check.Outcome != schemas.OutcomeUnknown {
			continue
		}
		for _, missing := range check.MissingFields {
			dot := strings.Index(missing, ".")
			if dot <= 0 {
				continue
			}
			cat, key := schemas.Category(missing[:dot]), missing[dot+1:]
			out[cat] = append(out[cat], key)
		}
	}
	return out
}

// unknownFixDue 判定是否轮到服务端确定性 unknown 修复：终局交付校验未全过、
// 存在 unknown 缺失字段，且 unknown 门回环已耗尽（与 budgetFixDue 同一保守
// 停用条件）。unknown 与兼容性仍以校验规则为准：修复只做"缺字段候选 →
// 字段完整候选"的目录内换选并重新通过全部校验，换不回 pass 就如实保留。
func (x *execution) unknownFixDue(outcome string, clarifiesEvaluatedDraft bool, gates *deliveryGateCounters, turn, turns int) bool {
	if outcome != "ready" && outcome != "proposal" && !clarifiesEvaluatedDraft {
		return false
	}
	if len(unknownCategoryFields(x.result.Validation)) == 0 {
		return false
	}
	for key, f := range x.input.State.Fields {
		if schemas.FreeField(key) && f.Status == "active" && f.Strength == "must" && f.Kind == "constraint" {
			return false
		}
	}
	return turn >= turns-2 || gates.unknown >= 2 || gates.total >= 3
}

// unknownSwapTierOK unknown 换选的档位约束（宽松版）：原候选缺失的字段
//（unknown 的来源）跳过比较——换选本就是"字段补全"；其余可比档位字段
// 仍必须不降，防止借补全之名降档（如 B650→A620、水冷→风冷）。
func unknownSwapTierOK(cat schemas.Category, old, new Candidate) bool {
	o, n := candidateSpecs(old), candidateSpecs(new)
	notLowerInt := func(key string) bool {
		ov, nv := specInt(o, key), specInt(n, key)
		return ov == nil || (nv != nil && *nv >= *ov)
	}
	switch cat {
	case schemas.CategoryPSU:
		return notLowerInt("wattage_w")
	case schemas.CategoryMotherboard:
		return notLowerInt("m2_slots") && notLowerInt("memory_speed_max_mts")
	case schemas.CategoryCooler:
		ot, nt := specString(o, "type"), specString(n, "type")
		if ot == nil || nt == nil || *ot != *nt {
			return false
		}
		if *ot == "air" {
			return notLowerInt("cooling_capacity_w")
		}
		return notLowerInt("radiator_size_mm")
	case schemas.CategoryMemory:
		og, ng := specString(o, "generation"), specString(n, "generation")
		if og != nil && (ng == nil || !strings.EqualFold(*og, *ng)) {
			return false
		}
		oc, nc := candidateCapacityGB(old), candidateCapacityGB(new)
		if oc > 0 && nc < oc {
			return false
		}
		return notLowerInt("speed_mts")
	case schemas.CategorySSD:
		of, nf := specString(o, "form_factor"), specString(n, "form_factor")
		if of != nil && (nf == nil || *of != *nf) {
			return false
		}
		oc, nc := candidateCapacityGB(old), candidateCapacityGB(new)
		return oc == 0 || nc >= oc
	}
	return true
}

// solveUnknownFix 对每个 unknown 品类槽位枚举缺失字段完整的目录内候选
//（价格升序、最小涨价优先），回溯找第一个重新通过全部校验的组合。
// 预算不在此判定：unknown 消除优先，超支交给压价阶段处理。
func (x *execution) solveUnknownFix(ctx context.Context, draft schemas.BuildDraft) (*budgetFix, bool) {
	missing := unknownCategoryFields(x.result.Validation)
	if len(missing) == 0 {
		return nil, false
	}
	locked := x.solverLockedCategories(draft)
	byID := map[string]Candidate{}
	for _, c := range x.candidates {
		byID[c.ID] = c
	}
	var slots []budgetSlot
	for _, cat := range schemas.AllCategories {
		keys, ok := missing[cat]
		if !ok || locked[cat] {
			continue
		}
		sel := draft.Selection
		oldID := ""
		switch cat {
		case schemas.CategoryCPU:
			oldID = sel.CPU
		case schemas.CategoryGPU:
			if sel.GPU != nil {
				oldID = *sel.GPU
			}
		case schemas.CategoryMotherboard:
			oldID = sel.Motherboard
		case schemas.CategoryMemory:
			oldID = sel.Memory
		case schemas.CategoryPSU:
			oldID = sel.PSU
		case schemas.CategoryCase:
			oldID = sel.Case
		case schemas.CategoryCooler:
			oldID = sel.Cooler
		default:
			for _, s := range sel.SSDs {
				if _, seen := missing[schemas.CategorySSD]; seen {
					oldID = s.SKU
					break
				}
			}
		}
		if oldID == "" {
			continue
		}
		old, ok := byID[oldID]
		if !ok {
			continue
		}
		slot := budgetSlot{Category: cat, SSDIndex: -1, OldID: oldID, OldName: displayName(old), OldPrice: parsePriceRat(old.Price), Quantity: 1, MaxSave: new(big.Rat)}
		for _, c := range x.candidates {
			if c.Category != cat || c.ID == oldID || c.External || c.Price == nil {
				continue
			}
			specs := candidateSpecs(c)
			complete := true
			for _, key := range keys {
				raw := specs[key]
				if len(raw) == 0 || string(raw) == "null" {
					complete = false
					break
				}
			}
			if complete && unknownSwapTierOK(cat, old, c) {
				slot.Options = append(slot.Options, c)
			}
		}
		if len(slot.Options) == 0 {
			continue
		}
		sort.SliceStable(slot.Options, func(i, j int) bool {
			pi, pj := parsePriceRat(slot.Options[i].Price), parsePriceRat(slot.Options[j].Price)
			if pi == nil || pj == nil {
				return pj == nil
			}
			if pi.Cmp(pj) != 0 {
				return pi.Cmp(pj) < 0 // 价格升序：消 unknown 顺带最小涨价
			}
			return slot.Options[i].ID < slot.Options[j].ID
		})
		slots = append(slots, slot)
	}
	if len(slots) == 0 {
		return nil, false
	}
	chosen := make([]int, len(slots))
	for i := range chosen {
		chosen[i] = -1
	}
	nodes := 0
	const maxNodes = 200000
	var dfs func(i int) *budgetFix
	dfs = func(i int) *budgetFix {
		if i == len(slots) {
			return x.verifyUnknownSelection(ctx, draft, slots, chosen)
		}
		if nodes++; nodes > maxNodes {
			return nil
		}
		if fix := dfs(i + 1); fix != nil {
			return fix
		}
		for oi := range slots[i].Options {
			chosen[i] = oi
			if fix := dfs(i + 1); fix != nil {
				return fix
			}
			chosen[i] = -1
		}
		return nil
	}
	fix := dfs(0)
	return fix, fix != nil
}

// verifyUnknownSelection 校验换选组合：全部规则通过且零缺价即成立。
func (x *execution) verifyUnknownSelection(ctx context.Context, draft schemas.BuildDraft, slots []budgetSlot, chosen []int) *budgetFix {
	sel := draft.Selection
	sel.SSDs = append([]schemas.SSDSelection(nil), draft.Selection.SSDs...)
	replacements := []budgetReplacement{}
	for si, slot := range slots {
		if chosen[si] < 0 {
			continue
		}
		c := slot.Options[chosen[si]]
		old := byIDFallback(x.candidates, slot.OldID)
		applySlotSwap(&sel, slot.Category, slot.SSDIndex, c.ID)
		replacements = append(replacements, budgetReplacement{
			Category: slot.Category,
			FromID:   slot.OldID, FromName: slot.OldName, FromPrice: priceText(old),
			ToID: c.ID, ToName: displayName(c), ToPrice: priceText(c),
			SavingCNY: new(big.Rat).Sub(slot.OldPrice, parsePriceRat(c.Price)).FloatString(2),
			Basis:     "字段补全（原候选规格缺失，新候选字段完整）",
		})
	}
	res, err := validate.New(snapshotResolver{snapshotID: x.snapshotID, candidates: x.candidates, date: x.date}).Evaluate(ctx, sel)
	if err != nil || res.Report.OverallStatus != schemas.OverallPass || res.Quote.MissingCount != 0 {
		return nil
	}
	total := quoteAmount(res.Quote, x.accountingSpec().BudgetBasis)
	if total == nil {
		return nil
	}
	return &budgetFix{Replacements: replacements, TotalCNY: total.FloatString(2)}
}

// applyUnknownFix unknown 门回环耗尽后的确定性换选终局：找到"字段完整候选"
// 组合则改写 draft 并重新 evaluate 后交付；换不回 pass 就不动，unknown 如实
// 保留。本函数不发出任何模型请求。
func (x *execution) applyUnknownFix(ctx context.Context) {
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil {
		return
	}
	fix, ok := x.solveUnknownFix(ctx, draft)
	if !ok {
		return
	}
	raw, err := patchDraftJSON(x.result.Draft, fix)
	if err != nil {
		return
	}
	if _, err := schemas.DecodeBuildDraft(raw); err != nil {
		return
	}
	x.evaluate(ctx, raw)
	x.result.Outcome = "proposal"
	x.result.Issues = dropStaleSpecIssues(x.result.Issues)
	x.result.Issues = append(x.result.Issues, fix.notes("unknown 修复换选")...)
}

// dropStaleSpecIssues 移除已被换选解决的"规格字段缺失"自述议题。
// ponytail: 窄文本特征识别，升级路径是结构化 issue 分类。
func dropStaleSpecIssues(issues []string) []string {
	out := make([]string, 0, len(issues))
	for _, s := range issues {
		if strings.Contains(s, "字段缺失") || strings.Contains(s, "无法判定") {
			continue
		}
		out = append(out, s)
	}
	return out
}
