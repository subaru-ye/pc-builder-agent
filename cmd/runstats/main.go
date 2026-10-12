// runstats 是对 PG_DSN 指向库的 agent_runs 只读成本巡检:分布看 p50/p95/max
// 不看均值,Top-N 高成本 run 是循环失控的第一线索(评估与可观测性纪律)。
// 不写库、不调用模型。运行方式(从仓库根):
//
//	go run ./cmd/runstats
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql 驱动注册(driver name: pgx)

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
)

func main() {
	dotenv.Load(".env")

	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:复制 .env.example 为 .env,或显式导出 PG_DSN")
	}

	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("打开数据库连接失败: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.Ping(); err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

	fmt.Fprintln(w, "按 kind/status 的 token 分布(仅统计已结束且落库 tokens 的 run):")
	fmt.Fprintln(w, "kind\tstatus\truns\ttokens_p50\ttokens_p95\ttokens_max\ttokens_sum\t模型调用p95\t耗时ms_p95")
	rows, err := conn.Query(`SELECT kind, status, count(*),
		coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY tokens), 0),
		coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY tokens), 0),
		coalesce(max(tokens), 0), coalesce(sum(tokens), 0),
		coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY model_calls), 0),
		coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms), 0)
		FROM agent_runs WHERE tokens IS NOT NULL
		GROUP BY kind, status ORDER BY kind, status`)
	if err != nil {
		log.Fatalf("查询分布失败: %v", err)
	}
	for rows.Next() {
		var kind, status string
		var runs int
		var p50, p95, mx, sum, mp95, dp95 float64
		if err := rows.Scan(&kind, &status, &runs, &p50, &p95, &mx, &sum, &mp95, &dp95); err != nil {
			log.Fatalf("读取分布失败: %v", err)
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\n", kind, status, runs, p50, p95, mx, sum, mp95, dp95)
	}
	rows.Close()
	w.Flush()
	fmt.Println()

	fmt.Fprintln(w, "Top 10 高成本 run(异常高成本≈循环失控信号,结合 retry/model_calls 归因):")
	fmt.Fprintln(w, "started_at(本地)\tkind\tstatus\ttokens\tmodel\ttool\tsearch\tpage\tretries\tms\trun_id")
	rows, err = conn.Query(`SELECT started_at, kind, status,
		coalesce(tokens, 0), coalesce(model_calls, 0), coalesce(tool_calls, 0),
		coalesce(search_calls, 0), coalesce(page_calls, 0), coalesce(retry_count, 0),
		coalesce(duration_ms, 0), id
		FROM agent_runs ORDER BY tokens DESC NULLS LAST, model_calls DESC NULLS LAST LIMIT 10`)
	if err != nil {
		log.Fatalf("查询 Top run 失败: %v", err)
	}
	printed := 0
	for rows.Next() {
		var started sql.NullTime
		var kind, status, runID string
		var tokens, model, tool, search, page, retries int
		var ms int64
		if err := rows.Scan(&started, &kind, &status, &tokens, &model, &tool, &search, &page, &retries, &ms, &runID); err != nil {
			log.Fatalf("读取 Top run 失败: %v", err)
		}
		when := "-"
		if started.Valid {
			when = started.Time.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			when, kind, status, tokens, model, tool, search, page, retries, ms, runID)
		printed++
	}
	rows.Close()
	w.Flush()
	if printed == 0 {
		fmt.Println("(库中尚无带 tokens 的 run,无可统计样本)")
	}
}
