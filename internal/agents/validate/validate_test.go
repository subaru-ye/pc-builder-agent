package validate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// fakeData 实现 Resolver,用于无 DB 单测。
type fakeData struct {
	resolved   schemas.ResolvedBuild
	resolveErr error
	snap       store.Snapshot
	snapErr    error
	prices     []store.Price
	priceErr   error
}

func (f fakeData) ResolveBuild(context.Context, schemas.BuildSelection) (schemas.ResolvedBuild, error) {
	return f.resolved, f.resolveErr
}
func (f fakeData) LatestSnapshot(context.Context) (store.Snapshot, error) {
	return f.snap, f.snapErr
}
func (f fakeData) PricesBySnapshot(context.Context, int64) ([]store.Price, error) {
	return f.prices, f.priceErr
}

func strp(s string) *string { return &s }

// sampleSelection 一份含独显 + 两条 SSD(其一多件)的配置。
func sampleSelection() schemas.BuildSelection {
	return schemas.BuildSelection{
		SchemaVersion: 1,
		BuildRef:      "b1",
		CPU:           "cpu1",
		GPU:           strp("gpu1"),
		Motherboard:   "mb1",
		Memory:        "mem1",
		SSDs:          []schemas.SSDSelection{{SKU: "ssd1", Quantity: 2}, {SKU: "ssd2", Quantity: 1}},
		PSU:           "psu1",
		Case:          "case1",
		Cooler:        "cooler1",
	}
}

func TestParseCents(t *testing.T) {
	ok := map[string]int64{
		"1299.00": 129900,
		"1299":    129900,
		"0.50":    50,
		"0.5":     50,
		" 100 ":   10000,
		".5":      50,
		"0":       0,
	}
	for in, want := range ok {
		got, err := parseCents(in)
		if err != nil {
			t.Errorf("parseCents(%q) 意外报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseCents(%q) = %d, 期望 %d", in, got, want)
		}
	}
	bad := []string{"", "abc", "1.234", "-5", "1.2.3", "12.3a"}
	for _, in := range bad {
		if _, err := parseCents(in); err == nil {
			t.Errorf("parseCents(%q) 应报错但没有", in)
		}
	}
}

func TestFormatCentsRoundTrip(t *testing.T) {
	for _, c := range []int64{0, 50, 100, 129900, 610000} {
		got, err := parseCents(formatCents(c))
		if err != nil || got != c {
			t.Errorf("round-trip %d: got %d err %v", c, got, err)
		}
	}
	if formatCents(50) != "0.50" {
		t.Errorf("formatCents(50) = %q, 期望 0.50", formatCents(50))
	}
}

func TestComputeQuote(t *testing.T) {
	prices := map[string]string{
		"cpu1":    "1000.00",
		"gpu1":    "2000.00",
		"mb1":     "800.00",
		"mem1":    "300.00",
		"ssd1":    "500.00",
		"psu1":    "400.00",
		"case1":   "350.00",
		"cooler1": "250.00",
		// ssd2 故意缺价
	}
	q := computeQuote(sampleSelection(), "2026-07-28", prices)

	// 合计 = 1000+2000+800+300+500*2+400+350+250 = 6100.00
	if q.TotalCNY != "6100.00" {
		t.Errorf("TotalCNY = %q, 期望 6100.00", q.TotalCNY)
	}
	if q.SnapshotDate != "2026-07-28" {
		t.Errorf("SnapshotDate = %q", q.SnapshotDate)
	}
	if q.MissingCount != 1 || len(q.MissingSKUs) != 1 || q.MissingSKUs[0] != "ssd2" {
		t.Errorf("缺价预期仅 ssd2,得到 count=%d skus=%v", q.MissingCount, q.MissingSKUs)
	}
	if len(q.Lines) != 9 {
		t.Fatalf("Lines 数 = %d, 期望 9", len(q.Lines))
	}
	// 逐行验证 ssd1(多件小计)与 ssd2(缺价)
	var ssd1, ssd2 *QuoteLine
	for i := range q.Lines {
		switch q.Lines[i].SKU {
		case "ssd1":
			ssd1 = &q.Lines[i]
		case "ssd2":
			ssd2 = &q.Lines[i]
		}
	}
	if ssd1 == nil || ssd1.UnitPriceCNY == nil || *ssd1.UnitPriceCNY != "500.00" ||
		ssd1.SubtotalCNY == nil || *ssd1.SubtotalCNY != "1000.00" {
		t.Errorf("ssd1 行报价错误: %+v", ssd1)
	}
	if ssd2 == nil || ssd2.UnitPriceCNY != nil || ssd2.SubtotalCNY != nil {
		t.Errorf("ssd2 应标缺价(nil): %+v", ssd2)
	}
}

