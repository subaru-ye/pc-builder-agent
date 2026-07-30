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
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// PostgreSQL 集成测试:PG_TEST_DSN 未设置时整体跳过(与 internal/store 同约定)。
// 本地运行(compose PG 就绪后):
//
//	$env:PG_TEST_DSN="postgres://pcbuilder:pcbuilder@localhost:15432/pcbuilder?sslmode=disable"
//	go test ./cmd/importparts -count=1

// partsDir 仓库内真实产物目录(D4 提交的 8×20 SKU)。
func partsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "scripts", "data", "parts"))
	if err != nil {
		t.Fatalf("解析产物目录失败: %v", err)
	}
	return dir
}

// setupConn 建临时库 + 全量迁移,返回连接。
func setupConn(t *testing.T) *pgx.Conn {
	t.Helper()
	baseDSN := os.Getenv("PG_TEST_DSN")
	if baseDSN == "" {
		t.Skip("PG_TEST_DSN 未设置,跳过 PostgreSQL 集成测试")
	}
	ctx := context.Background()

	dbName := fmt.Sprintf("importparts_test_%d", time.Now().UnixNano())
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
	return conn
}

func countParts(t *testing.T, conn *pgx.Conn) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(context.Background(),
		"SELECT count(*) FROM parts").Scan(&n); err != nil {
		t.Fatalf("统计 parts 行数失败: %v", err)
	}
	return n
}

// TestImportRealCatalog 导入仓库真实产物:160 行、每类 20 行、重跑幂等。
func TestImportRealCatalog(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()

	parts, err := loadParts(partsDir(t))
	if err != nil {
		t.Fatalf("loadParts 失败: %v", err)
	}
	if len(parts) != 160 {
		t.Fatalf("产物应为 160 条,得到 %d", len(parts))
	}
	if err := importParts(ctx, conn, parts); err != nil {
		t.Fatalf("importParts 失败: %v", err)
	}
	if n := countParts(t, conn); n != 160 {
		t.Fatalf("导入后应为 160 行,得到 %d", n)
	}
	var perCat int
	for _, cat := range schemas.AllCategories {
		if err := conn.QueryRow(ctx,
			"SELECT count(*) FROM parts WHERE category = $1", string(cat)).Scan(&perCat); err != nil {
			t.Fatalf("统计 %s 行数失败: %v", cat, err)
		}
		if perCat != 20 {
			t.Errorf("类目 %s 应为 20 行,得到 %d", cat, perCat)
		}
	}

	// 重跑同一批:行数不变,updated_at 前移但内容一致(upsert 幂等)。
	if err := importParts(ctx, conn, parts); err != nil {
		t.Fatalf("重跑 importParts 失败: %v", err)
	}
	if n := countParts(t, conn); n != 160 {
		t.Fatalf("重跑后应仍为 160 行,得到 %d", n)
	}
	var distinct int
	if err := conn.QueryRow(ctx,
		"SELECT count(DISTINCT sku) FROM parts").Scan(&distinct); err != nil {
		t.Fatalf("统计去重 SKU 失败: %v", err)
	}
	if distinct != 160 {
		t.Fatalf("重跑后去重 SKU 应为 160,得到 %d", distinct)
	}
}

// TestImportRollbackOnBadRecord 批内混入 DB 层非法记录(类目违反 CHECK 约束,
// 绕过 loadParts 直接调 importParts):整批回滚,前面已 upsert 的合法行不落库。
func TestImportRollbackOnBadRecord(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()

	good := partRecord{
		SKU: "cpu-good", Category: schemas.CategoryCPU, Brand: "AMD", Model: "R5 7600",
		SchemaVersion: 1,
		Specs:         []byte(`{"socket":"AM5","supported_chipsets":["B650"],"has_igpu":true,"tdp_w":65}`),
		SourceMeta:    []byte(`{}`),
	}
	bad := good
	bad.SKU = "cpu-bad"
	bad.Category = schemas.Category("weird") // 违反 parts.category CHECK

	err := importParts(ctx, conn, []partRecord{good, bad})
	if err == nil || !strings.Contains(err.Error(), "cpu-bad") {
		t.Fatalf("应报 cpu-bad 写入失败,得到 %v", err)
	}
	if n := countParts(t, conn); n != 0 {
		t.Fatalf("整批应回滚为 0 行,得到 %d", n)
	}
}

// TestLoadRejectsBadSpecs 载入阶段拦截非法 specs:未知字段立即失败,不触库。
func TestLoadRejectsBadSpecs(t *testing.T) {
	dir := t.TempDir()
	src := partsDir(t)
	for _, cat := range schemas.AllCategories {
		data, err := os.ReadFile(filepath.Join(src, string(cat)+".jsonl"))
		if err != nil {
			t.Fatalf("读取产物失败: %v", err)
		}
		if cat == schemas.CategoryCPU {
			data = append(data,
				[]byte(`{"sku":"cpu-x","category":"cpu","brand":"b","model":"m","schema_version":1,"specs":{"socket":"AM5","supported_chipsets":[],"has_igpu":true,"tdp_w":65,"bogus":1},"source_meta":{}}`+"\n")...)
		}
		if err := os.WriteFile(filepath.Join(dir, string(cat)+".jsonl"), data, 0o644); err != nil {
			t.Fatalf("写临时产物失败: %v", err)
		}
	}
	if _, err := loadParts(dir); err == nil || !strings.Contains(err.Error(), "cpu-x") {
		t.Fatalf("应报 cpu-x specs 非法,得到 %v", err)
	}
}
