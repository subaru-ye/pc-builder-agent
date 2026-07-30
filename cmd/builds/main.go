// P4 版本树 CLI(用例 C/D/E):回放 / diff / Markdown 导出,直连 DB 读,纯确定性代码。
//
//	go run ./cmd/builds list [-session X]        # 无 -session 列出全部会话
//	go run ./cmd/builds diff -session X -from 1 -to 3
//	go run ./cmd/builds export -session X -version 3 [-o build.md]
//
// 退出码:0 = 成功;1 = 参数错误;2 = 数据或系统错误(会话/版本不存在、DB 不可达等)。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const (
	exitOK       = 0
	exitUsageErr = 1
	exitDataErr  = 2
)

func usage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "用法: builds <list|diff|export> [flags]")
	_, _ = fmt.Fprintln(stderr, "  list   [-session X]                       版本树回放(无 -session 列出会话)")
	_, _ = fmt.Fprintln(stderr, "  diff   -session X -from N -to M           两版本逐品类 diff")
	_, _ = fmt.Fprintln(stderr, "  export -session X -version N [-o file.md] Markdown 配置单导出")
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(exitUsageErr)
	}

	dotenv.Load(".env")
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		_, _ = fmt.Fprintln(os.Stderr, "builds: PG_DSN 未设置:复制 .env.example 为 .env,或显式导出 PG_DSN")
		os.Exit(exitDataErr)
	}

	os.Exit(run(context.Background(), os.Args[1], os.Args[2:], dsn, os.Stdout, os.Stderr))
}

// run 子命令分发;返回进程退出码。
func run(ctx context.Context, sub string, args []string, dsn string, stdout, stderr io.Writer) int {
	st, err := store.New(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}
	defer st.Close()

	switch sub {
	case "list":
		return runList(ctx, st, args, stdout, stderr)
	case "diff":
		return runDiff(ctx, st, args, stdout, stderr)
	case "export":
		return runExport(ctx, st, args, stdout, stderr)
	default:
		usage(stderr)
		return exitUsageErr
	}
}

func runList(ctx context.Context, st *store.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	session := fs.String("session", "", "会话 ID;缺省列出全部会话")
	if err := fs.Parse(args); err != nil {
		return exitUsageErr
	}

	if *session == "" {
		sessions, err := st.Sessions(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
			return exitDataErr
		}
		_, _ = fmt.Fprint(stdout, renderSessions(sessions))
		return exitOK
	}

	builds, err := st.BuildsBySession(ctx, *session)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}
	if len(builds) == 0 {
		_, _ = fmt.Fprintf(stderr, "builds: 会话 %q 没有已落库的版本\n", *session)
		return exitDataErr
	}
	rows, err := decodeBuilds(builds)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}
	_, _ = fmt.Fprint(stdout, renderList(*session, rows))
	return exitOK
}

func runDiff(ctx context.Context, st *store.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	session := fs.String("session", "", "会话 ID(必填)")
	from := fs.Int("from", 0, "基准版本号(必填)")
	to := fs.Int("to", 0, "目标版本号(必填)")
	if err := fs.Parse(args); err != nil {
		return exitUsageErr
	}
	if *session == "" || *from <= 0 || *to <= 0 {
		_, _ = fmt.Fprintln(stderr, "builds: diff 需要 -session、-from、-to(正整数版本号)")
		return exitUsageErr
	}

	fromRow, err := loadVersion(ctx, st, *session, *from)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}
	toRow, err := loadVersion(ctx, st, *session, *to)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}

	out, err := renderDiff(fromRow, toRow)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}
	_, _ = fmt.Fprint(stdout, out)
	return exitOK
}

func runExport(ctx context.Context, st *store.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	session := fs.String("session", "", "会话 ID(必填)")
	version := fs.Int("version", 0, "版本号(必填)")
	outPath := fs.String("o", "", "输出文件路径;缺省写 stdout")
	if err := fs.Parse(args); err != nil {
		return exitUsageErr
	}
	if *session == "" || *version <= 0 {
		_, _ = fmt.Fprintln(stderr, "builds: export 需要 -session、-version")
		return exitUsageErr
	}

	row, err := loadVersion(ctx, st, *session, *version)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}
	names, err := st.PartNames(ctx, row.skus())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}

	md, err := renderExport(row, names)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: %v\n", err)
		return exitDataErr
	}
	if *outPath == "" {
		_, _ = fmt.Fprint(stdout, md)
		return exitOK
	}
	if err := os.WriteFile(*outPath, []byte(md), 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "builds: 写出文件失败: %v\n", err)
		return exitDataErr
	}
	_, _ = fmt.Fprintf(stderr, "builds: 已导出 %s\n", *outPath)
	return exitOK
}

// loadVersion 取指定版本并连同需求单解码为渲染行。
func loadVersion(ctx context.Context, st *store.Store, session string, version int) (buildRow, error) {
	b, err := st.BuildByVersion(ctx, session, version)
	if err != nil {
		return buildRow{}, err
	}
	spec, err := st.RequirementSpecByID(ctx, b.RequirementID)
	if err != nil {
		return buildRow{}, err
	}
	return decodeBuild(b, spec)
}
