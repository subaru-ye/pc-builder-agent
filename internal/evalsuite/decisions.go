package evalsuite

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func assertNonDelivery(c Case, r buildharness.BuildResult, snap SnapshotView) Verdict {
	fail := func(detail string) Verdict {
		return Verdict{Failures: []AssertionFailure{{ID: "N1", Name: "非交付结果及证据", Detail: detail}}}
	}
	d := r.Decision
	if r.Succeeded || d == nil || d.Kind != c.Expect.Outcome || d.Reason != c.Expect.Reason || strings.TrimSpace(r.Message) == "" || r.Message != d.Message {
		return fail("未交付状态、期望类型、原因码或可见说明不匹配")
	}
	if d.Kind == "search_exhausted" {
		if d.Reason != "no_verified_solution" || d.Scope != "current_run" || r.Attempts < 1 || r.Attempts > buildharness.MaxAttempts {
			return fail("没有有界搜索执行证据")
		}
		if r.Result.Report.OverallStatus != schemas.OverallFail && assertBudgetWindow(c.Requirement, r.Result.Quote) == "" {
			return fail("已有可交付结果却宣称搜索失败")
		}
		if !strings.Contains(r.Message, "不能据此认定") {
			return fail("搜索失败说明未限定结论范围")
		}
		// 正确停止搜索仍须保留用户已有件和硬锁；不能用失败报告掩盖越权换件。
		if detail := assertOwnership(c, r, snap); detail != "" {
			return Verdict{Failures: []AssertionFailure{{ID: "A9", Name: "已有件与采购报价", Detail: detail, Veto: true}}}
		}
		if detail := assertLocked(c.Locked, c.BaseSelection, &r.Draft.Selection); detail != "" {
			return Verdict{Failures: []AssertionFailure{{ID: "A8", Name: "锁定品类复验", Detail: detail, Veto: true}}}
		}
		return Verdict{Passed: true}
	}
	if d.Reason == "selected_price_missing" {
		if d.Scope != "current_catalog" || d.SnapshotDate != snap.SnapshotDate || r.Attempts < 1 || validate.BudgetQuote(c.Requirement, r.Result.Quote).MissingCount < 1 {
			return fail("缺少所选配件缺价证据")
		}
		return Verdict{Passed: true}
	}
	if snap.Catalog == nil {
		return fail("轨迹未保存用于复验的完整目录")
	}
	want := buildharness.AssessCatalog(buildharness.BuildInput{Requirement: c.Requirement, Change: c.Change, BaseSelection: c.BaseSelection, Locked: c.Locked}, *snap.Catalog)
	// 保留旧说明的逐字复验，不因现行模板改善而把历史通过改成失败。
	// 仅兼容已发布的精确模板；字段、类型和其他证据仍须完整一致。
	if want != nil && want.Reason == "missing_owned_information" && d.ExplanationVersion == 0 {
		want.ExplanationVersion = 0
		want.Message = "请补充已有配件的完整型号和数量，并确认预算是仅用于新增购买，还是包含已有件的整机参考总价？缺失项：" + strings.Join(want.Fields, "、")
	}
	if want == nil || !reflect.DeepEqual(d, want) || r.Attempts != 0 {
		return fail("无法从冻结输入与完整目录重建非交付证据")
	}
	return Verdict{Passed: true}
}

func assertOwnership(c Case, r buildharness.BuildResult, snap SnapshotView) string {
	if fields := schemas.MissingOwnedFields(c.Requirement); len(fields) > 0 {
		return fmt.Sprintf("已有件信息未补齐:%v", fields)
	}
	if len(c.Requirement.OwnedParts) == 0 {
		return ""
	}
	if snap.Catalog == nil {
		return "缺少已有件型号核验目录"
	}
	for _, p := range c.Requirement.OwnedParts {
		matches := []string{}
		for _, v := range snap.Catalog.Candidates {
			if v.Category == p.Category && (strings.EqualFold(strings.TrimSpace(p.Model), v.Model) || strings.EqualFold(strings.TrimSpace(p.Model), v.Brand+" "+v.Model)) {
				matches = append(matches, v.SKU)
			}
		}
		actual := categorySKUs(r.Draft.Selection, p.Category)
		if len(matches) != 1 || !reflect.DeepEqual(matches, actual) {
			return "已有件型号被替换或未唯一匹配"
		}
		if p.Category == schemas.CategorySSD {
			qty := p.Quantity
			if qty == 0 {
				qty = 1
			}
			if len(r.Draft.Selection.SSDs) != 1 || r.Draft.Selection.SSDs[0].Quantity != qty {
				return "已有 SSD 数量被修改"
			}
		}
	}
	want := validate.WithOwnership(r.Result.Quote, c.Requirement)
	if !reflect.DeepEqual(want.PurchaseTotalCNY, r.Result.Quote.PurchaseTotalCNY) || want.PurchaseMissingCount != r.Result.Quote.PurchaseMissingCount {
		return "采购合计或缺价数错误"
	}
	for i, line := range want.Lines {
		if line.Owned != r.Result.Quote.Lines[i].Owned {
			return "已有件报价标注错误"
		}
	}
	return ""
}
