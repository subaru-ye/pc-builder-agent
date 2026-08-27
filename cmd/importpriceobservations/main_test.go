package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	migrations "github.com/subaru-ye/pc-builder-agent/db"
)

func setupPriceConn(t *testing.T) *pgx.Conn {
	t.Helper()
	baseDSN := os.Getenv("PG_TEST_DSN")
	if baseDSN == "" {
		t.Skip("PG_TEST_DSN 未设置，跳过 PostgreSQL 集成测试")
	}
	ctx := context.Background()
	database := fmt.Sprintf("priceobs_test_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatalf("连接测试数据库失败: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		t.Fatalf("创建临时数据库失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", database))
		_ = admin.Close(context.Background())
	})
	parsed, _ := url.Parse(baseDSN)
	parsed.Path = "/" + database
	dsn := parsed.String()
	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	_ = db.Close()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	_, err = conn.Exec(ctx, `INSERT INTO parts (sku, category, brand, model, specs) VALUES
		('cpu-a','cpu','AMD','A','{"socket":"AM5","supported_chipsets":["B650"],"has_igpu":true,"tdp_w":65}'),
		('gpu-b','gpu','AMD','B','{"length_mm":280,"tdp_w":220,"power_connectors":["pcie_8pin"]}')`)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func priceFixture(day string) (priceManifest, []observation, []selection) {
	releaseID, runID := uuid.NewString(), uuid.NewString()
	created := time.Now().UTC().Format(time.RFC3339Nano)
	manifest := priceManifest{SchemaVersion: 1, ReleaseID: releaseID, RunID: runID, SnapshotDate: day,
		CreatedAt: created, Policy: "manual", InputSHA256: fmt.Sprintf("%064d", 1),
		ObservationsSHA: fmt.Sprintf("%064d", 2), SelectionSHA: fmt.Sprintf("%064d", 3),
		ReviewSHA: fmt.Sprintf("%064d", 4), ManifestSHA256: fmt.Sprintf("%064d", 5), Stats: json.RawMessage(`{"selected":2}`)}
	observations := []observation{{SchemaVersion: 1, ID: fmt.Sprintf("%064d", 6), SKU: "cpu-a",
		PriceCNY: "1299.00", Currency: "CNY", SourceID: "manual", ProductID: "p1",
		SourceURL: "https://example.com/p1", Seller: "seller", PriceType: "regular",
		StockStatus: "in_stock", VariantMatch: "exact", ObservedAt: created,
		RawSHA256: fmt.Sprintf("%064d", 7), DecisionStatus: "qualified", RejectionReasons: []string{}}}
	selections := []selection{
		{SchemaVersion: 1, SKU: "cpu-a", ObservationID: &observations[0].ID, PriceCNY: "1299.00", SourceID: "manual", ObservedAt: created, PriceType: "regular"},
		{SchemaVersion: 1, SKU: "gpu-b", PriceCNY: "4599.00", SourceID: "legacy:jd", ObservedAt: "2026-07-28T00:00:00Z", PriceType: "bootstrap", CarriedForward: true},
	}
	return manifest, observations, selections
}

func TestImportReleaseAtomicAndIdempotent(t *testing.T) {
	conn := setupPriceConn(t)
	ctx := context.Background()
	manifest, observations, selections := priceFixture("2026-08-27")
	skipped, err := importRelease(ctx, conn, "", manifest, observations, selections)
	if err != nil || skipped {
		t.Fatalf("首次导入失败: skipped=%v err=%v", skipped, err)
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM prices`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("prices=%d err=%v", count, err)
	}
	var observedAt *time.Time
	var kind string
	var basis string
	if err := conn.QueryRow(ctx, `SELECT observed_at, price_type, availability_basis FROM prices WHERE sku='cpu-a'`).Scan(&observedAt, &kind, &basis); err != nil || observedAt == nil || kind != "regular" || basis != "unknown" {
		t.Fatalf("价格元数据未保存: observed=%v kind=%q basis=%q err=%v", observedAt, kind, basis, err)
	}
	skipped, err = importRelease(ctx, conn, "", manifest, observations, selections)
	if err != nil || !skipped {
		t.Fatalf("重复导入应幂等: skipped=%v err=%v", skipped, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE price_observations SET seller='changed' WHERE observation_id=$1`, observations[0].ID); err == nil {
		t.Fatal("不可变 observation 不应允许更新")
	}
}

func TestListingObservationRequiresSearchBasis(t *testing.T) {
	row := observation{SchemaVersion: 1, ID: fmt.Sprintf("%064d", 1), SKU: "cpu-a",
		PriceCNY: "1299.00", Currency: "CNY", SourceID: "serpapi_baidu:shop", CollectorID: "serpapi_baidu",
		ProductID: "p", SourceURL: "https://example.com/p", Seller: "shop", PriceType: "listing",
		AvailabilityBasis: "search_listing", StockStatus: "unknown", VariantMatch: "exact",
		ObservedAt: time.Now().UTC().Format(time.RFC3339), RawSHA256: fmt.Sprintf("%064d", 2),
		DecisionStatus: "qualified", RejectionReasons: []string{}}
	if err := validateObservation(row); err != nil {
		t.Fatalf("合法 listing 被拒绝: %v", err)
	}
	row.StockStatus = "in_stock"
	if err := validateObservation(row); err == nil {
		t.Fatal("搜索报价不得宣称库存")
	}
}

func TestImportReleaseRollbackOnUnknownSKU(t *testing.T) {
	conn := setupPriceConn(t)
	ctx := context.Background()
	manifest, observations, selections := priceFixture("2026-08-28")
	selections[1].SKU = "unknown"
	if _, err := importRelease(ctx, conn, "", manifest, observations, selections); err == nil {
		t.Fatal("未知 SKU 应导致整批失败")
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM price_snapshots`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("失败后快照应为 0，得到 %d err=%v", count, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM price_observations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("失败后 observation 应为 0，得到 %d err=%v", count, err)
	}
}
