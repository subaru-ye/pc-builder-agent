// evalpreference 是偏好记忆确定性评估入口(阶段四基线 + Builder 交接):
//
//	check          — 零网络自检场景注册表(六类覆盖、ID 唯一);
//	run            — 在一次性 peval_prefeval_* 临时库上执行六类场景并落 report.json/report.md,
//	                 门禁失败时以非零码退出。DSN 取 -dsn 或 PG_TEST_DSN,只接受 localhost 服务器。
//	handoff-check  — 交接场景注册表自检(四类断言维度覆盖、ID 唯一);
//	handoff-run   — 在一次性 peval_handoff_* 临时库上执行四类交接场景(scripted builder
//	                 观测确认事务冻结并派发的完整 PlanningInput),门禁失败非零退出。
//
// 全程零模型、零外部网络;需求状态由 scripted screening 经真实产品管道产生。
// 不评估自动提取、初筛注入或 Builder 模型注入,PM-STALE 仅验证白名单拒绝写入。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
)

func main() {
	mode := flag.String("mode", "check", "check | run | handoff-check | handoff-run")
	out := flag.String("out", "", "run 模式产物目录(必须不存在)")
	dsn := flag.String("dsn", os.Getenv("PG_TEST_DSN"), "PostgreSQL 服务器 DSN(仅 localhost;临时库由本入口创建并删除)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	switch *mode {
	case "check":
		if err := planningeval.CheckPreferenceMemoryEval(); err != nil {
			fmt.Fprintln(os.Stderr, "check:", err)
			os.Exit(1)
		}
		fmt.Println("preference eval check: 场景注册表完整(六类覆盖)")
	case "run":
		if *out == "" {
			fmt.Fprintln(os.Stderr, "run 模式需要 -out(必须不存在的目录)")
			os.Exit(2)
		}
		if _, err := os.Stat(*out); err == nil {
			fmt.Fprintf(os.Stderr, "输出目录已存在,拒绝覆盖: %s\n", *out)
			os.Exit(2)
		}
		if *dsn == "" {
			fmt.Fprintln(os.Stderr, "run 模式需要 -dsn 或 PG_TEST_DSN(localhost PostgreSQL 服务器)")
			os.Exit(2)
		}
		command := fmt.Sprintf("PG_TEST_DSN=%s go run ./cmd/evalpreference -mode run -out %s",
			maskedDSN(*dsn), *out)
		report, err := planningeval.RunPreferenceMemoryEval(ctx, *dsn)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run:", err)
			os.Exit(1)
		}
		if err := planningeval.WritePrefEvalReport(*out, report, command); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		for _, c := range report.Cases {
			fmt.Printf("%-12s %-6s %s\n", c.ID, c.Category, c.Summary)
		}
		for _, v := range report.GateVerdicts {
			fmt.Printf("gate %-22s %s %s %v\n", v.Name, v.Actual, v.Threshold, v.Pass)
		}
		fmt.Printf("report: %s | gate_passed=%v\n", *out+"/report.md", report.GatePassed)
		if !report.GatePassed {
			os.Exit(1)
		}
	case "handoff-check":
		if err := planningeval.CheckPreferenceHandoffEval(); err != nil {
			fmt.Fprintln(os.Stderr, "handoff-check:", err)
			os.Exit(1)
		}
		fmt.Println("preference handoff eval check: 场景注册表完整(四类交接断言维度覆盖)")
	case "handoff-run":
		if *out == "" {
			fmt.Fprintln(os.Stderr, "handoff-run 模式需要 -out(必须不存在的目录)")
			os.Exit(2)
		}
		if _, err := os.Stat(*out); err == nil {
			fmt.Fprintf(os.Stderr, "输出目录已存在,拒绝覆盖: %s\n", *out)
			os.Exit(2)
		}
		if *dsn == "" {
			fmt.Fprintln(os.Stderr, "handoff-run 模式需要 -dsn 或 PG_TEST_DSN(localhost PostgreSQL 服务器)")
			os.Exit(2)
		}
		command := fmt.Sprintf("PG_TEST_DSN=%s go run ./cmd/evalpreference -mode handoff-run -out %s",
			maskedDSN(*dsn), *out)
		report, err := planningeval.RunPreferenceHandoffEval(ctx, *dsn)
		if err != nil {
			fmt.Fprintln(os.Stderr, "handoff-run:", err)
			os.Exit(1)
		}
		if err := planningeval.WritePrefEvalReport(*out, report, command); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		for _, c := range report.Cases {
			fmt.Printf("%-12s %-14s %s\n", c.ID, c.Category, c.Summary)
		}
		for _, v := range report.GateVerdicts {
			fmt.Printf("gate %-22s %s %s %v\n", v.Name, v.Actual, v.Threshold, v.Pass)
		}
		fmt.Printf("report: %s | gate_passed=%v\n", *out+"/report.md", report.GatePassed)
		if !report.GatePassed {
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "未知 mode %q(仅 check|run|handoff-check|handoff-run)\n", *mode)
		os.Exit(2)
	}
}

// maskedDSN 隐藏密码,报告里的复现命令不落凭据。
func maskedDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return "***"
	}
	if _, set := u.User.Password(); set {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}
