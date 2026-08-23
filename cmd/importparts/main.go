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
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
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
	CatalogState  string           `json:"catalog_state,omitempty"`
}

// releaseManifest 是 P11 不可变 release 的最小导入信封。files/created_at 由
// 文件层保留，数据库只记录可建立发布链和审计所需的字段。
type releaseManifest struct {
	SchemaVersion     int             `json:"schema_version"`
	ReleaseID         string          `json:"release_id"`
	PreviousReleaseID *string         `json:"previous_release_id"`
	RunID             string          `json:"run_id"`
	CreatedAt         string          `json:"created_at"`
	Decision          string          `json:"decision"`
	InputSHA256       string          `json:"input_sha256"`
	PartsSHA256       string          `json:"parts_sha256,omitempty"`
	EvidenceSHA256    string          `json:"evidence_sha256,omitempty"`
	Files             []releaseFile   `json:"files"`
	Stats             json.RawMessage `json:"stats"`
	ManifestSHA256    string          `json:"manifest_sha256"`
}

type evidenceRecord struct {
	SchemaVersion   int             `json:"schema_version"`
	ID              string          `json:"id"`
	SKU             string          `json:"sku"`
	Field           string          `json:"field"`
	Value           json.RawMessage `json:"value"`
	SourceID        string          `json:"source_id"`
	SourceURL       string          `json:"source_url"`
	CapturedAt      string          `json:"captured_at"`
	RawSHA256       string          `json:"raw_sha256"`
	Method          string          `json:"method"`
	EvidenceExcerpt string          `json:"evidence_excerpt"`
	EvidenceStatus  string          `json:"evidence_status"`
}

type releaseFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func loadReleaseManifest(path string) (*releaseManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 release manifest 失败: %w", err)
	}
	var manifest releaseManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("release manifest 解码失败: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("release manifest 不得包含多个 JSON 值")
		}
		return nil, fmt.Errorf("release manifest 尾部非法: %w", err)
	}
	if manifest.SchemaVersion != 1 && manifest.SchemaVersion != 2 {
		return nil, fmt.Errorf("release manifest schema_version 必须为 1 或 2")
	}
	if _, err := uuid.Parse(manifest.ReleaseID); err != nil {
		return nil, fmt.Errorf("release_id 非法: %w", err)
	}
	if _, err := uuid.Parse(manifest.RunID); err != nil {
		return nil, fmt.Errorf("run_id 非法: %w", err)
	}
	if manifest.PreviousReleaseID != nil {
		if _, err := uuid.Parse(*manifest.PreviousReleaseID); err != nil {
			return nil, fmt.Errorf("previous_release_id 非法: %w", err)
		}
	}
	if manifest.Decision != "auto" && manifest.Decision != "manual" && manifest.Decision != "bootstrap" {
		return nil, fmt.Errorf("release decision 非法: %q", manifest.Decision)
	}
	if !sha256Pattern.MatchString(manifest.InputSHA256) || !sha256Pattern.MatchString(manifest.ManifestSHA256) {
		return nil, fmt.Errorf("release SHA-256 必须为 64 位小写十六进制")
	}
	if manifest.SchemaVersion == 1 && (manifest.PartsSHA256 != "" || manifest.EvidenceSHA256 != "") {
		return nil, fmt.Errorf("release v1 不得包含 parts/evidence SHA-256")
	}
	if manifest.SchemaVersion == 2 &&
		(!sha256Pattern.MatchString(manifest.PartsSHA256) || !sha256Pattern.MatchString(manifest.EvidenceSHA256)) {
		return nil, fmt.Errorf("release v2 parts/evidence SHA-256 非法")
	}
	if _, err := time.Parse(time.RFC3339, manifest.CreatedAt); err != nil {
		return nil, fmt.Errorf("created_at 非法: %w", err)
	}
	var stats map[string]any
	if err := json.Unmarshal(manifest.Stats, &stats); err != nil || stats == nil {
		return nil, fmt.Errorf("stats 必须为 JSON 对象")
	}
	expected := make(map[string]struct{}, len(schemas.AllCategories)+1)
	for _, category := range schemas.AllCategories {
		expected["parts/"+string(category)+".jsonl"] = struct{}{}
	}
	if manifest.SchemaVersion == 2 {
		expected["evidence/fields.jsonl"] = struct{}{}
	}
	if len(manifest.Files) != len(expected) {
		return nil, fmt.Errorf("release files 与 schema_version 不匹配")
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		if _, ok := expected[file.Path]; !ok || !sha256Pattern.MatchString(file.SHA256) || file.Bytes < 0 {
			return nil, fmt.Errorf("release file 条目非法: %+v", file)
		}
		if _, duplicate := seen[file.Path]; duplicate {
			return nil, fmt.Errorf("release file 路径重复: %s", file.Path)
		}
		seen[file.Path] = struct{}{}
	}
	if len(seen) != len(expected) {
		return nil, fmt.Errorf("release files 缺少类目")
	}
	var canonical map[string]any
	if err := json.Unmarshal(data, &canonical); err != nil {
		return nil, fmt.Errorf("release manifest 规范化失败: %w", err)
	}
	canonical["manifest_sha256"] = ""
	encoded, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("release manifest 规范化失败: %w", err)
	}
	encoded = append(encoded, '\n')
	sum := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if sum != manifest.ManifestSHA256 {
		return nil, fmt.Errorf("release manifest_sha256 不匹配")
	}
	return &manifest, nil
}

