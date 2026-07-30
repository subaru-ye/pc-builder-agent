package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // goose 迁移连接的 database/sql 驱动
	"github.com/pressly/goose/v3"

	migrations "github.com/subaru-ye/pc-builder-agent/db"
)

// PostgreSQL 集成测试:PG_TEST_DSN 未设置时整体跳过(与 internal/store 同约定)。
// 本地运行(compose PG 就绪后):
//
//	$env:PG_TEST_DSN="postgres://pcbuilder:pcbuilder@localhost:15432/pcbuilder?sslmode=disable"
//	go test ./cmd/importprices -count=1

// setupConn 建临时库 + 全量迁移 + 两个零件夹具,返回连接。
func setupConn(t *testing.T) *pgx.Conn {
	t.Helper()
	baseDSN := os.Getenv("PG_TEST_DSN")
	if baseDSN == "" {
		t.Skip("PG_TEST_DSN 未设置,跳过 PostgreSQL 集成测试")
	}
	ctx := context.Background()

	dbName := fmt.Sprintf("importprices_test_%d", time.Now().UnixNano())
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

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("解析 PG_TEST_DSN 失败: %v", err)
	}
	u.Path = "/" + dbName
	testDSN := u.String()

	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose 方言设置失败: %v", err)
	}
	mconn, err := sql.Open("pgx", testDSN)
	if err != nil {
		t.Fatalf("打开迁移连接失败: %v", err)
	}
	if err := goose.Up(mconn, "migrations"); err != nil {
		_ = mconn.Close()
		t.Fatalf("空库全量迁移失败: %v", err)
	}
	_ = mconn.Close()

	conn, err := pgx.Connect(ctx, testDSN)
	if err != nil {
		t.Fatalf("连接临时库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	// prices.sku 外键指向 parts,先灌两个最小零件。
	if _, err := conn.Exec(ctx, `
		INSERT INTO parts (sku, category, brand, model, specs) VALUES
		('cpu-a', 'cpu', 'AMD', 'A', '{"socket":"AM5","supported_chipsets":["B650"],"has_igpu":true,"tdp_w":65}'),
		('gpu-b', 'gpu', 'NV',  'B', '{"length_mm":300,"tdp_w":220,"power_connectors":["pcie_16pin"]}')`); err != nil {
		t.Fatalf("灌入零件夹具失败: %v", err)
	}
	return conn
}

// writeCSV 写临时价格 CSV,返回路径。
func writeCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prices.csv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写临时 CSV 失败: %v", err)
	}
	return path
}

const goodCSV = `sku,price_cny,source,captured_at
cpu-a,1299.00,jd,2026-07-28
gpu-b,4599.00,jd,2026-07-28
`

func TestImportAndIdempotentSkip(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()

	batch, err := loadPriceCSV(writeCSV(t, goodCSV))
	if err != nil {
		t.Fatalf("loadPriceCSV 失败: %v", err)
	}
	if len(batch.Rows) != 2 || batch.SnapshotDate.Format("2006-01-02") != "2026-07-28" {
		t.Fatalf("批次解析不正确: %+v", batch)
	}

	skipped, err := importPrices(ctx, conn, batch)
	if err != nil || skipped {
		t.Fatalf("首次导入应成功且不跳过: skipped=%v err=%v", skipped, err)
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM prices").Scan(&n); err != nil || n != 2 {
		t.Fatalf("应有 2 条价格,得到 %d(err=%v)", n, err)
	}
	var price string
	if err := conn.QueryRow(ctx,
		"SELECT price_cny::text FROM prices WHERE sku = 'cpu-a'").Scan(&price); err != nil || price != "1299.00" {
		t.Fatalf("price_cny 应为 1299.00,得到 %q(err=%v)", price, err)
	}

	// 同 SHA256 重跑:幂等跳过,行数与批次数不变。
	skipped, err = importPrices(ctx, conn, batch)
	if err != nil || !skipped {
		t.Fatalf("重跑应幂等跳过: skipped=%v err=%v", skipped, err)
	}
	var snaps int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM price_snapshots").Scan(&snaps); err != nil || snaps != 1 {
		t.Fatalf("应仍为 1 个快照,得到 %d(err=%v)", snaps, err)
	}
}

func TestRollbackOnUnknownSKU(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()

	csv := `sku,price_cny,source,captured_at
cpu-a,1299.00,jd,2026-07-28
sku-not-exist,99.00,jd,2026-07-28
`
	batch, err := loadPriceCSV(writeCSV(t, csv))
	if err != nil {
		t.Fatalf("loadPriceCSV 失败: %v", err)
	}
	_, err = importPrices(ctx, conn, batch)
	if err == nil || !strings.Contains(err.Error(), "sku-not-exist") {
		t.Fatalf("应报 sku-not-exist 外键失败,得到 %v", err)
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM prices").Scan(&n); err != nil || n != 0 {
		t.Fatalf("整批应回滚为 0 条价格,得到 %d(err=%v)", n, err)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM price_snapshots").Scan(&n); err != nil || n != 0 {
		t.Fatalf("快照批次也应回滚,得到 %d(err=%v)", n, err)
	}
}

func TestLoadPriceCSVRejects(t *testing.T) {
	cases := []struct {
		name, csv, wantErr string
	}{
		{"表头错误", "sku,price,source,captured_at\ncpu-a,1,jd,2026-07-28\n", "表头必须为"},
		{"价格非法", "sku,price_cny,source,captured_at\ncpu-a,-5,jd,2026-07-28\n", "price_cny"},
		{"价格三位小数", "sku,price_cny,source,captured_at\ncpu-a,1.999,jd,2026-07-28\n", "price_cny"},
		{"日期非法", "sku,price_cny,source,captured_at\ncpu-a,1.00,jd,2026/07/28\n", "captured_at"},
		{"跨日混批", "sku,price_cny,source,captured_at\ncpu-a,1.00,jd,2026-07-28\ngpu-b,2.00,jd,2026-07-29\n", "不一致"},
		{"SKU 重复", "sku,price_cny,source,captured_at\ncpu-a,1.00,jd,2026-07-28\ncpu-a,2.00,jd,2026-07-28\n", "重复"},
		{"无数据行", "sku,price_cny,source,captured_at\n", "无数据行"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadPriceCSV(writeCSV(t, tc.csv))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("应报 %q,得到 %v", tc.wantErr, err)
			}
		})
	}
}
