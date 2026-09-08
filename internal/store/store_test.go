package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // goose 迁移连接的 database/sql 驱动
	"github.com/pressly/goose/v3"

	migrations "github.com/subaru-ye/pc-builder-agent/db"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// PostgreSQL 集成测试:PG_TEST_DSN 未设置时整体跳过(规则/schema 单测不受影响)。
// 本地运行(compose PG 就绪后):
//
//	$env:PG_TEST_DSN="postgres://pcbuilder:pcbuilder@localhost:15432/pcbuilder?sslmode=disable"
//	go test ./internal/store -count=1
//
// 每次运行都会新建独立临时库、从空库执行全部迁移、灌入夹具,结束后 DROP。

// setupStore 建临时库 + 全量迁移 + 夹具,返回可用 Store。
func setupStore(t *testing.T) *Store {
	t.Helper()
	baseDSN := os.Getenv("PG_TEST_DSN")
	if baseDSN == "" {
		t.Skip("PG_TEST_DSN 未设置,跳过 PostgreSQL 集成测试")
	}
	ctx := context.Background()

	dbName := fmt.Sprintf("store_test_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatalf("连接 PG_TEST_DSN 失败: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("创建临时库失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(),
			fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", dbName))
		_ = admin.Close(context.Background())
	})

	testDSN := replaceDBName(t, baseDSN, dbName)
	migrateUp(t, testDSN)
	seedFixtures(t, testDSN)

	s, err := New(ctx, testDSN)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func replaceDBName(t *testing.T, dsn, dbName string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("解析 PG_TEST_DSN 失败: %v", err)
	}
	u.Path = "/" + dbName
	return u.String()
}

// migrateUp 从空库执行全部嵌入迁移(与 cmd/migrate 同一套迁移源)。
func migrateUp(t *testing.T, dsn string) {
	t.Helper()
	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose 方言设置失败: %v", err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("打开迁移连接失败: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := goose.Up(conn, "migrations"); err != nil {
		t.Fatalf("空库全量迁移失败: %v", err)
	}
}

// 夹具:与 rules 包 validBuild 同口径的一套全 pass 零件 + 一个价格快照。
const fixtureSQL = `
INSERT INTO parts (sku, category, brand, model, specs) VALUES
('cpu-r5-7600',  'cpu',  'AMD', 'Ryzen 5 7600',
 '{"socket":"AM5","supported_chipsets":["B650","X670"],"has_igpu":true,"tdp_w":65}'),
('gpu-rtx4070',  'gpu',  'NVIDIA', 'RTX 4070',
 '{"length_mm":300,"tdp_w":220,"power_connectors":["pcie_16pin"]}'),
('mb-b650m',     'motherboard', 'MSI', 'B650M MORTAR',
 '{"socket":"AM5","chipset":"B650","memory_generation":"ddr5","memory_speed_max_mts":6000,"form_factor":"matx","m2_slots":2}'),
('ram-ddr5-6000','memory', 'Kingston', 'Fury 32G',
 '{"generation":"ddr5","speed_mts":6000}'),
('ssd-sn770',    'ssd',  'WD', 'SN770 1T',
 '{"form_factor":"m2"}'),
('psu-750w',     'psu',  'Corsair', 'RM750e',
 '{"wattage_w":750,"power_connectors":["pcie_8pin","pcie_8pin","pcie_16pin"]}'),
('case-air',     'case', 'Fractal', 'Pop Air',
 '{"gpu_length_max_mm":300,"cooler_height_max_mm":160,"supported_form_factors":["atx","matx","itx"],"radiator_sizes_mm":[240,360]}'),
('cooler-ak620', 'cooler', 'DeepCool', 'AK620',
 '{"type":"air","height_mm":155,"cooling_capacity_w":220}'),
('cpu-inactive', 'cpu',  'AMD', 'Ryzen 5 5600(停售)',
 '{"socket":"AM4","supported_chipsets":["B550"],"has_igpu":false,"tdp_w":65}');

UPDATE parts SET active = FALSE WHERE sku = 'cpu-inactive';

INSERT INTO price_snapshots (snapshot_date, file_sha256)
VALUES ('2026-07-27', 'deadbeef');

INSERT INTO prices (snapshot_id, sku, price_cny, source)
SELECT id, 'cpu-r5-7600', 1299.00, 'jd' FROM price_snapshots WHERE snapshot_date = '2026-07-27';
INSERT INTO prices (snapshot_id, sku, price_cny, source)
SELECT id, 'gpu-rtx4070', 4599.00, 'jd' FROM price_snapshots WHERE snapshot_date = '2026-07-27';
`

func seedFixtures(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接临时库失败: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, fixtureSQL); err != nil {
		t.Fatalf("灌入夹具失败: %v", err)
	}
}

