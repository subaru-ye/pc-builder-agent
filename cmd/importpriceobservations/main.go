// Command importpriceobservations 原子导入 P12 不可变价格 release。
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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
)

type releaseFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type priceManifest struct {
	SchemaVersion     int             `json:"schema_version"`
	ReleaseID         string          `json:"release_id"`
	PreviousReleaseID *string         `json:"previous_release_id"`
	RunID             string          `json:"run_id"`
	SnapshotDate      string          `json:"snapshot_date"`
	CreatedAt         string          `json:"created_at"`
	Policy            string          `json:"policy"`
	InputSHA256       string          `json:"input_sha256"`
	ObservationsSHA   string          `json:"observations_sha256"`
	SelectionSHA      string          `json:"selection_sha256"`
	ReviewSHA         string          `json:"review_sha256"`
	Files             []releaseFile   `json:"files"`
	Stats             json.RawMessage `json:"stats"`
	ManifestSHA256    string          `json:"manifest_sha256"`
}

type observation struct {
	SchemaVersion     int      `json:"schema_version"`
	ID                string   `json:"observation_id"`
	SKU               string   `json:"sku"`
	PriceCNY          string   `json:"price_cny"`
	Currency          string   `json:"currency"`
	SourceID          string   `json:"source_id"`
	CollectorID       string   `json:"collector_id"`
	ProductID         string   `json:"product_id"`
	SourceURL         string   `json:"source_url"`
	Seller            string   `json:"seller"`
	PriceType         string   `json:"price_type"`
	AvailabilityBasis string   `json:"availability_basis"`
	StockStatus       string   `json:"stock_status"`
	VariantMatch      string   `json:"variant_match"`
	ObservedAt        string   `json:"observed_at"`
	RawSHA256         string   `json:"raw_sha256"`
	DecisionStatus    string   `json:"decision_status"`
	RejectionReasons  []string `json:"rejection_reasons"`
}

type selection struct {
	SchemaVersion      int     `json:"schema_version"`
	SKU                string  `json:"sku"`
	ObservationID      *string `json:"observation_id"`
	PriceCNY           string  `json:"price_cny"`
	SourceID           string  `json:"source_id"`
	ObservedAt         string  `json:"observed_at"`
	PriceType          string  `json:"price_type"`
	AvailabilityBasis  string  `json:"availability_basis"`
	CarriedForward     bool    `json:"carried_forward"`
	SourceSnapshotDate *string `json:"source_snapshot_date"`
}

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("不得包含多个 JSON 值")
		}
		return err
	}
	return nil
}

