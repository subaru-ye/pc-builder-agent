// P3 embedding 生成器(P3-语义选件设计.md §3/§8):把 parts 全量拼接 embedding
// 文本(canonical specs + scripts/data/styles 风格标注),调端点向量化后回填
// parts.embedding / embedding_text。幂等全量重算;任一 SKU 失败整批报错,
// 单事务写库不留半批状态。
//
// 运行方式(从仓库根,PG_DSN/DASHSCOPE_API_KEY 来自 .env 或环境变量):
//
//	go run ./cmd/embedparts                         # 全量生成回填
//	go run ./cmd/embedparts -query "要安静的显卡"    # 检索模式:top-N 人工核对(DoD)
//	go run ./cmd/embedparts -query "白色海景房" -category case -top 5
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/embedding"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// defaultBaseURL 百炼 OpenAI 兼容端点默认地址(与 cmd/host 同口径)。
const defaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

func main() {
	stylesPath := flag.String("styles", filepath.Join("scripts", "data", "styles", "styles.json"), "风格标注文件路径")
	query := flag.String("query", "", "检索模式:查询文本(非空时不做生成,只打印 top-N 供人工核对)")
	category := flag.String("category", "", "检索模式:品类过滤(可选,八大类之一)")
	top := flag.Int("top", 5, "检索模式:返回条数")
	flag.Parse()

	dotenv.Load(".env")
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:复制 .env.example 为 .env,或显式导出 PG_DSN")
	}
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		log.Fatal("DASHSCOPE_API_KEY 未设置")
	}
	baseURL := os.Getenv("DASHSCOPE_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	embeddingModelName := os.Getenv("EMBEDDING_MODEL")
	if embeddingModelName == "" {
		embeddingModelName = embedding.DefaultModel
	}
	client := embedding.NewClient(baseURL, apiKey, embeddingModelName, store.EmbeddingDims)

	ctx := context.Background()
	if *query != "" {
		if err := runQuery(ctx, client, dsn, *query, *category, *top); err != nil {
			log.Fatalf("检索失败: %v", err)
		}
		return
	}
	if err := runGenerate(ctx, client, dsn, *stylesPath); err != nil {
		log.Fatalf("生成失败: %v", err)
	}
}

// dbPart 生成模式所需的 parts 行(active 全量)。
type dbPart struct {
	SKU      string
	Category schemas.Category
	Brand    string
	Model    string
	Specs    map[string]any
}

// runGenerate 全量生成:读库 + 风格标注 → 拼接文本 → 批量向量化 → 单事务回填。
func runGenerate(ctx context.Context, client *embedding.Client, dsn, stylesPath string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	parts, err := loadActiveParts(ctx, conn)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("parts 表无 active 记录,先跑 cmd/importparts")
	}

	styles, err := loadStyles(stylesPath)
	if err != nil {
		return err
	}
	// 标注文件里出现库中不存在的 SKU 视为 typo,立即报错(不静默丢标注)。
	known := make(map[string]bool, len(parts))
	for _, p := range parts {
		known[p.SKU] = true
	}
	var unknown []string
	for sku := range styles {
		if !known[sku] {
			unknown = append(unknown, sku)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("风格标注含库中不存在的 SKU(疑似 typo): %v", unknown)
	}

	texts := make([]string, len(parts))
	annotated := 0
	for i, p := range parts {
		var st *styleEntry
		if se, ok := styles[p.SKU]; ok {
			st = &se
			annotated++
		}
		texts[i] = buildEmbeddingText(p.Category, p.Brand, p.Model, p.Specs, st)
	}

	log.Printf("拼接完成:%d 件(含风格标注 %d 件,未标注 %d 件),开始向量化…",
		len(parts), annotated, len(parts)-annotated)
	vecs, err := client.Embed(ctx, texts)
	if err != nil {
		return err
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 提交成功后 Rollback 为空操作

	const updateSQL = `UPDATE parts SET embedding = $1::vector, embedding_text = $2, updated_at = now() WHERE sku = $3`
	for i, p := range parts {
		tag, err := tx.Exec(ctx, updateSQL, store.VectorLiteral(vecs[i]), texts[i], p.SKU)
		if err != nil {
			return fmt.Errorf("回填 SKU %q 失败(整批回滚): %w", p.SKU, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("回填 SKU %q 影响 %d 行,期望 1(整批回滚)", p.SKU, tag.RowsAffected())
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	log.Printf("回填完成:%d 件 embedding(%d 维)+ embedding_text", len(parts), store.EmbeddingDims)
	return nil
}

// loadActiveParts 读 active 零件全量(按 sku 排序,批次顺序稳定可复现)。
func loadActiveParts(ctx context.Context, conn *pgx.Conn) ([]dbPart, error) {
	rows, err := conn.Query(ctx,
		`SELECT sku, category, brand, model, specs FROM parts WHERE active ORDER BY sku`)
	if err != nil {
		return nil, fmt.Errorf("查询 parts 失败: %w", err)
	}
	defer rows.Close()

	var out []dbPart
	for rows.Next() {
		var (
			p   dbPart
			raw []byte
		)
		if err := rows.Scan(&p.SKU, &p.Category, &p.Brand, &p.Model, &raw); err != nil {
			return nil, fmt.Errorf("读取 parts 行失败: %w", err)
		}
		if err := json.Unmarshal(raw, &p.Specs); err != nil {
			return nil, fmt.Errorf("SKU %q specs 解码失败: %w", p.SKU, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 parts 失败: %w", err)
	}
	return out, nil
}

// runQuery 检索模式:查询文本向量化后走 store.SemanticCandidates,打印 top-N
// (含 similarity 与 match_text)供 DoD 人工核对命中的参数字段。
func runQuery(ctx context.Context, client *embedding.Client, dsn, query, category string, top int) error {
	s, err := store.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()

	vec, err := client.EmbedOne(ctx, query)
	if err != nil {
		return err
	}
	res, err := s.SemanticCandidates(ctx, store.SemanticQuery{
		Category:       schemas.Category(category),
		QueryEmbedding: vec,
		TopN:           top,
	})
	if err != nil {
		return err
	}

	fmt.Printf("查询: %q(品类过滤: %s)\n", query, orDash(category))
	if res.SnapshotDate != nil {
		fmt.Printf("报价快照: %s\n", res.SnapshotDate.Format("2006-01-02"))
	} else {
		fmt.Println("报价快照: 无(价格列为空)")
	}
	for i, c := range res.Candidates {
		price := "-"
		if c.PriceCNY != nil {
			price = *c.PriceCNY
		}
		fmt.Printf("%d. %-40s sim=%.4f  price=%s\n   %s\n", i+1, c.SKU, c.Similarity, price, c.MatchText)
	}
	if res.Truncated > 0 {
		fmt.Printf("(另有 %d 条命中被截断)\n", res.Truncated)
	}
	if len(res.Candidates) == 0 {
		fmt.Println("(无命中:确认已跑过全量生成回填)")
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