// validSelection 与夹具对应的合法选择。
func validSelection() schemas.BuildSelection {
	gpu := "gpu-rtx4070"
	return schemas.BuildSelection{
		SchemaVersion: 1,
		BuildRef:      "build_store_test",
		CPU:           "cpu-r5-7600",
		Motherboard:   "mb-b650m",
		Memory:        "ram-ddr5-6000",
		SSDs:          []schemas.SSDSelection{{SKU: "ssd-sn770", Quantity: 1}},
		GPU:           &gpu,
		PSU:           "psu-750w",
		Case:          "case-air",
		Cooler:        "cooler-ak620",
	}
}

func TestResolveBuild(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	t.Run("完整展开", func(t *testing.T) {
		got, err := s.ResolveBuild(ctx, validSelection())
		if err != nil {
			t.Fatalf("ResolveBuild 失败: %v", err)
		}
		if got.BuildRef != "build_store_test" {
			t.Errorf("BuildRef = %q", got.BuildRef)
		}
		if got.CPU.Socket == nil || *got.CPU.Socket != "AM5" {
			t.Errorf("cpu.socket = %v, want AM5", got.CPU.Socket)
		}
		if got.GPU == nil || got.GPU.LengthMM == nil || *got.GPU.LengthMM != 300 {
			t.Errorf("gpu.length_mm 展开不正确: %+v", got.GPU)
		}
		if got.Motherboard.M2Slots == nil || *got.Motherboard.M2Slots != 2 {
			t.Errorf("motherboard.m2_slots 展开不正确: %+v", got.Motherboard)
		}
		if len(got.SSDs) != 1 || got.SSDs[0].Quantity != 1 ||
			got.SSDs[0].Spec.FormFactor == nil || *got.SSDs[0].Spec.FormFactor != schemas.SSDFormFactorM2 {
			t.Errorf("ssd 展开不正确: %+v", got.SSDs)
		}
		if len(got.Case.RadiatorSizesMM) != 2 || got.Case.RadiatorSizesMM[1] != 360 {
			t.Errorf("case.radiator_sizes_mm 展开不正确: %v", got.Case.RadiatorSizesMM)
		}
		if got.Cooler.Type == nil || *got.Cooler.Type != schemas.CoolerTypeAir {
			t.Errorf("cooler.type 展开不正确: %+v", got.Cooler)
		}
	})

	t.Run("gpu null 展开为 nil", func(t *testing.T) {
		sel := validSelection()
		sel.GPU = nil
		got, err := s.ResolveBuild(ctx, sel)
		if err != nil {
			t.Fatalf("ResolveBuild 失败: %v", err)
		}
		if got.GPU != nil {
			t.Errorf("gpu 应为 nil,得到 %+v", got.GPU)
		}
	})

	t.Run("未知 SKU 返回 ErrUnknownSKU", func(t *testing.T) {
		sel := validSelection()
		sel.CPU = "cpu-not-exist"
		_, err := s.ResolveBuild(ctx, sel)
		if !errors.Is(err, ErrUnknownSKU) {
			t.Errorf("应为 ErrUnknownSKU,得到 %v", err)
		}
		if err == nil || !strings.Contains(err.Error(), "cpu-not-exist") {
			t.Errorf("错误信息应包含具体 SKU,得到 %v", err)
		}
	})

	t.Run("停用 SKU 返回 ErrUnknownSKU", func(t *testing.T) {
		sel := validSelection()
		sel.CPU = "cpu-inactive"
		_, err := s.ResolveBuild(ctx, sel)
		if !errors.Is(err, ErrUnknownSKU) {
			t.Errorf("应为 ErrUnknownSKU,得到 %v", err)
		}
	})

	t.Run("类目不符报错", func(t *testing.T) {
		sel := validSelection()
		sel.CPU = "cooler-ak620" // cooler SKU 放进 cpu 位置
		_, err := s.ResolveBuild(ctx, sel)
		if err == nil || !strings.Contains(err.Error(), "类目") {
			t.Errorf("应报类目错误,得到 %v", err)
		}
	})
}