func partsDigest(dir string) (string, error) {
	digest := sha256.New()
	for _, category := range schemas.AllCategories {
		name := string(category) + ".jsonl"
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", fmt.Errorf("读取 release parts %s 失败: %w", name, err)
		}
		_, _ = digest.Write([]byte(name))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(data)
		_, _ = digest.Write([]byte{0})
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func validateReleaseFiles(manifestPath, partsDir string, manifest *releaseManifest) error {
	releaseDir, err := filepath.Abs(filepath.Dir(manifestPath))
	if err != nil {
		return fmt.Errorf("解析 release 目录失败: %w", err)
	}
	expectedPartsDir, err := filepath.Abs(filepath.Join(releaseDir, "parts"))
	if err != nil {
		return fmt.Errorf("解析 release parts 目录失败: %w", err)
	}
	actualPartsDir, err := filepath.Abs(partsDir)
	if err != nil || filepath.Clean(actualPartsDir) != filepath.Clean(expectedPartsDir) {
		return fmt.Errorf("-dir 必须指向 release manifest 同目录下的 parts")
	}
	for _, file := range manifest.Files {
		path := filepath.Join(releaseDir, filepath.FromSlash(file.Path))
		resolved, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("解析 release file %s 失败: %w", file.Path, err)
		}
		relative, err := filepath.Rel(releaseDir, resolved)
		if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release file %s 越过 release 目录", file.Path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("release file %s 不存在: %w", file.Path, err)
		}
		if !info.Mode().IsRegular() || info.Size() != file.Bytes {
			return fmt.Errorf("release file %s 类型或大小不匹配", file.Path)
		}
		digest := sha256.New()
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 release file %s 失败: %w", file.Path, err)
		}
		_, _ = digest.Write(content)
		if fmt.Sprintf("%x", digest.Sum(nil)) != file.SHA256 {
			return fmt.Errorf("release file %s SHA-256 不匹配", file.Path)
		}
	}
	digest, err := partsDigest(actualPartsDir)
	if err != nil {
		return err
	}
	if manifest.SchemaVersion == 1 {
		if digest != manifest.InputSHA256 {
			return fmt.Errorf("release parts 与 input_sha256 不一致")
		}
		return nil
	}
	if digest != manifest.PartsSHA256 {
		return fmt.Errorf("release parts 与 parts_sha256 不一致")
	}
	evidencePath := filepath.Join(releaseDir, "evidence", "fields.jsonl")
	evidenceData, err := os.ReadFile(evidencePath)
	if err != nil {
		return fmt.Errorf("读取 release evidence 失败: %w", err)
	}
	evidenceDigest := fmt.Sprintf("%x", sha256.Sum256(evidenceData))
	if evidenceDigest != manifest.EvidenceSHA256 {
		return fmt.Errorf("release evidence 与 evidence_sha256 不一致")
	}
	combined, err := json.MarshalIndent(map[string]string{
		"parts_sha256": digest, "evidence_sha256": evidenceDigest,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("规范化 release 输入哈希失败: %w", err)
	}
	combined = append(combined, '\n')
	if fmt.Sprintf("%x", sha256.Sum256(combined)) != manifest.InputSHA256 {
		return fmt.Errorf("release parts/evidence 与 input_sha256 不一致")
	}
	return nil
}

