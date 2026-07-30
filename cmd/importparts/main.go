// P1 parts 事务导入器(D5):把数据管道产物 scripts/data/parts/<category>.jsonl
// 全量导入 PostgreSQL parts 表。按 sku upsert,幂等可重跑;整批单事务,任一条
// 记录非法(信封、类目、specs 契约)或写入失败即整批回滚,不留半批状态。
//
// 运行方式(从仓库根,PG_DSN 来自 .env 或环境变量):
//
//	go run ./cmd/importparts                 # 默认读 scripts/data/parts
//	go run ./cmd/importparts -dir 某目录     # 显式指定产物目录
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// partRecord parts.jsonl 单行信封,与 parts 表列一一对应(时间戳由 DB 生成)。
type partRecord struct {
	SKU           string           `json:"sku"`
	Category      schemas.Category `json:"category"`
	Brand         string           `json:"brand"`
	Model         string           `json:"model"`
	SchemaVersion int              `json:"schema_version"`
	Specs         json.RawMessage  `json:"specs"`
	SourceMeta    json.RawMessage  `json:"source_meta"`
}

// specCheckers 各类目 specs 的契约校验(严格解码,未知字段/非法枚举即错)。
var specCheckers = map[schemas.Category]func([]byte) error{
	schemas.CategoryCPU:         func(b []byte) error { _, err := schemas.DecodeCPUSpec(b); return err },
	schemas.CategoryGPU:         func(b []byte) error { _, err := schemas.DecodeGPUSpec(b); return err },
	schemas.CategoryMotherboard: func(b []byte) error { _, err := schemas.DecodeMotherboardSpec(b); return err },
	schemas.CategoryMemory:      func(b []byte) error { _, err := schemas.DecodeMemorySpec(b); return err },
	schemas.CategorySSD:         func(b []byte) error { _, err := schemas.DecodeSSDSpec(b); return err },
	schemas.CategoryPSU:         func(b []byte) error { _, err := schemas.DecodePSUSpec(b); return err },
	schemas.CategoryCase:        func(b []byte) error { _, err := schemas.DecodeCaseSpec(b); return err },
	schemas.CategoryCooler:      func(b []byte) error { _, err := schemas.DecodeCoolerSpec(b); return err },
}

// loadParts 读取目录下八类 parts.jsonl 并逐条校验;任一条非法立即失败。
// 八个文件缺一不可:导入器面向全量目录,不接受部分类目静默缺失。
func loadParts(dir string) ([]partRecord, error) {
	var out []partRecord
	seen := make(map[string]string) // sku -> 来源文件(跨文件查重)
	for _, cat := range schemas.AllCategories {
		name := string(cat) + ".jsonl"
		records, err := loadCategoryFile(filepath.Join(dir, name), cat, seen)
		if err != nil {
			return nil, err
		}
		out = append(out, records...)
	}
	return out, nil
}

// loadCategoryFile 读取单类文件:信封严格解码 + 类目一致性 + specs 契约校验。
func loadCategoryFile(path string, cat schemas.Category, seen map[string]string) ([]partRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 失败: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	name := filepath.Base(path)
	var out []partRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec partRecord
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rec); err != nil {
			return nil, fmt.Errorf("%s:%d 记录解码失败: %w", name, lineNo, err)
		}
		if err := validateRecord(rec, cat); err != nil {
			return nil, fmt.Errorf("%s:%d %w", name, lineNo, err)
		}
		if prev, dup := seen[rec.SKU]; dup {
			return nil, fmt.Errorf("%s:%d SKU %q 与 %s 重复", name, lineNo, rec.SKU, prev)
		}
		seen[rec.SKU] = name
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", name, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 无有效记录", name)
	}
	return out, nil
}

// validateRecord 单条信封校验:必填字段、类目与文件一致、specs 过对应契约。
func validateRecord(rec partRecord, cat schemas.Category) error {
	if rec.SKU == "" {
		return fmt.Errorf("sku 不得为空")
	}
	if rec.Brand == "" || rec.Model == "" {
		return fmt.Errorf("SKU %q brand/model 不得为空", rec.SKU)
	}
	if rec.SchemaVersion != 1 {
		return fmt.Errorf("SKU %q schema_version 必须为 1,得到 %d", rec.SKU, rec.SchemaVersion)
	}
	if rec.Category != cat {
		return fmt.Errorf("SKU %q 类目为 %s,与文件类目 %s 不符", rec.SKU, rec.Category, cat)
	}
	check, ok := specCheckers[rec.Category]
	if !ok {
		return fmt.Errorf("SKU %q 非法类目 %q", rec.SKU, rec.Category)
	}
	if len(rec.Specs) == 0 {
		return fmt.Errorf("SKU %q specs 缺失", rec.SKU)
	}
	if err := check(rec.Specs); err != nil {
		return fmt.Errorf("SKU %q %w", rec.SKU, err)
	}
	if len(rec.SourceMeta) == 0 {
		return fmt.Errorf("SKU %q source_meta 缺失", rec.SKU)
	}
	return nil
}

// importParts 单事务全量 upsert;任一条写入失败整批回滚。
// 重跑同一批数据只更新 updated_at 等列,行数与内容保持不变(幂等)。
func importParts(ctx context.Context, conn *pgx.Conn, parts []partRecord) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 提交成功后 Rollback 为空操作

	const upsertSQL = `
		INSERT INTO parts (sku, category, brand, model, schema_version, specs, source_meta)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (sku) DO UPDATE SET
			category       = EXCLUDED.category,
			brand          = EXCLUDED.brand,
			model          = EXCLUDED.model,
			schema_version = EXCLUDED.schema_version,
			specs          = EXCLUDED.specs,
			source_meta    = EXCLUDED.source_meta,
			active         = TRUE,
			updated_at     = now()`
	for _, rec := range parts {
		if _, err := tx.Exec(ctx, upsertSQL,
			rec.SKU, rec.Category, rec.Brand, rec.Model,
			rec.SchemaVersion, rec.Specs, rec.SourceMeta); err != nil {
			return fmt.Errorf("upsert SKU %q 失败(整批回滚): %w", rec.SKU, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

func main() {
	dir := flag.String("dir", filepath.Join("scripts", "data", "parts"), "parts jsonl 产物目录")
	flag.Parse()

	dotenv.Load(".env")
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:复制 .env.example 为 .env,或显式导出 PG_DSN")
	}

	parts, err := loadParts(*dir)
	if err != nil {
		log.Fatalf("加载 parts 产物失败: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := importParts(ctx, conn, parts); err != nil {
		log.Fatalf("导入失败: %v", err)
	}

	perCat := make(map[schemas.Category]int)
	for _, rec := range parts {
		perCat[rec.Category]++
	}
	for _, cat := range schemas.AllCategories {
		log.Printf("%-12s %d 条", cat, perCat[cat])
	}
	log.Printf("导入完成:共 %d 条(单事务 upsert)", len(parts))
}
