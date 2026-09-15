// P1 价格快照导入器(D6):把人工采集的人民币价格 CSV 导入 PostgreSQL。
// CSV 表头固定为 sku,price_cny,source,captured_at;captured_at 为 YYYY-MM-DD,
// 默认全文件同一天;显式指定 snapshot-date 时允许沿用旧观察日期并核验旧价。
// 文件 SHA256 写入 price_snapshots,行写 prices,旧观察日期不会被刷新。
//
// 幂等语义:同 SHA256 的文件重跑直接跳过(exit 0);同日期不同内容视为冲突报错,
// 显式 -revision 可新增同日批次，保留旧快照。任一行 SKU 不存在或数值非法,整批回滚。
//
// 运行方式(从仓库根,PG_DSN 来自 .env 或环境变量):
//
//	go run ./cmd/importprices -file scripts/data/prices/2026-07-28.csv
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
)

// priceRow CSV 单行(价格保持十进制文本,由 PG NUMERIC(10,2) 承接,不走浮点)。
type priceRow struct {
	SKU        string
	PriceCNY   string
	Source     string
	ObservedAt time.Time
}

// priceBatch 一次导入批次:快照日期 + 文件哈希 + 全部价格行。
type priceBatch struct {
	AllowRevision bool
	SnapshotDate  time.Time
	FileSHA256    string
	Rows          []priceRow
}

var expectedHeader = []string{"sku", "price_cny", "source", "captured_at"}

