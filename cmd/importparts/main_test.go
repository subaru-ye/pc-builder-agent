package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
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

func writeReleaseManifest(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	value := map[string]any{
		"schema_version":      1,
		"release_id":          "45101c4f-2291-5d51-9a2d-1f5d8ef0fa01",
		"previous_release_id": nil,
		"run_id":              "7ef69660-9c17-5c60-b184-51c034db59db",
		"created_at":          "2026-08-24T00:00:00Z",
		"decision":            "bootstrap",
		"input_sha256":        strings.Repeat("a", 64),
		"files": []map[string]any{
			{"path": "parts/cpu.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
			{"path": "parts/gpu.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
			{"path": "parts/motherboard.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
			{"path": "parts/memory.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
			{"path": "parts/ssd.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
			{"path": "parts/psu.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
			{"path": "parts/case.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
			{"path": "parts/cooler.jsonl", "sha256": strings.Repeat("a", 64), "bytes": 0},
		},
		"stats":           map[string]any{"total_skus": 160},
		"manifest_sha256": "",
	}
	canonical, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	canonical = append(canonical, '\n')
	value["manifest_sha256"] = fmt.Sprintf("%x", sha256.Sum256(canonical))
	if mutate != nil {
		mutate(value)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadReleaseManifest(t *testing.T) {
	manifest, err := loadReleaseManifest(writeReleaseManifest(t, nil))
	if err != nil {
		t.Fatalf("合法 manifest 应通过: %v", err)
	}
	if manifest.Decision != "bootstrap" || manifest.ReleaseID == "" {
		t.Fatalf("manifest 解码不正确: %+v", manifest)
	}

	_, err = loadReleaseManifest(writeReleaseManifest(t, func(value map[string]any) {
		value["input_sha256"] = strings.Repeat("b", 64)
	}))
	if err == nil || !strings.Contains(err.Error(), "manifest_sha256 不匹配") {
		t.Fatalf("篡改后应被哈希拦截,得到 %v", err)
	}
}

func TestSpecFingerprintStableAcrossJSONKeyOrder(t *testing.T) {
	a := partRecord{Specs: []byte(`{"socket":"AM5","tdp_w":65}`), SourceMeta: []byte(`{"socket":"manual","tdp_w":"pcpart"}`)}
	b := partRecord{Specs: []byte(`{"tdp_w":65,"socket":"AM5"}`), SourceMeta: []byte(`{"tdp_w":"pcpart","socket":"manual"}`)}
	fa, err := specFingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := specFingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	if fa != fb || !sha256Pattern.MatchString(fa) {
		t.Fatalf("语义相同 JSON 指纹应一致: %q != %q", fa, fb)
	}
}

func testEvidence(t *testing.T, sku string) evidenceRecord {
	t.Helper()
	record := evidenceRecord{
		SchemaVersion: 1, SKU: sku, Field: "specs.socket", Value: []byte(`"AM5"`),
		SourceID: "amd_products", SourceURL: "https://www.amd.com/en/products/example.html",
		CapturedAt: "2026-08-24T00:00:00Z", RawSHA256: strings.Repeat("b", 64),
		Method: "deterministic", EvidenceExcerpt: "CPU Socket: AM5", EvidenceStatus: "verified",
	}
	var value any
	if err := json.Unmarshal(record.Value, &value); err != nil {
		t.Fatal(err)
	}
	identity, err := json.Marshal(map[string]any{
		"source_id": record.SourceID, "sku": record.SKU, "field": record.Field,
		"value": value, "raw_sha256": record.RawSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	record.ID = fmt.Sprintf("%x", sha256.Sum256(identity))
	return record
}

func TestCatalogStateAndEvidenceValidation(t *testing.T) {
	valid := partRecord{CatalogState: "catalog_only"}
	if got := catalogState(valid); got != "catalog_only" {
		t.Fatalf("catalog_state 未保留: %q", got)
	}
	if got := catalogState(partRecord{}); got != "active_core" {
		t.Fatalf("历史记录应兼容 active_core: %q", got)
	}
	evidence := testEvidence(t, "cpu-r5-7600")
	if err := validateEvidence(evidence); err != nil {
		t.Fatalf("合法 evidence 应通过: %v", err)
	}
	evidence.SourceURL = "https://user:pass@www.amd.com/example"
	if err := validateEvidence(evidence); err == nil {
		t.Fatal("含凭据 URL 必须拒绝")
	}
}

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

func TestImportRealCatalogWithP11Release(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()
	parts, err := loadParts(partsDir(t))
	if err != nil {
		t.Fatal(err)
	}
	release := &releaseManifest{
		SchemaVersion:  1,
		ReleaseID:      "45101c4f-2291-5d51-9a2d-1f5d8ef0fa01",
		RunID:          "7ef69660-9c17-5c60-b184-51c034db59db",
		Decision:       "bootstrap",
		InputSHA256:    strings.Repeat("a", 64),
		Stats:          []byte(`{"total_skus":160}`),
		ManifestSHA256: strings.Repeat("b", 64),
	}
	if err := importPartsWithRelease(ctx, conn, parts, release); err != nil {
		t.Fatalf("P11 release 导入失败: %v", err)
	}
	var activeCore, fingerprinted, publications int
	if err := conn.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE catalog_state = 'active_core'),
			count(*) FILTER (WHERE spec_fingerprint IS NOT NULL)
		FROM parts`).Scan(&activeCore, &fingerprinted); err != nil {
		t.Fatal(err)
	}
	if activeCore != 160 || fingerprinted != 160 {
		t.Fatalf("P11 目录状态/指纹不完整: active_core=%d fingerprinted=%d", activeCore, fingerprinted)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM data_publications").Scan(&publications); err != nil {
		t.Fatal(err)
	}
	if publications != 1 {
		t.Fatalf("应记录 1 个 publication,得到 %d", publications)
	}
	// 同 release 重试幂等，不新增 publication。
	if err := importPartsWithRelease(ctx, conn, parts, release); err != nil {
		t.Fatalf("P11 release 重试失败: %v", err)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM data_publications").Scan(&publications); err != nil {
		t.Fatal(err)
	}
	if publications != 1 {
		t.Fatalf("重试后 publication 应仍为 1,得到 %d", publications)
	}
}

func TestImportV2CatalogStateEvidenceAtomic(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()
	parts, err := loadParts(partsDir(t))
	if err != nil {
		t.Fatal(err)
	}
	parts[0].CatalogState = "catalog_only"
	parts[1].CatalogState = "retired"
	release := &releaseManifest{
		SchemaVersion: 2, ReleaseID: "55101c4f-2291-5d51-9a2d-1f5d8ef0fa02",
		RunID: "8ef69660-9c17-5c60-b184-51c034db59dc", Decision: "auto",
		InputSHA256: strings.Repeat("a", 64), Stats: []byte(`{"total_skus":160}`),
		ManifestSHA256: strings.Repeat("b", 64),
	}
	evidence := testEvidence(t, parts[2].SKU)
	if err := importPartsWithRelease(ctx, conn, parts, release, []evidenceRecord{evidence}); err != nil {
		t.Fatalf("v2 导入失败: %v", err)
	}
	var state string
	var active bool
	if err := conn.QueryRow(ctx, "SELECT catalog_state, active FROM parts WHERE sku=$1", parts[0].SKU).Scan(&state, &active); err != nil {
		t.Fatal(err)
	}
	if state != "catalog_only" || active {
		t.Fatalf("catalog_only 映射错误: state=%s active=%v", state, active)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM part_evidence WHERE release_id=$1", release.ReleaseID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("evidence 应为 1 条,得到 %d", count)
	}
	if err := importPartsWithRelease(ctx, conn, parts, release, []evidenceRecord{evidence}); err != nil {
		t.Fatalf("相同 v2 重试应幂等: %v", err)
	}
	secondRelease := *release
	secondRelease.ReleaseID = "65101c4f-2291-5d51-9a2d-1f5d8ef0fa03"
	secondRelease.RunID = "9ef69660-9c17-5c60-b184-51c034db59dd"
	secondRelease.PreviousReleaseID = &release.ReleaseID
	secondRelease.ManifestSHA256 = strings.Repeat("e", 64)
	if err := importPartsWithRelease(ctx, conn, parts, &secondRelease, []evidenceRecord{evidence}); err != nil {
		t.Fatalf("后续 release 复用稳定 evidence 应通过: %v", err)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM part_evidence WHERE evidence_id=$1", evidence.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("稳定 evidence 跨 release 不应重复: %d", count)
	}

	parts[2].Model += " drift"
	evidence.EvidenceExcerpt = "conflicting excerpt"
	if err := importPartsWithRelease(ctx, conn, parts, &secondRelease, []evidenceRecord{evidence}); err == nil {
		t.Fatal("同 ID evidence 冲突必须导致整批回滚")
	}
	var model string
	if err := conn.QueryRow(ctx, "SELECT model FROM parts WHERE sku=$1", parts[2].SKU).Scan(&model); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(model, " drift") {
		t.Fatal("evidence 冲突时 parts 更新必须回滚")
	}
}

func TestLegacyImportDoesNotRetireMissingSKU(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()
	parts, err := loadParts(partsDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := importParts(ctx, conn, parts); err != nil {
		t.Fatal(err)
	}
	retiredSKU := parts[len(parts)-1].SKU
	if err := importParts(ctx, conn, parts[:len(parts)-1]); err != nil {
		t.Fatal(err)
	}
	var active bool
	var state string
	if err := conn.QueryRow(ctx,
		"SELECT active, catalog_state FROM parts WHERE sku = $1", retiredSKU).Scan(&active, &state); err != nil {
		t.Fatal(err)
	}
	if !active || state != "active_core" {
		t.Fatalf("普通导入不得通过文件缺失退休 SKU: active=%v state=%s", active, state)
	}
	if n := countParts(t, conn); n != 160 {
		t.Fatalf("退休不应物理删除,得到 %d 行", n)
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
