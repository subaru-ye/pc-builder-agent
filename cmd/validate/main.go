// P1 校验 CLI(E1):go run ./cmd/validate --input build.json
// 读取手写配置 JSON → PG_DSN 连库解析 SKU → ResolvedBuild → 12 条规则引擎,
// stdout 仅输出 ValidationReport JSON(诊断信息一律走 stderr)。
//
// 退出码(冻结):0 = 通过(overall pass|review);1 = 存在错误级违规(overall fail);
// 2 = 输入或系统错误(文件不可读、schema error、未知 SKU、数据库不可达等)。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/rules"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const (
	exitOK        = 0 // overall pass|review,无错误级违规
	exitViolation = 1 // overall fail,存在错误级违规
	exitInputErr  = 2 // 输入或系统错误,未产出报告
)

// run 全流程:解码 → 解析 → 校验 → 输出报告;返回进程退出码。
// stdout 只在成功产出报告时写入(且仅写报告 JSON),错误一律进 stderr。
func run(ctx context.Context, inputPath, dsn string, stdout, stderr io.Writer) int {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: 读取输入失败: %v\n", err)
		return exitInputErr
	}
	sel, err := schemas.DecodeBuildSelection(data)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: %v\n", err)
		return exitInputErr
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: %v\n", err)
		return exitInputErr
	}
	defer st.Close()

	build, err := st.ResolveBuild(ctx, sel)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: %v\n", err)
		return exitInputErr
	}

	report, err := rules.NewDefaultEngine().Validate(build)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: %v\n", err)
		return exitInputErr
	}

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: 序列化报告失败: %v\n", err)
		return exitInputErr
	}
	_, _ = fmt.Fprintln(stdout, string(out))

	if report.OverallStatus == schemas.OverallFail {
		return exitViolation
	}
	return exitOK
}

func main() {
	input := flag.String("input", "", "手写配置 JSON 路径(BuildSelection schema_version=1)")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "validate: 必须用 --input 指定配置 JSON")
		os.Exit(exitInputErr)
	}

	dotenv.Load(".env")
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "validate: PG_DSN 未设置:复制 .env.example 为 .env,或显式导出 PG_DSN")
		os.Exit(exitInputErr)
	}

	os.Exit(run(context.Background(), *input, dsn, os.Stdout, os.Stderr))
}