func TestPriceSnapshots(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	date := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)

	t.Run("按日期取快照并读价格", func(t *testing.T) {
		snap, err := s.SnapshotByDate(ctx, date)
		if err != nil {
			t.Fatalf("SnapshotByDate 失败: %v", err)
		}
		if snap.FileSHA256 != "deadbeef" {
			t.Errorf("file_sha256 = %q", snap.FileSHA256)
		}
		prices, err := s.PricesBySnapshot(ctx, snap.ID)
		if err != nil {
			t.Fatalf("PricesBySnapshot 失败: %v", err)
		}
		if len(prices) != 2 {
			t.Fatalf("应有 2 条价格,得到 %d", len(prices))
		}
		// 按 SKU 升序:cpu-r5-7600 在前。
		if prices[0].SKU != "cpu-r5-7600" || prices[0].PriceCNY != "1299.00" || prices[0].Source != "jd" {
			t.Errorf("价格行不正确: %+v", prices[0])
		}
	})

	t.Run("最新快照", func(t *testing.T) {
		snap, err := s.LatestSnapshot(ctx)
		if err != nil {
			t.Fatalf("LatestSnapshot 失败: %v", err)
		}
		if !snap.SnapshotDate.Equal(date) {
			t.Errorf("snapshot_date = %v, want %v", snap.SnapshotDate, date)
		}
	})

	t.Run("无快照日期返回 ErrSnapshotNotFound", func(t *testing.T) {
		_, err := s.SnapshotByDate(ctx, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		if !errors.Is(err, ErrSnapshotNotFound) {
			t.Errorf("应为 ErrSnapshotNotFound,得到 %v", err)
		}
	})
}

// candidateExtraSQL 在共享夹具基础上补一批 AM5 CPU(含一个无价件与一个异平台/异品牌件),
// 专用于 Candidates 的过滤/排序/截断/价格降级场景。价格入 2026-07-27 快照。
const candidateExtraSQL = `
INSERT INTO parts (sku, category, brand, model, specs) VALUES
('cpu-am5-cheap','cpu','AMD','R5 cheap',
 '{"socket":"AM5","supported_chipsets":["B650"],"has_igpu":true,"tdp_w":65}'),
('cpu-am5-mid','cpu','AMD','R7 mid',
 '{"socket":"AM5","supported_chipsets":["B650"],"has_igpu":false,"tdp_w":105}'),
('cpu-am5-high','cpu','AMD','R9 high',
 '{"socket":"AM5","supported_chipsets":["X670"],"has_igpu":false,"tdp_w":170}'),
('cpu-am5-nopr','cpu','AMD','R5 noprice',
 '{"socket":"AM5","supported_chipsets":["B650"],"has_igpu":true,"tdp_w":65}'),
('cpu-intel','cpu','Intel','i5-14600K',
 '{"socket":"LGA1700","supported_chipsets":["B760"],"has_igpu":true,"tdp_w":125}');

INSERT INTO prices (snapshot_id, sku, price_cny, source)
SELECT id, v.sku, v.price, 'jd' FROM price_snapshots, (VALUES
 ('cpu-am5-cheap', 800.00), ('cpu-am5-mid', 1500.00),
 ('cpu-am5-high', 2500.00), ('cpu-intel', 1000.00)
) AS v(sku, price) WHERE snapshot_date = '2026-07-27';
`

