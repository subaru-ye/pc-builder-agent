package main

import (
	"bytes"
	"context"
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

// PostgreSQL 端到端测试:PG_TEST_DSN 未设置时整体跳过(与 internal/store 同约定)。
// 本地运行(compose PG 就绪后):
//
//	$env:PG_TEST_DSN="postgres://pcbuilder:pcbuilder@localhost:15432/pcbuilder?sslmode=disable"
//	go test ./cmd/validate -count=1

// 夹具:一套全 pass 零件(与 internal/store 测试同口径)+ 一块 LGA1700 主板用于制造
// SOCKET_MATCH 错误级违规。
const fixtureSQL = `
INSERT INTO parts (sku, category, brand, model, specs) VALUES
('cpu-r5-7600',  'cpu',  'AMD', 'Ryzen 5 7600',
 '{"socket":"AM5","supported_chipsets":["B650","X670"],"has_igpu":true,"tdp_w":65}'),
('gpu-rtx4070',  'gpu',  'NVIDIA', 'RTX 4070',
 '{"length_mm":300,"tdp_w":220,"power_connectors":["pcie_16pin"]}'),
('mb-b650m',     'motherboard', 'MSI', 'B650M MORTAR',
 '{"socket":"AM5","chipset":"B650","memory_generation":"ddr5","memory_speed_max_mts":6000,"form_factor":"matx","m2_slots":2}'),
('mb-z790',      'motherboard', 'ASUS', 'Z790-P',
 '{"socket":"LGA1700","chipset":"Z790","memory_generation":"ddr5","memory_speed_max_mts":7200,"form_factor":"atx","m2_slots":4}'),
('ram-ddr5-6000','memory', 'Kingston', 'Fury 32G',
 '{"generation":"ddr5","speed_mts":6000}'),
('ssd-sn770',    'ssd',  'WD', 'SN770 1T',
 '{"form_factor":"m2"}'),
('psu-750w',     'psu',  'Corsair', 'RM750e',
 '{"wattage_w":750,"power_connectors":["pcie_8pin","pcie_8pin","pcie_16pin"]}'),
('case-air',     'case', 'Fractal', 'Pop Air',
 '{"gpu_length_max_mm":300,"cooler_height_max_mm":160,"supported_form_factors":["atx","matx","itx"],"radiator_sizes_mm":[240,360]}'),
('cooler-ak620', 'cooler', 'DeepCool', 'AK620',
 '{"type":"air","height_mm":155,"cooling_capacity_w":220}');
`

// setupDSN 建临时库 + 全量迁移 + 夹具,返回该库 DSN。
func setupDSN(t *testing.T) string {
	t.Helper()
	baseDSN := os.Getenv("PG_TEST_DSN")
	if baseDSN == "" {
		t.Skip("PG_TEST_DSN 未设置,跳过 PostgreSQL 端到端测试")
	}
	ctx := context.Background()

	dbName := fmt.Sprintf("validate_test_%d", time.Now().UnixNano())
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
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, fixtureSQL); err != nil {
		t.Fatalf("灌入夹具失败: %v", err)
	}
	return testDSN
}

// writeInput 写临时 build.json,返回路径。
func writeInput(t *testing.T, motherboard, cpu string) string {
	t.Helper()
	content := fmt.Sprintf(`{
  "schema_version": 1,
  "build_ref": "build_validate_e2e",
  "parts": {
    "cpu": %q,
    "motherboard": %q,
    "memory": "ram-ddr5-6000",
    "ssd": [{"sku": "ssd-sn770", "quantity": 1}],
    "gpu": "gpu-rtx4070",
    "psu": "psu-750w",
    "case": "case-air",
    "cooler": "cooler-ak620"
  }
}`, cpu, motherboard)
	path := filepath.Join(t.TempDir(), "build.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写临时 build.json 失败: %v", err)
	}
	return path
}

// runCLI 执行 run 并解析 stdout 报告(如有)。
func runCLI(t *testing.T, dsn, input string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), input, dsn, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestValidateLegalBuild(t *testing.T) {
	dsn := setupDSN(t)
	code, stdout, stderr := runCLI(t, dsn, writeInput(t, "mb-b650m", "cpu-r5-7600"))
	if code != exitOK {
		t.Fatalf("合法配置应 exit %d,得到 %d(stderr=%s)", exitOK, code, stderr)
	}
	var report schemas.ValidationReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout 应为 ValidationReport JSON: %v\n%s", err, stdout)
	}
	if report.OverallStatus != schemas.OverallPass {
		t.Errorf("overall_status = %s, want pass", report.OverallStatus)
	}
	if len(report.Checks) != len(schemas.AllRuleIDs) {
		t.Errorf("应输出 %d 条检查,得到 %d", len(schemas.AllRuleIDs), len(report.Checks))
	}
	for i, c := range report.Checks {
		if c.RuleID != schemas.AllRuleIDs[i] {
			t.Errorf("checks[%d].rule_id = %s, want %s(顺序冻结)", i, c.RuleID, schemas.AllRuleIDs[i])
		}
	}
}

func TestValidateViolatingBuild(t *testing.T) {
	dsn := setupDSN(t)
	// AM5 CPU 配 LGA1700 主板:SOCKET_MATCH 错误级违规 → overall fail → exit 1。
	code, stdout, stderr := runCLI(t, dsn, writeInput(t, "mb-z790", "cpu-r5-7600"))
	if code != exitViolation {
		t.Fatalf("违规配置应 exit %d,得到 %d(stderr=%s)", exitViolation, code, stderr)
	}
	var report schemas.ValidationReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout 应为 ValidationReport JSON: %v\n%s", err, stdout)
	}
	if report.OverallStatus != schemas.OverallFail {
		t.Errorf("overall_status = %s, want fail", report.OverallStatus)
	}
	if report.Checks[0].RuleID != schemas.RuleSocketMatch ||
		report.Checks[0].Outcome != schemas.OutcomeFail ||
		report.Checks[0].Severity != schemas.SeverityError {
		t.Errorf("SOCKET_MATCH 应为 error 级 fail: %+v", report.Checks[0])
	}
}

func TestValidateUnknownSKU(t *testing.T) {
	dsn := setupDSN(t)
	code, stdout, stderr := runCLI(t, dsn, writeInput(t, "mb-not-exist", "cpu-r5-7600"))
	if code != exitInputErr {
		t.Fatalf("未知 SKU 应 exit %d,得到 %d", exitInputErr, code)
	}
	if stdout != "" {
		t.Errorf("输入错误路径 stdout 应为空,得到 %q", stdout)
	}
	if !strings.Contains(stderr, "mb-not-exist") {
		t.Errorf("stderr 应包含具体 SKU,得到 %q", stderr)
	}
}
