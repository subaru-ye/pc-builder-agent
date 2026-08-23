// Command datajob 把 P11 文件 run manifest 同步到 PostgreSQL 运维表。
// 文件 manifest 仍是任务失败时的恢复依据；此命令不执行采集或发布。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
)

type runError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type runManifest struct {
	SchemaVersion int             `json:"schema_version"`
	RunID         string          `json:"run_id"`
	Profile       string          `json:"profile"`
	Trigger       string          `json:"trigger"`
	ScheduledFor  string          `json:"scheduled_for"`
	StartedAt     string          `json:"started_at"`
	FinishedAt    *string         `json:"finished_at"`
	Status        string          `json:"status"`
	ModelUsed     bool            `json:"model_used"`
	Sources       json.RawMessage `json:"sources"`
	Summary       json.RawMessage `json:"summary"`
	Error         *runError       `json:"error"`
	ManifestSHA   string          `json:"-"`
}

var secretMarkers = []string{"api_key", "apikey", "authorization", "cookie", "password", "secret", "token"}

func assertSafeJSON(value any, path string) error {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			lowered := strings.ToLower(key)
			for _, marker := range secretMarkers {
				if strings.Contains(lowered, marker) {
					return fmt.Errorf("%s.%s 含敏感字段", path, key)
				}
			}
			if err := assertSafeJSON(child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range item {
			if err := assertSafeJSON(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case string:
		lowered := strings.ToLower(item)
		if strings.Contains(lowered, "authorization:") || strings.Contains(lowered, "cookie:") || strings.HasPrefix(lowered, "sk-") {
			return fmt.Errorf("%s 疑似含凭据", path)
		}
	}
	return nil
}

func loadRunManifest(path string) (runManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runManifest{}, fmt.Errorf("读取 run manifest 失败: %w", err)
	}
	var manifest runManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return runManifest{}, fmt.Errorf("run manifest 解码失败: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return runManifest{}, fmt.Errorf("run manifest 不得包含多个 JSON 值")
		}
		return runManifest{}, fmt.Errorf("run manifest 尾部非法: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return runManifest{}, fmt.Errorf("schema_version 必须为 1")
	}
	if _, err := uuid.Parse(manifest.RunID); err != nil {
		return runManifest{}, fmt.Errorf("run_id 非法: %w", err)
	}
	if manifest.ModelUsed {
		return runManifest{}, fmt.Errorf("定时 run 禁止 model_used=true")
	}
	if manifest.Profile != "health" && manifest.Profile != "weekly" && manifest.Profile != "monthly" && manifest.Profile != "retry" {
		return runManifest{}, fmt.Errorf("profile 非法: %q", manifest.Profile)
	}
	if manifest.Trigger != "manual" && manifest.Trigger != "schedule" && manifest.Trigger != "startup_catch_up" {
		return runManifest{}, fmt.Errorf("trigger 非法: %q", manifest.Trigger)
	}
	validStatus := map[string]bool{
		"running": true, "no_change": true, "published": true, "quarantined": true,
		"failed": true, "blocked": true,
	}
	if !validStatus[manifest.Status] {
		return runManifest{}, fmt.Errorf("status 非法: %q", manifest.Status)
	}
	if _, err := time.Parse(time.RFC3339, manifest.ScheduledFor); err != nil {
		return runManifest{}, fmt.Errorf("scheduled_for 非法: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, manifest.StartedAt); err != nil {
		return runManifest{}, fmt.Errorf("started_at 非法: %w", err)
	}
	if manifest.FinishedAt != nil {
		if _, err := time.Parse(time.RFC3339, *manifest.FinishedAt); err != nil {
			return runManifest{}, fmt.Errorf("finished_at 非法: %w", err)
		}
	}
	if !json.Valid(manifest.Sources) || !json.Valid(manifest.Summary) {
		return runManifest{}, fmt.Errorf("sources/summary 必须为合法 JSON")
	}
	var sources any
	var summary map[string]any
	if err := json.Unmarshal(manifest.Sources, &sources); err != nil {
		return runManifest{}, fmt.Errorf("sources JSON 解码失败: %w", err)
	}
	if err := json.Unmarshal(manifest.Summary, &summary); err != nil {
		return runManifest{}, fmt.Errorf("summary 必须为 JSON 对象: %w", err)
	}
	if err := assertSafeJSON(sources, "sources"); err != nil {
		return runManifest{}, fmt.Errorf("manifest 敏感信息门禁: %w", err)
	}
	if err := assertSafeJSON(summary, "summary"); err != nil {
		return runManifest{}, fmt.Errorf("manifest 敏感信息门禁: %w", err)
	}
	if manifest.Error != nil {
		if err := assertSafeJSON(map[string]any{"code": manifest.Error.Code, "message": manifest.Error.Message}, "error"); err != nil {
			return runManifest{}, fmt.Errorf("manifest 敏感信息门禁: %w", err)
		}
	}
	manifest.ManifestSHA = fmt.Sprintf("%x", sha256.Sum256(data))
	return manifest, nil
}

func upsertRun(ctx context.Context, conn *pgx.Conn, manifest runManifest) error {
	var errorCode *string
	if manifest.Error != nil {
		errorCode = &manifest.Error.Code
	}
	_, err := conn.Exec(ctx, `
		INSERT INTO data_job_runs (
			run_id, profile, trigger_kind, scheduled_for, status, model_used,
			manifest_sha256, error_code, summary, started_at, finished_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (run_id) DO UPDATE SET
			status = EXCLUDED.status,
			model_used = EXCLUDED.model_used,
			manifest_sha256 = EXCLUDED.manifest_sha256,
			error_code = EXCLUDED.error_code,
			summary = EXCLUDED.summary,
			finished_at = EXCLUDED.finished_at`,
		manifest.RunID, manifest.Profile, manifest.Trigger, manifest.ScheduledFor,
		manifest.Status, manifest.ModelUsed, manifest.ManifestSHA, errorCode,
		manifest.Summary, manifest.StartedAt, manifest.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("写入 data_job_runs 失败: %w", err)
	}
	return nil
}

func main() {
	path := flag.String("manifest", "", "P11 run manifest 路径")
	flag.Parse()
	if *path == "" {
		log.Fatal("必须用 -manifest 指定 run manifest")
	}
	manifest, err := loadRunManifest(*path)
	if err != nil {
		log.Fatalf("加载失败: %v", err)
	}
	dotenv.Load(".env")
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := upsertRun(ctx, conn, manifest); err != nil {
		log.Fatalf("同步失败: %v", err)
	}
	log.Printf("已同步 run %s status=%s", manifest.RunID, manifest.Status)
}