func TestComputeQuoteAllMissing(t *testing.T) {
	q := computeQuote(sampleSelection(), "", nil)
	if q.TotalCNY != "0.00" {
		t.Errorf("无价合计应为 0.00, 得到 %q", q.TotalCNY)
	}
	// 9 行但 SKU 去重:cpu1/gpu1/mb1/mem1/ssd1/ssd2/psu1/case1/cooler1 = 9 唯一
	if q.MissingCount != 9 {
		t.Errorf("全缺价 MissingCount = %d, 期望 9", q.MissingCount)
	}
}

func TestEvaluateHappyPath(t *testing.T) {
	f := fakeData{
		resolved: schemas.ResolvedBuild{
			BuildRef: "b1",
			SSDs:     []schemas.ResolvedSSD{{Quantity: 2}},
		},
		snap: store.Snapshot{ID: 7, SnapshotDate: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)},
		prices: []store.Price{
			{SKU: "cpu1", PriceCNY: "1000.00"},
		},
	}
	res, err := New(f).Evaluate(context.Background(), sampleSelection())
	if err != nil {
		t.Fatalf("Evaluate 意外报错: %v", err)
	}
	if res.Report.BuildRef != "b1" {
		t.Errorf("report.BuildRef = %q", res.Report.BuildRef)
	}
	if len(res.Report.Checks) != len(schemas.AllRuleIDs) {
		t.Errorf("checks 数 = %d, 期望 %d", len(res.Report.Checks), len(schemas.AllRuleIDs))
	}
	if res.Quote.SnapshotDate != "2026-07-28" {
		t.Errorf("quote 快照日期 = %q", res.Quote.SnapshotDate)
	}
	// 仅 cpu1 有价:合计 1000.00,其余缺价
	if res.Quote.TotalCNY != "1000.00" {
		t.Errorf("quote 合计 = %q, 期望 1000.00", res.Quote.TotalCNY)
	}
}

func TestEvaluateResolveErrorPropagates(t *testing.T) {
	sentinel := errors.New("未知 SKU")
	f := fakeData{resolveErr: sentinel}
	if _, err := New(f).Evaluate(context.Background(), sampleSelection()); !errors.Is(err, sentinel) {
		t.Errorf("解析错误应透传, 得到 %v", err)
	}
}

func TestEvaluateNoSnapshotGraceful(t *testing.T) {
	f := fakeData{
		resolved: schemas.ResolvedBuild{
			BuildRef: "b1",
			SSDs:     []schemas.ResolvedSSD{{Quantity: 1}},
		},
		snapErr: store.ErrSnapshotNotFound,
	}
	res, err := New(f).Evaluate(context.Background(), sampleSelection())
	if err != nil {
		t.Fatalf("无快照应优雅降级而非报错, 得到 %v", err)
	}
	if res.Quote.SnapshotDate != "" || res.Quote.TotalCNY != "0.00" {
		t.Errorf("无快照报价预期空日期+0.00, 得到 date=%q total=%q", res.Quote.SnapshotDate, res.Quote.TotalCNY)
	}
	if res.Quote.MissingCount == 0 {
		t.Errorf("无快照时应全部缺价")
	}
}