func TestCandidates(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, candidateExtraSQL); err != nil {
		t.Fatalf("灵入候选夹具失败: %v", err)
	}
	// 活跃 AM5 CPU 按价升序(NULLS LAST):cheap 800 / 7600 1299 / mid 1500 / high 2500 / nopr 无价。
	skus := func(r CandidateResult) []string {
		out := make([]string, len(r.Candidates))
		for i, c := range r.Candidates {
			out[i] = c.SKU
		}
		return out
	}

	t.Run("socket 过滤 + 价升序 + 无价排末", func(t *testing.T) {
		r, err := s.Candidates(ctx, CandidateQuery{Category: schemas.CategoryCPU, Socket: "AM5", TopN: 10})
		if err != nil {
			t.Fatalf("Candidates 失败: %v", err)
		}
		want := []string{"cpu-am5-cheap", "cpu-r5-7600", "cpu-am5-mid", "cpu-am5-high", "cpu-am5-nopr"}
		if !reflect.DeepEqual(skus(r), want) {
			t.Errorf("顺序错误: got %v want %v", skus(r), want)
		}
		if r.Truncated != 0 {
			t.Errorf("Truncated = %d, want 0", r.Truncated)
		}
		if r.SnapshotDate == nil || !r.SnapshotDate.Equal(time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("SnapshotDate = %v, want 2026-07-27", r.SnapshotDate)
		}
		if r.Candidates[0].PriceCNY == nil || *r.Candidates[0].PriceCNY != "800.00" {
			t.Errorf("首件价 = %v, want 800.00", r.Candidates[0].PriceCNY)
		}
		last := r.Candidates[len(r.Candidates)-1]
		if last.SKU != "cpu-am5-nopr" || last.PriceCNY != nil {
			t.Errorf("无价件应排末且价为 nil: %+v", last)
		}
	})

	t.Run("top_n 截断如实回报", func(t *testing.T) {
		r, err := s.Candidates(ctx, CandidateQuery{Category: schemas.CategoryCPU, Socket: "AM5", TopN: 2})
		if err != nil {
			t.Fatalf("Candidates 失败: %v", err)
		}
		if !reflect.DeepEqual(skus(r), []string{"cpu-am5-cheap", "cpu-r5-7600"}) {
			t.Errorf("前两条错误: %v", skus(r))
		}
		if r.Truncated != 3 {
			t.Errorf("Truncated = %d, want 3(共 5 取 2)", r.Truncated)
		}
	})

	t.Run("price_max 剔除无价与超价", func(t *testing.T) {
		max := 1400
		r, err := s.Candidates(ctx, CandidateQuery{Category: schemas.CategoryCPU, Socket: "AM5", PriceMaxCNY: &max, TopN: 10})
		if err != nil {
			t.Fatalf("Candidates 失败: %v", err)
		}
		if !reflect.DeepEqual(skus(r), []string{"cpu-am5-cheap", "cpu-r5-7600"}) {
			t.Errorf("price_max=1400 应仅余两件(无价件被剔): %v", skus(r))
		}
		if r.Truncated != 0 {
			t.Errorf("Truncated = %d, want 0", r.Truncated)
		}
	})

	t.Run("brand 不区分大小写", func(t *testing.T) {
		r, err := s.Candidates(ctx, CandidateQuery{Category: schemas.CategoryCPU, Brand: "intel", TopN: 10})
		if err != nil {
			t.Fatalf("Candidates 失败: %v", err)
		}
		if !reflect.DeepEqual(skus(r), []string{"cpu-intel"}) {
			t.Errorf("brand=intel 应仅命中 Intel 件: %v", skus(r))
		}
	})

	t.Run("case form_factor 数组包含", func(t *testing.T) {
		r, err := s.Candidates(ctx, CandidateQuery{Category: schemas.CategoryCase, FormFactor: "itx", TopN: 10})
		if err != nil {
			t.Fatalf("Candidates 失败: %v", err)
		}
		if !reflect.DeepEqual(skus(r), []string{"case-air"}) {
			t.Errorf("case 支持 itx 应命中 case-air: %v", skus(r))
		}
	})

	t.Run("非法查询返回 ErrInvalidQuery", func(t *testing.T) {
		bad := []CandidateQuery{
			{Category: schemas.CategoryGPU, Socket: "AM5", TopN: 5},     // socket 不适用于 gpu
			{Category: schemas.CategoryCPU, TopN: 0},                    // top_n 非正
			{Category: "widget", TopN: 5},                               // 非法品类
			{Category: schemas.CategoryCPU, FormFactor: "atx", TopN: 5}, // form_factor 不适用于 cpu
		}
		for _, q := range bad {
			if _, err := s.Candidates(ctx, q); !errors.Is(err, ErrInvalidQuery) {
				t.Errorf("%+v 应为 ErrInvalidQuery,得到 %v", q, err)
			}
		}
	})
}

func TestActiveCatalogSnapshot(t *testing.T) {
	s := setupStore(t)
	result, err := s.ActiveCatalogSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.SnapshotDate.Format("2006-01-02") != "2026-07-27" {
		t.Fatalf("snapshot=%s", result.Snapshot.SnapshotDate)
	}
	for _, candidate := range result.Candidates {
		if candidate.SKU == "cpu-inactive" {
			t.Fatal("inactive SKU 不得进入 Harness 候选快照")
		}
	}
	if len(result.Candidates) != 8 {
		t.Fatalf("active_core 候选数=%d, want 8", len(result.Candidates))
	}
}

func TestCatalogSnapshotByDate(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	date, err := time.Parse("2006-01-02", "2026-07-27")
	if err != nil {
		t.Fatal(err)
	}
	byDate, err := s.CatalogSnapshotByDate(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.ActiveCatalogSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byDate.Snapshot.ID != latest.Snapshot.ID || len(byDate.Candidates) != len(latest.Candidates) {
		t.Fatalf("按日期批次与最新批次不一致: byDate=%d/%d条 latest=%d/%d条",
			byDate.Snapshot.ID, len(byDate.Candidates), latest.Snapshot.ID, len(latest.Candidates))
	}
	priced := 0
	for _, candidate := range byDate.Candidates {
		if candidate.SKU == "cpu-inactive" {
			t.Fatal("inactive SKU 不得进入按日期候选快照")
		}
		if candidate.PriceCNY != nil {
			priced++
		}
	}
	if priced != 2 {
		t.Fatalf("该批次有价候选=%d, want 2(仅夹具录入的两条价格)", priced)
	}

	missing, _ := time.Parse("2006-01-02", "2026-07-28")
	if _, err := s.CatalogSnapshotByDate(ctx, missing); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("无该日期批次应返回 ErrSnapshotNotFound,得到 %v", err)
	}
}