func loadEvidence(path string) ([]evidenceRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 evidence 失败: %w", err)
	}
	defer func() { _ = f.Close() }()
	var records []evidenceRecord
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var record evidenceRecord
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("evidence:%d 解码失败: %w", lineNo, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("evidence:%d JSON 尾部非法", lineNo)
		}
		if err := validateEvidence(record); err != nil {
			return nil, fmt.Errorf("evidence:%d %w", lineNo, err)
		}
		if _, duplicate := seen[record.ID]; duplicate {
			return nil, fmt.Errorf("evidence:%d ID %q 重复", lineNo, record.ID)
		}
		seen[record.ID] = struct{}{}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 evidence 失败: %w", err)
	}
	return records, nil
}

func validateEvidence(record evidenceRecord) error {
	if record.SchemaVersion != 1 || !sha256Pattern.MatchString(record.ID) || !sha256Pattern.MatchString(record.RawSHA256) {
		return fmt.Errorf("schema_version 或哈希非法")
	}
	if record.SKU == "" || record.SourceID == "" || record.Field == "" || len(record.Value) == 0 || !json.Valid(record.Value) {
		return fmt.Errorf("SKU、来源、字段或值非法")
	}
	if !regexp.MustCompile(`^(model|brand|specs\.[a-z0-9_]+)$`).MatchString(record.Field) {
		return fmt.Errorf("field 非法: %q", record.Field)
	}
	parsedURL, err := url.Parse(record.SourceURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil {
		return fmt.Errorf("source_url 必须是不含凭据的 HTTPS URL")
	}
	if _, err := time.Parse(time.RFC3339, record.CapturedAt); err != nil {
		return fmt.Errorf("captured_at 非法: %w", err)
	}
	if record.Method != "deterministic" && record.Method != "model_assisted" && record.Method != "manual" {
		return fmt.Errorf("method 非法: %q", record.Method)
	}
	if record.EvidenceStatus != "verified" && record.EvidenceStatus != "candidate" &&
		record.EvidenceStatus != "conflict" && record.EvidenceStatus != "rejected" {
		return fmt.Errorf("evidence_status 非法: %q", record.EvidenceStatus)
	}
	if utf8.RuneCountInString(record.EvidenceExcerpt) > 500 {
		return fmt.Errorf("evidence_excerpt 超过 500 字符")
	}
	var value any
	if err := json.Unmarshal(record.Value, &value); err != nil {
		return fmt.Errorf("value 非法: %w", err)
	}
	identity, err := json.Marshal(map[string]any{
		"source_id":  record.SourceID,
		"sku":        record.SKU,
		"field":      record.Field,
		"value":      value,
		"raw_sha256": record.RawSHA256,
	})
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(identity)) != record.ID {
		return fmt.Errorf("evidence ID 与内容不匹配")
	}
	lowered := strings.ToLower(record.SourceURL + "\n" + record.EvidenceExcerpt)
	for _, marker := range []string{"authorization:", "cookie:", "api_key", "apikey", "password", "secret", "token=", "sk-"} {
		if strings.Contains(lowered, marker) {
			return fmt.Errorf("evidence 含敏感信息标记")
		}
	}
	return nil
}

func specFingerprint(rec partRecord) (string, error) {
	var value struct {
		Specs      any `json:"specs"`
		SourceMeta any `json:"source_meta"`
	}
	if err := json.Unmarshal(rec.Specs, &value.Specs); err != nil {
		return "", err
	}
	if err := json.Unmarshal(rec.SourceMeta, &value.SourceMeta); err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func jsonSemanticallyEqual(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
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
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("%s:%d 不得包含多个 JSON 值", name, lineNo)
			}
			return nil, fmt.Errorf("%s:%d JSON 尾部非法: %w", name, lineNo, err)
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
	if state := catalogState(rec); state != "active_core" && state != "catalog_only" && state != "retired" {
		return fmt.Errorf("SKU %q catalog_state 非法: %q", rec.SKU, rec.CatalogState)
	}
	return nil
}

func catalogState(rec partRecord) string {
	if rec.CatalogState == "" {
		return "active_core"
	}
	return rec.CatalogState
}

// importParts 单事务全量 upsert;任一条写入失败整批回滚。
// 重跑同一批数据只更新 updated_at 等列,行数与内容保持不变(幂等)。
func importParts(ctx context.Context, conn *pgx.Conn, parts []partRecord) error {
	return importPartsWithRelease(ctx, conn, parts, nil)
}