func loadManifest(releaseDir string) (priceManifest, error) {
	path := filepath.Join(releaseDir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return priceManifest{}, fmt.Errorf("读取价格 manifest 失败: %w", err)
	}
	var manifest priceManifest
	if err := strictJSON(data, &manifest); err != nil {
		return priceManifest{}, fmt.Errorf("价格 manifest 解码失败: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Policy != "manual" && manifest.Policy != "automatic" {
		return priceManifest{}, fmt.Errorf("价格 manifest schema/policy 非法")
	}
	if _, err := uuid.Parse(manifest.ReleaseID); err != nil {
		return priceManifest{}, fmt.Errorf("release_id 非法: %w", err)
	}
	if _, err := uuid.Parse(manifest.RunID); err != nil {
		return priceManifest{}, fmt.Errorf("run_id 非法: %w", err)
	}
	if manifest.PreviousReleaseID != nil {
		if _, err := uuid.Parse(*manifest.PreviousReleaseID); err != nil {
			return priceManifest{}, fmt.Errorf("previous_release_id 非法: %w", err)
		}
	}
	if _, err := time.Parse("2006-01-02", manifest.SnapshotDate); err != nil {
		return priceManifest{}, fmt.Errorf("snapshot_date 非法: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, manifest.CreatedAt); err != nil {
		return priceManifest{}, fmt.Errorf("created_at 非法: %w", err)
	}
	for _, value := range []string{manifest.InputSHA256, manifest.ObservationsSHA, manifest.SelectionSHA, manifest.ReviewSHA, manifest.ManifestSHA256} {
		if !hashPattern.MatchString(value) {
			return priceManifest{}, fmt.Errorf("manifest SHA-256 非法")
		}
	}
	var stats map[string]any
	if err := json.Unmarshal(manifest.Stats, &stats); err != nil || stats == nil {
		return priceManifest{}, fmt.Errorf("stats 必须为 JSON 对象")
	}
	if len(manifest.Files) != 3 {
		return priceManifest{}, fmt.Errorf("价格 release 必须包含三个文件")
	}
	canonical := map[string]any{}
	if err := json.Unmarshal(data, &canonical); err != nil {
		return priceManifest{}, err
	}
	canonical["manifest_sha256"] = ""
	encoded, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return priceManifest{}, err
	}
	encoded = append(encoded, '\n')
	if fmt.Sprintf("%x", sha256.Sum256(encoded)) != manifest.ManifestSHA256 {
		return priceManifest{}, fmt.Errorf("manifest_sha256 不匹配")
	}
	return manifest, nil
}

func validateFiles(releaseDir string, manifest priceManifest) error {
	want := map[string]string{
		"observations.jsonl": manifest.ObservationsSHA,
		"selection.jsonl":    manifest.SelectionSHA,
		"review.json":        manifest.ReviewSHA,
	}
	seen := make(map[string]bool)
	for _, file := range manifest.Files {
		if seen[file.Path] || !hashPattern.MatchString(file.SHA256) || want[file.Path] != file.SHA256 {
			return fmt.Errorf("release file 条目非法: %s", file.Path)
		}
		seen[file.Path] = true
		path := filepath.Join(releaseDir, file.Path)
		resolved, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		root, _ := filepath.Abs(releaseDir)
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release file 越过目录")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Bytes {
			return fmt.Errorf("release file %s 类型或大小不匹配", file.Path)
		}
		data, err := os.ReadFile(path)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != file.SHA256 {
			return fmt.Errorf("release file %s SHA-256 不匹配", file.Path)
		}
	}
	if len(seen) != len(want) {
		return fmt.Errorf("release files 不完整")
	}
	combined, _ := json.MarshalIndent(map[string]string{
		"observations_sha256": manifest.ObservationsSHA,
		"review_sha256":       manifest.ReviewSHA,
		"selection_sha256":    manifest.SelectionSHA,
	}, "", "  ")
	combined = append(combined, '\n')
	if fmt.Sprintf("%x", sha256.Sum256(combined)) != manifest.InputSHA256 {
		return fmt.Errorf("release input_sha256 不匹配")
	}
	return nil
}

func loadJSONL[T any](path string, validate func(T) error) ([]T, error) {
	stream, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	var rows []T
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			continue
		}
		var row T
		if err := strictJSON(data, &row); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), line, err)
		}
		if err := validate(row); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), line, err)
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}

func validateObservation(row observation) error {
	if row.SchemaVersion != 1 || !hashPattern.MatchString(row.ID) || !hashPattern.MatchString(row.RawSHA256) {
		return fmt.Errorf("observation schema/hash 非法")
	}
	if row.SKU == "" || row.SourceID == "" || row.ProductID == "" || row.Seller == "" || row.Currency != "CNY" {
		return fmt.Errorf("observation 必填字段非法")
	}
	if row.CollectorID == "" && row.PriceType == "listing" {
		return fmt.Errorf("listing 缺少 collector_id")
	}
	if row.AvailabilityBasis != "" && row.AvailabilityBasis != "confirmed_stock" && row.AvailabilityBasis != "search_listing" && row.AvailabilityBasis != "unknown" {
		return fmt.Errorf("observation availability_basis 非法")
	}
	if row.PriceType == "listing" && (row.CollectorID != "serpapi_baidu" || row.AvailabilityBasis != "search_listing" || row.StockStatus != "unknown") {
		return fmt.Errorf("listing 必须来自 serpapi_baidu 且只能表达搜索报价")
	}
	if !strings.HasPrefix(row.SourceURL, "https://") || strings.Contains(row.SourceURL, "@") {
		return fmt.Errorf("source_url 非法")
	}
	if _, err := time.Parse(time.RFC3339, row.ObservedAt); err != nil {
		return fmt.Errorf("observed_at 非法")
	}
	return nil
}