// pricePattern 人民币价格:正十进制,最多两位小数(NUMERIC(10,2) 同口径)。
var pricePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,2})?$`)

// loadPriceCSV 读取并校验价格 CSV;任一行非法立即失败。
func loadPriceCSV(path string) (priceBatch, error) {
	return loadPriceCSVForSnapshot(path, "")
}

// loadPriceCSVForSnapshot 将发布批次日期与每行原始采集日期分开;混合日期须显式选择批次。
func loadPriceCSVForSnapshot(path, snapshotDate string) (priceBatch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return priceBatch{}, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	sum := sha256.Sum256(data)

	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		return priceBatch{}, fmt.Errorf("解析 CSV 失败: %w", err)
	}
	if len(records) == 0 {
		return priceBatch{}, errors.New("CSV 为空,缺少表头")
	}
	if !headerEquals(records[0]) {
		return priceBatch{}, fmt.Errorf("表头必须为 %v,得到 %v", expectedHeader, records[0])
	}
	if len(records) == 1 {
		return priceBatch{}, errors.New("CSV 无数据行")
	}

	batch := priceBatch{FileSHA256: hex.EncodeToString(sum[:])}
	if snapshotDate != "" {
		batch.SnapshotDate, err = time.Parse("2006-01-02", snapshotDate)
		if err != nil {
			return priceBatch{}, fmt.Errorf("snapshot-date 非法: %w", err)
		}
	}
	seen := make(map[string]bool)
	for i, rec := range records[1:] {
		lineNo := i + 2 // 表头占第 1 行
		sku, price, source, captured := rec[0], rec[1], rec[2], rec[3]
		if sku == "" || source == "" {
			return priceBatch{}, fmt.Errorf("第 %d 行 sku/source 不得为空", lineNo)
		}
		if seen[sku] {
			return priceBatch{}, fmt.Errorf("第 %d 行 SKU %q 重复", lineNo, sku)
		}
		seen[sku] = true
		if !pricePattern.MatchString(price) || price == "0" || price == "0.0" || price == "0.00" {
			return priceBatch{}, fmt.Errorf("第 %d 行 price_cny %q 非法(需正十进制,至多两位小数)", lineNo, price)
		}
		day, err := time.ParseInLocation("2006-01-02", captured, time.UTC)
		if err != nil {
			return priceBatch{}, fmt.Errorf("第 %d 行 captured_at %q 非法(需 YYYY-MM-DD): %w", lineNo, captured, err)
		}
		if batch.SnapshotDate.IsZero() {
			batch.SnapshotDate = day
		} else if snapshotDate == "" && !batch.SnapshotDate.Equal(day) {
			return priceBatch{}, fmt.Errorf("第 %d 行 captured_at %s 与批次日期 %s 不一致(一文件一快照日)",
				lineNo, captured, batch.SnapshotDate.Format("2006-01-02"))
		}
		if day.After(batch.SnapshotDate) {
			return priceBatch{}, fmt.Errorf("第 %d 行 captured_at 晚于快照日期", lineNo)
		}
		batch.Rows = append(batch.Rows, priceRow{SKU: sku, PriceCNY: price, Source: source, ObservedAt: day})
	}
	return batch, nil
}

func headerEquals(got []string) bool {
	if len(got) != len(expectedHeader) {
		return false
	}
	for i, want := range expectedHeader {
		if got[i] != want {
			return false
		}
	}
	return true
}

// importPrices 单事务导入一个批次。返回 skipped=true 表示同 SHA256 已入库(幂等跳过)。
// 同日期不同哈希、SKU 不在 parts 表(FK)等任何失败都整批回滚。
func importPrices(ctx context.Context, conn *pgx.Conn, batch priceBatch) (skipped bool, err error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 提交成功后 Rollback 为空操作
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(73491018)`); err != nil {
		return false, err
	}

	// 同 SHA256 已导入:幂等跳过,不比对行内容(哈希即内容标识)。
	var existingDate time.Time
	if err := tx.QueryRow(ctx,
		`SELECT snapshot_date FROM price_snapshots WHERE file_sha256 = $1`,
		batch.FileSHA256).Scan(&existingDate); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("查询既有快照失败: %w", err)
	}
	if !existingDate.IsZero() {
		if !existingDate.Equal(batch.SnapshotDate) {
			return false, errors.New("同一文件已用于其他快照日期,不能幂等跳过")
		}
		return true, nil
	}

	var snapshotID int64
	if !batch.AllowRevision {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_snapshots WHERE snapshot_date=$1)`, batch.SnapshotDate).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return false, errors.New("同日已有不同批次；使用 -revision 新建不可变修订，不覆盖旧批次")
		}
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO price_snapshots (snapshot_date, file_sha256) VALUES ($1, $2) RETURNING id`,
		batch.SnapshotDate, batch.FileSHA256).Scan(&snapshotID)
	if err != nil {
		return false, fmt.Errorf("创建快照批次失败(同日期已有不同内容批次?): %w", err)
	}
	for _, row := range batch.Rows {
		carried := row.ObservedAt.Before(batch.SnapshotDate)
		sourceSnapshotID := snapshotID
		if carried {
			// 四列 CSV 不能证明新的历史观察;只有数据库中确有同价同源同日旧记录才允许沿用。
			if err := tx.QueryRow(ctx, `SELECT COALESCE(p.source_snapshot_id, p.snapshot_id)
				FROM prices p JOIN price_snapshots s ON s.id = p.snapshot_id
				WHERE p.sku=$1 AND p.price_cny=$2 AND p.source=$3 AND p.observed_at=$4
				AND s.snapshot_date <= $5 ORDER BY s.snapshot_date DESC, s.id DESC LIMIT 1`,
				row.SKU, row.PriceCNY, row.Source, row.ObservedAt, batch.SnapshotDate).Scan(&sourceSnapshotID); err != nil {
				return false, fmt.Errorf("沿用 SKU %q 缺少同价同源同采集日的历史记录: %w", row.SKU, err)
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO prices
			 (snapshot_id, sku, price_cny, source, observed_at, price_type, carried_forward, source_snapshot_id)
			 VALUES ($1, $2, $3, $4, $5, 'bootstrap', $6, $7)`,
			snapshotID, row.SKU, row.PriceCNY, row.Source, row.ObservedAt, carried, sourceSnapshotID); err != nil {
			return false, fmt.Errorf("写入 SKU %q 价格失败(整批回滚): %w", row.SKU, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("提交事务失败: %w", err)
	}
	return false, nil
}

func main() {
	file := flag.String("file", "", "价格 CSV 路径(表头 sku,price_cny,source,captured_at)")
	snapshotDate := flag.String("snapshot-date", "", "批次日期 YYYY-MM-DD;混合采集日期时必填,旧日期须有匹配历史价格")
	revision := flag.Bool("revision", false, "允许同日新增不可变批次；旧快照保持不变")
	flag.Parse()
	if *file == "" {
		log.Fatal("必须用 -file 指定价格 CSV")
	}

	dotenv.Load(".env")
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:复制 .env.example 为 .env,或显式导出 PG_DSN")
	}

	batch, err := loadPriceCSVForSnapshot(*file, *snapshotDate)
	if err != nil {
		log.Fatalf("加载价格 CSV 失败: %v", err)
	}
	batch.AllowRevision = *revision

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	skipped, err := importPrices(ctx, conn, batch)
	if err != nil {
		log.Fatalf("导入失败: %v", err)
	}
	if skipped {
		log.Printf("快照 %s(SHA256 %s)已存在,幂等跳过",
			batch.SnapshotDate.Format("2006-01-02"), batch.FileSHA256[:12])
		return
	}
	log.Printf("导入完成:快照 %s,共 %d 条价格(单事务)",
		batch.SnapshotDate.Format("2006-01-02"), len(batch.Rows))
}