// importPartsWithRelease 把 parts 与可选 P11 发布记录放入同一数据库事务。
func importPartsWithRelease(
	ctx context.Context,
	conn *pgx.Conn,
	parts []partRecord,
	release *releaseManifest,
	evidenceSets ...[]evidenceRecord,
) error {
	var evidence []evidenceRecord
	if len(evidenceSets) > 1 {
		return fmt.Errorf("evidence 参数只能提供一次")
	}
	if len(evidenceSets) == 1 {
		evidence = evidenceSets[0]
	}
	if release == nil && len(evidence) != 0 {
		return fmt.Errorf("没有 release 时不得导入 evidence")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 提交成功后 Rollback 为空操作
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('pc_builder_agent:data_publish', 0))`); err != nil {
		return fmt.Errorf("获取数据发布事务锁失败: %w", err)
	}

	const upsertSQL = `
		INSERT INTO parts (
			sku, category, brand, model, schema_version, specs, source_meta,
			active, catalog_state, spec_fingerprint
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (sku) DO UPDATE SET
			category       = EXCLUDED.category,
			brand          = EXCLUDED.brand,
			model          = EXCLUDED.model,
			schema_version = EXCLUDED.schema_version,
			specs          = EXCLUDED.specs,
			source_meta    = EXCLUDED.source_meta,
			active         = EXCLUDED.active,
			catalog_state  = EXCLUDED.catalog_state,
			spec_fingerprint = EXCLUDED.spec_fingerprint,
			updated_at     = now()`
	skus := make([]string, 0, len(parts))
	for _, rec := range parts {
		fingerprint, err := specFingerprint(rec)
		if err != nil {
			return fmt.Errorf("计算 SKU %q 指纹失败: %w", rec.SKU, err)
		}
		state := catalogState(rec)
		active := state == "active_core"
		if _, err := tx.Exec(ctx, upsertSQL,
			rec.SKU, rec.Category, rec.Brand, rec.Model,
			rec.SchemaVersion, rec.Specs, rec.SourceMeta, active, state, fingerprint); err != nil {
			return fmt.Errorf("upsert SKU %q 失败(整批回滚): %w", rec.SKU, err)
		}
		skus = append(skus, rec.SKU)
	}
	if release != nil {
		var missing int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM parts
			WHERE catalog_state <> 'retired' AND NOT (sku = ANY($1::text[]))`, skus).Scan(&missing); err != nil {
			return fmt.Errorf("检查 release 缺失 SKU 失败(整批回滚): %w", err)
		}
		if missing != 0 {
			return fmt.Errorf("release 缺少 %d 个 active SKU；退休必须通过显式状态变更表示", missing)
		}
	}
	if release != nil {
		var existing struct {
			PreviousReleaseID *string
			RunID             *string
			ManifestSHA256    string
			InputSHA256       string
			Decision          string
			Stats             []byte
		}
		err := tx.QueryRow(ctx, `
			SELECT previous_release_id::text, run_id::text, manifest_sha256,
			       input_sha256, decision, stats::text
			FROM data_publications WHERE release_id = $1`, release.ReleaseID).Scan(
			&existing.PreviousReleaseID,
			&existing.RunID,
			&existing.ManifestSHA256,
			&existing.InputSHA256,
			&existing.Decision,
			&existing.Stats,
		)
		if err == nil {
			if existing.PreviousReleaseID == nil && release.PreviousReleaseID != nil ||
				existing.PreviousReleaseID != nil && release.PreviousReleaseID == nil ||
				existing.PreviousReleaseID != nil && release.PreviousReleaseID != nil && *existing.PreviousReleaseID != *release.PreviousReleaseID ||
				existing.RunID == nil || *existing.RunID != release.RunID ||
				existing.ManifestSHA256 != release.ManifestSHA256 ||
				existing.InputSHA256 != release.InputSHA256 ||
				existing.Decision != release.Decision ||
				!jsonSemanticallyEqual(existing.Stats, release.Stats) {
				return fmt.Errorf("release %q 已存在但 publication 元数据不一致", release.ReleaseID)
			}
		} else if err == pgx.ErrNoRows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO data_publications (
					release_id, previous_release_id, run_id, manifest_sha256,
					input_sha256, decision, stats
				) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				release.ReleaseID, release.PreviousReleaseID, release.RunID,
				release.ManifestSHA256, release.InputSHA256, release.Decision, release.Stats,
			); err != nil {
				return fmt.Errorf("记录 release %q 失败(整批回滚): %w", release.ReleaseID, err)
			}
		} else {
			return fmt.Errorf("读取 release %q publication 失败: %w", release.ReleaseID, err)
		}
	}
	if release != nil {
		knownSKUs := make(map[string]struct{}, len(parts))
		for _, record := range parts {
			knownSKUs[record.SKU] = struct{}{}
		}
		for _, record := range evidence {
			if err := validateEvidence(record); err != nil {
				return fmt.Errorf("evidence %q 非法(整批回滚): %w", record.ID, err)
			}
			if _, exists := knownSKUs[record.SKU]; !exists {
				return fmt.Errorf("evidence %q 引用 release 外 SKU %q(整批回滚)", record.ID, record.SKU)
			}
			command, err := tx.Exec(ctx, `
				INSERT INTO part_evidence (
					evidence_id, sku, field_path, value, source_id, source_url,
					raw_sha256, method, evidence_status, captured_at, release_id, evidence_excerpt
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
				ON CONFLICT (evidence_id) DO NOTHING`,
				record.ID, record.SKU, record.Field, record.Value, record.SourceID,
				record.SourceURL, record.RawSHA256, record.Method, record.EvidenceStatus,
				record.CapturedAt, release.ReleaseID, record.EvidenceExcerpt,
			)
			if err != nil {
				return fmt.Errorf("写入 evidence %q 失败(整批回滚): %w", record.ID, err)
			}
			if command.RowsAffected() == 0 {
				var existing evidenceRecord
				var existingRelease *string
				var existingCaptured time.Time
				if err := tx.QueryRow(ctx, `
					SELECT evidence_id, sku, field_path, value, source_id, source_url,
					       raw_sha256, method, evidence_status, captured_at, release_id::text,
					       evidence_excerpt
					FROM part_evidence WHERE evidence_id = $1`, record.ID).Scan(
					&existing.ID, &existing.SKU, &existing.Field, &existing.Value, &existing.SourceID,
					&existing.SourceURL, &existing.RawSHA256, &existing.Method,
					&existing.EvidenceStatus, &existingCaptured, &existingRelease, &existing.EvidenceExcerpt,
				); err != nil {
					return fmt.Errorf("核对 evidence %q 失败: %w", record.ID, err)
				}
				captured, _ := time.Parse(time.RFC3339, record.CapturedAt)
				if existing.SKU != record.SKU || existing.Field != record.Field ||
					existing.SourceID != record.SourceID || existing.SourceURL != record.SourceURL ||
					existing.RawSHA256 != record.RawSHA256 || existing.Method != record.Method ||
					existing.EvidenceStatus != record.EvidenceStatus ||
					existing.EvidenceExcerpt != record.EvidenceExcerpt ||
					!captured.Equal(existingCaptured) || !jsonSemanticallyEqual(existing.Value, record.Value) {
					return fmt.Errorf("evidence %q 已存在但元数据不一致", record.ID)
				}
				if existingRelease == nil {
					if _, err := tx.Exec(ctx, `
						UPDATE part_evidence SET release_id = $1
						WHERE evidence_id = $2 AND release_id IS NULL`, release.ReleaseID, record.ID); err != nil {
						return fmt.Errorf("关联历史 evidence %q 到 release 失败: %w", record.ID, err)
					}
				}
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

func main() {
	dir := flag.String("dir", filepath.Join("scripts", "data", "parts"), "parts jsonl 产物目录")
	releaseManifestPath := flag.String("release-manifest", "", "P11 release manifest 路径(可选)")
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
	var release *releaseManifest
	var evidence []evidenceRecord
	if *releaseManifestPath != "" {
		release, err = loadReleaseManifest(*releaseManifestPath)
		if err != nil {
			log.Fatalf("加载 release manifest 失败: %v", err)
		}
		if filepath.Base(filepath.Dir(*releaseManifestPath)) != release.ReleaseID {
			log.Fatalf("release manifest 所在目录必须与 release_id 一致")
		}
		if err := validateReleaseFiles(*releaseManifestPath, *dir, release); err != nil {
			log.Fatalf("校验 release 文件失败: %v", err)
		}
		if release.SchemaVersion == 2 {
			evidencePath := filepath.Join(filepath.Dir(*releaseManifestPath), "evidence", "fields.jsonl")
			evidence, err = loadEvidence(evidencePath)
			if err != nil {
				log.Fatalf("加载 release evidence 失败: %v", err)
			}
		}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := importPartsWithRelease(ctx, conn, parts, release, evidence); err != nil {
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