func validateSelection(row selection) error {
	if row.SchemaVersion != 1 || row.SKU == "" || row.SourceID == "" {
		return fmt.Errorf("selection 必填字段非法")
	}
	if row.ObservationID != nil && !hashPattern.MatchString(*row.ObservationID) {
		return fmt.Errorf("selection observation_id 非法")
	}
	if row.PriceType != "regular" && row.PriceType != "sale" && row.PriceType != "listing" && row.PriceType != "bootstrap" {
		return fmt.Errorf("selection price_type 非法")
	}
	if row.AvailabilityBasis == "" {
		row.AvailabilityBasis = "unknown"
	}
	if row.AvailabilityBasis != "confirmed_stock" && row.AvailabilityBasis != "search_listing" && row.AvailabilityBasis != "unknown" {
		return fmt.Errorf("selection availability_basis 非法")
	}
	if _, err := time.Parse(time.RFC3339, row.ObservedAt); err != nil {
		return fmt.Errorf("selection observed_at 非法")
	}
	return nil
}

func importRelease(ctx context.Context, conn *pgx.Conn, releaseDir string, manifest priceManifest, observations []observation, selections []selection) (bool, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existingRelease *string
	if err := tx.QueryRow(ctx, `SELECT release_id::text FROM price_snapshots WHERE manifest_sha256=$1`, manifest.ManifestSHA256).Scan(&existingRelease); err == nil {
		if existingRelease == nil || *existingRelease != manifest.ReleaseID {
			return false, fmt.Errorf("manifest 已对应其他 release")
		}
		return true, nil
	} else if err != pgx.ErrNoRows {
		return false, err
	}

	createdAt, _ := time.Parse(time.RFC3339, manifest.CreatedAt)
	_, err = tx.Exec(ctx, `INSERT INTO data_job_runs
		(run_id, profile, trigger_kind, scheduled_for, status, model_used, manifest_sha256, summary, started_at, finished_at)
		VALUES ($1, 'weekly', 'manual', $2, 'published', false, $3, $4, $2, $2)
		ON CONFLICT (run_id) DO NOTHING`, manifest.RunID, createdAt, manifest.ManifestSHA256, manifest.Stats)
	if err != nil {
		return false, fmt.Errorf("写入价格 run 失败: %w", err)
	}

	for _, row := range observations {
		if row.CollectorID == "" {
			row.CollectorID = "manual_price_csv"
		}
		if row.AvailabilityBasis == "" {
			if row.StockStatus == "in_stock" {
				row.AvailabilityBasis = "confirmed_stock"
			} else {
				row.AvailabilityBasis = "unknown"
			}
		}
		tag, insertErr := tx.Exec(ctx, `INSERT INTO price_observations
			(observation_id, sku, source_id, collector_id, product_id, source_url, seller, price_cny, currency,
			 price_type, availability_basis, stock_status, variant_match, observed_at, raw_sha256, decision_status, rejection_reasons)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT (observation_id) DO NOTHING`, row.ID, row.SKU, row.SourceID, row.CollectorID,
			row.ProductID, row.SourceURL, row.Seller, row.PriceCNY, row.Currency, row.PriceType,
			row.AvailabilityBasis, row.StockStatus, row.VariantMatch, row.ObservedAt, row.RawSHA256,
			row.DecisionStatus, row.RejectionReasons)
		if insertErr != nil {
			return false, fmt.Errorf("写入 observation %s 失败: %w", row.ID, insertErr)
		}
		if tag.RowsAffected() == 0 {
			var identical bool
			err = tx.QueryRow(ctx, `SELECT
				sku=$2 AND source_id=$3 AND collector_id=$4 AND product_id=$5 AND source_url=$6 AND seller=$7
				AND price_cny=$8 AND currency=$9 AND price_type=$10 AND availability_basis=$11 AND stock_status=$12
				AND variant_match=$13 AND observed_at=$14 AND raw_sha256=$15
				AND decision_status=$16 AND rejection_reasons=$17::jsonb
				FROM price_observations WHERE observation_id=$1`, row.ID, row.SKU, row.SourceID,
				row.CollectorID, row.ProductID, row.SourceURL, row.Seller, row.PriceCNY, row.Currency,
				row.PriceType, row.AvailabilityBasis, row.StockStatus, row.VariantMatch, row.ObservedAt,
				row.RawSHA256, row.DecisionStatus, row.RejectionReasons).Scan(&identical)
			if err != nil || !identical {
				return false, fmt.Errorf("observation %s 与既有不可变记录冲突", row.ID)
			}
		}
	}

	var previousID *int64
	if manifest.PreviousReleaseID != nil {
		var value int64
		if err := tx.QueryRow(ctx, `SELECT id FROM price_snapshots WHERE release_id=$1`, *manifest.PreviousReleaseID).Scan(&value); err != nil {
			return false, fmt.Errorf("previous price release 不存在: %w", err)
		}
		previousID = &value
	} else {
		var value int64
		if err := tx.QueryRow(ctx, `SELECT id FROM price_snapshots ORDER BY snapshot_date DESC LIMIT 1`).Scan(&value); err == nil {
			previousID = &value
		} else if err != pgx.ErrNoRows {
			return false, fmt.Errorf("查询历史价格快照失败: %w", err)
		}
	}
	var snapshotID int64
	err = tx.QueryRow(ctx, `INSERT INTO price_snapshots
		(snapshot_date, file_sha256, release_id, previous_snapshot_id, run_id, manifest_sha256, publication_policy, stats)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, manifest.SnapshotDate, manifest.InputSHA256,
		manifest.ReleaseID, previousID, manifest.RunID, manifest.ManifestSHA256, manifest.Policy, manifest.Stats).Scan(&snapshotID)
	if err != nil {
		return false, fmt.Errorf("创建价格快照失败（同日不同 manifest 会被拒绝）: %w", err)
	}
	for _, row := range selections {
		if row.AvailabilityBasis == "" {
			row.AvailabilityBasis = "unknown"
		}
		sourceSnapshotID := snapshotID
		if row.CarriedForward {
			if row.SourceSnapshotDate != nil {
				if err := tx.QueryRow(ctx, `SELECT id FROM price_snapshots WHERE snapshot_date=$1::date`, *row.SourceSnapshotDate).Scan(&sourceSnapshotID); err != nil {
					return false, fmt.Errorf("SKU %s 的 source snapshot 不存在: %w", row.SKU, err)
				}
			} else if previousID != nil {
				sourceSnapshotID = *previousID
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO prices
			(snapshot_id, sku, price_cny, source, observation_id, observed_at, price_type, availability_basis, carried_forward, source_snapshot_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, snapshotID, row.SKU, row.PriceCNY, row.SourceID,
			row.ObservationID, row.ObservedAt, row.PriceType, row.AvailabilityBasis, row.CarriedForward, sourceSnapshotID)
		if err != nil {
			return false, fmt.Errorf("写入 SKU %s 选价失败: %w", row.SKU, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func main() {
	releaseDir := flag.String("release", "", "P12 price release 目录")
	flag.Parse()
	if *releaseDir == "" {
		log.Fatal("必须指定 -release")
	}
	dotenv.Load(".env")
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置")
	}
	manifest, err := loadManifest(*releaseDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := validateFiles(*releaseDir, manifest); err != nil {
		log.Fatal(err)
	}
	observations, err := loadJSONL(filepath.Join(*releaseDir, "observations.jsonl"), validateObservation)
	if err != nil {
		log.Fatal(err)
	}
	selections, err := loadJSONL(filepath.Join(*releaseDir, "selection.jsonl"), validateSelection)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	skipped, err := importRelease(ctx, conn, *releaseDir, manifest, observations, selections)
	if err != nil {
		log.Fatal(err)
	}
	if skipped {
		log.Printf("价格 release %s 已存在，幂等跳过", manifest.ReleaseID)
		return
	}
	log.Printf("价格 release %s 导入完成，共 %d 个选价", manifest.ReleaseID, len(selections))
}
