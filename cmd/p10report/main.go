// p10report 校验 P10 真实模型矩阵和三人盲评是否达到阶段 1 封板门禁。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/subaru-ye/pc-builder-agent/internal/p10report"
)

func main() {
	livePath := flag.String("live", "", "live-results.json 路径")
	humanPath := flag.String("human", "", "human-results.json 路径")
	flag.Parse()
	if *livePath == "" || *humanPath == "" {
		log.Fatal("必须同时提供 -live 和 -human")
	}
	liveFile, err := os.Open(*livePath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = liveFile.Close() }()
	live, err := p10report.DecodeLive(liveFile)
	if err != nil {
		log.Fatalf("解码 live 报告: %v", err)
	}
	humanFile, err := os.Open(*humanPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = humanFile.Close() }()
	human, err := p10report.DecodeHuman(humanFile)
	if err != nil {
		log.Fatalf("解码 human 报告: %v", err)
	}
	errs := append(p10report.ValidateLive(live), p10report.ValidateHuman(human)...)
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "P10 封板门禁未通过:")
		fmt.Fprintln(os.Stderr, p10report.FormatErrors(errs))
		os.Exit(1)
	}
	fmt.Println("P10 封板门禁通过:Live Pass³ + 真人 3/3 可直接照买")
}
