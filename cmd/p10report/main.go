// p10report 校验 P10 真实模型矩阵和三人盲评是否达到产品发布验收门禁。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/subaru-ye/pc-builder-agent/internal/p10report"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("p10report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	livePath := flags.String("live", "", "live-results.json 路径")
	humanPath := flags.String("human", "", "human-results.json 路径")
	final := flags.Bool("final", false, "要求 Live 与真人报告同时通过最终封板门禁")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *livePath == "" && *humanPath == "" {
		_, _ = fmt.Fprintln(stderr, "必须至少提供 -live 或 -human")
		return 2
	}
	if *final && (*livePath == "" || *humanPath == "") {
		_, _ = fmt.Fprintln(stderr, "-final 必须同时提供 -live 和 -human")
		return 2
	}

	var errs []error
	if *livePath != "" {
		report, err := decodeLiveFile(*livePath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "解码 live 报告: %v\n", err)
			return 2
		}
		errs = append(errs, p10report.ValidateLive(report)...)
	}
	if *humanPath != "" {
		report, err := decodeHumanFile(*humanPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "解码 human 报告: %v\n", err)
			return 2
		}
		errs = append(errs, p10report.ValidateHuman(report)...)
	}
	if len(errs) > 0 {
		_, _ = fmt.Fprintln(stderr, "P10 报告门禁未通过:")
		_, _ = fmt.Fprintln(stderr, p10report.FormatErrors(errs))
		return 1
	}
	switch {
	case *livePath != "" && *humanPath != "":
		_, _ = fmt.Fprintln(stdout, "P10 封板门禁通过:Live Pass³ + 真人 3/3 可直接照买")
	case *livePath != "":
		_, _ = fmt.Fprintln(stdout, "P10 机器门禁通过:Harness v2 Live Pass³ + 成本与时延目标")
	default:
		_, _ = fmt.Fprintln(stdout, "P10 真人门禁通过:3/3 可直接照买")
	}
	return 0
}

func decodeLiveFile(path string) (p10report.LiveReport, error) {
	file, err := os.Open(path)
	if err != nil {
		return p10report.LiveReport{}, err
	}
	defer func() { _ = file.Close() }()
	return p10report.DecodeLive(file)
}

func decodeHumanFile(path string) (p10report.HumanReport, error) {
	file, err := os.Open(path)
	if err != nil {
		return p10report.HumanReport{}, err
	}
	defer func() { _ = file.Close() }()
	return p10report.DecodeHuman(file)
}
