// eval 评估 runner(P13):在钉死的价格快照上运行评估集,产出 Pass@1 报告。
// 仅在人工执行时调用上游模型;run 模式写完整轨迹,replay 模式零模型调用重放断言。
// 运行方式(从仓库根,需先起 docker-compose 的 postgres 并导入零件/价格数据):
//
//	go run ./cmd/eval -snapshot-date 2026-09-01 -mode run
//	go run ./cmd/eval -mode replay -dir artifacts/eval/<时间戳>
//
// 退出码:0 全绿;1 存在失败或 data-error;2 用法/配置错误(与 cmd/validate 对齐)。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const defaultSuitePath = "internal/evalsuite/testdata/suites/v1.4.json"

const defaultCasesDir = "internal/evalsuite/testdata/cases"

func main() {
	os.Exit(run())
}

func run() int {
	var (
		suiteVersion = flag.String("suite-version", "", "freeze 模式必填:新评估集版本名")
		suitePath    = flag.String("suite", defaultSuitePath, "评估集版本清单;仅执行列出的题目并核验哈希")
		mode         = flag.String("mode", "run", "运行模式:run / replay / check(离线校验) / freeze(冻结新版本)")
		snapshotDate = flag.String("snapshot-date", "", "run 模式必填:钉死的快照日期 YYYY-MM-DD")
		casesDir     = flag.String("cases", defaultCasesDir, "评估用例目录")
		outRoot      = flag.String("out", "artifacts/eval", "run 模式的输出根目录")
		replayDir    = flag.String("dir", "", "replay 模式必填:首跑结果目录")
		caseTimeout  = flag.Duration("timeout", 15*time.Minute, "单条用例执行上限")
		seeds        = flag.Int("seeds", 1, "每条用例独立重复次数;>1 即 Pass^k 口径(全 seed pass 且零 veto)")
	)
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "不支持位置参数")
		return 2
	}

	switch *mode {
	case "freeze":
		if err := evalsuite.FreezeSuite(*casesDir, *suitePath, *suiteVersion, time.Now().Format(time.RFC3339)); err != nil {
			log.Println(err)
			return 2
		}
		fmt.Println("评估集已冻结:", *suitePath)
		return 0
	case "check":
		suite, cases, err := evalsuite.LoadSuite(*suitePath, *casesDir)
		if err != nil {
			log.Println(err)
			return 2
		}
		fmt.Printf("评估集 %s: %d 条用例，哈希校验通过（零模型调用）\n", suite.Manifest.Version, len(cases))
		return 0
	case "run":
		return runReal(*snapshotDate, *casesDir, *outRoot, *caseTimeout, *seeds, *suitePath)
	case "replay":
		return runReplay(*replayDir)
	default:
		fmt.Fprintf(os.Stderr, "-mode=%q 无效:须为 run 或 replay\n", *mode)
		return 2
	}
}

func runReal(dateText, casesDir, outRoot string, caseTimeout time.Duration, seeds int, suitePath string) int {
	if dateText == "" {
		fmt.Fprintln(os.Stderr, "-snapshot-date 必填:评估结论只对钉死的快照批次负责(P13 §3.4)")
		return 2
	}
	date, err := time.Parse("2006-01-02", dateText)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-snapshot-date=%q 无效:%v\n", dateText, err)
		return 2
	}

	if seeds < 1 {
		fmt.Fprintln(os.Stderr, "-seeds 必须大于零")
		return 2
	}
	suite, cases, err := evalsuite.LoadSuite(suitePath, casesDir)
	if err != nil {
		log.Println(err)
		return 2
	}
	suiteHash, err := suite.Hash()
	if err != nil {
		log.Println(err)
		return 2
	}
	dotenv.Load(".env")
	ctx := context.Background()

	builderCfg, err := modelprovider.Load(modelprovider.RoleBuilder)
	if err != nil {
		log.Println(err)
		return 2
	}
	embeddingCfg, err := modelprovider.Load(modelprovider.RoleEmbedding)
	if err != nil {
		log.Println(err)
		return 2
	}
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "PG_DSN 未设置:评估需要零件库(docker-compose up -d 后见 .env.example)")
		return 2
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		log.Println("连接零件库失败:", err)
		return 2
	}
	defer st.Close()

	// 钉死快照:候选、校验与报价共用同一批次;库内无该日期批次即显式失败。
	cat, err := st.CatalogSnapshotByDate(ctx, date)
	if err != nil {
		log.Println("钉死快照失败:", err)
		return 2
	}
	snapshotView := evalsuite.NewSnapshotView(cat)

	embeddingClient, err := modelprovider.NewEmbedding(embeddingCfg)
	if err != nil {
		log.Println("创建 embedding 客户端失败:", err)
		return 2
	}
	planner, err := buildharness.NewCandidatePlanner(pinnedCatalogSource{store: st, pinned: cat}, embeddingClient)
	if err != nil {
		log.Println(err)
		return 2
	}
	builderModel, err := modelprovider.NewChat(ctx, builderCfg, "eval")
	if err != nil {
		log.Println("创建生成模型失败:", err)
		return 2
	}
	harness, err := buildharness.New(buildharness.Config{
		Model:    builderModel,
		Planner:  planner,
		Repairer: buildharness.NewRepairPlanner(),
		Eval:     validate.New(pinnedResolver{store: st, snap: cat.Snapshot}),
	})
	if err != nil {
		log.Println(err)
		return 2
	}

	// 用例含 screening 阶段时装配初筛 runner(独立 InMemory 会话,零 Redis 依赖)。
	var screeningRunner evalsuite.ScreeningRunner
	var screeningCfg modelprovider.Config
	for _, c := range cases {
		if c.Stage == evalsuite.StageScreening {
			screeningCfg, err = modelprovider.Load(modelprovider.RoleScreening)
			if err != nil {
				log.Println(err)
				return 2
			}
			screeningRunner, err = newADKScreeningRunner(ctx, screeningCfg)
			if err != nil {
				log.Println(err)
				return 2
			}
			break
		}
	}

	identity := codeIdentity()
	meta := evalsuite.ReportMeta{
		RecordSchemaVersion: 1,
		SuiteVersion:        suite.Manifest.Version, SuiteSHA256: suiteHash, RequestedSeeds: seeds, Code: &identity,
		Models:         map[string]map[string]any{"builder": modelIdentity(builderCfg), "embedding": modelIdentity(embeddingCfg)},
		SnapshotDate:   snapshotView.SnapshotDate,
		BuilderModel:   builderCfg.ModelDescription(),
		EmbeddingModel: string(embeddingCfg.Provider) + "/" + embeddingCfg.Model,
		GeneratedAt:    time.Now(),
		Mode:           "run",
	}
	if screeningRunner != nil {
		meta.ScreeningModel = screeningCfg.ModelDescription()
		meta.Models["screening"] = modelIdentity(screeningCfg)
	}
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		log.Println(err)
		return 2
	}
	outDir, err := os.MkdirTemp(outRoot, meta.GeneratedAt.Format("20060102-150405")+"-")
	if err != nil {
		log.Println("创建输出目录失败:", err)
		return 2
	}
	// Persist the exam and provenance before the first model request.
	if err := suite.Write(filepath.Join(outDir, "cases.json")); err != nil {
		log.Println(err)
		return 2
	}
	if err := writeMeta(outDir, meta); err != nil {
		log.Println(err)
		return 2
	}
	records, err := evalsuite.RunCases(ctx, cases, evalsuite.Deps{
		Harness:   harness,
		Snapshot:  snapshotView,
		Screening: screeningRunner,
		Timeout:   caseTimeout,
		Seeds:     seeds,
		OnRecord: func(record evalsuite.CaseRecord) {
			fmt.Printf("%s seed=%d passed=%v data_error=%v duration_ms=%d\n", record.CaseID, record.Seed, record.Verdict.Passed, record.Verdict.DataError, record.DurationMS)
		},
	})
	if err != nil {
		log.Println(err)
		return 2
	}

	summary := evalsuite.Summarize(meta, records)
	if err := summary.WriteJSONL(outDir); err != nil {
		log.Println(err)
		return 2
	}
	if err := summary.WriteReport(outDir); err != nil {
		log.Println(err)
		return 2
	}
	if err := writeMeta(outDir, meta); err != nil {
		log.Println(err)
		return 2
	}

	fmt.Printf("评估完成:%s\n  报告:%s\n  Pass@1 = %.1f%%(通过 %d / 失败 %d / data-error %d,共 %d 条)\n",
		meta.SnapshotDate, filepath.Join(outDir, "report.md"),
		summary.PassRate*100, summary.Passed, summary.Failed, summary.DataErrors, summary.Total)
	if !summary.AllGreen() {
		return 1
	}
	return 0
}

// runReplay 从首跑轨迹重放断言矩阵:零模型调用,逐用例结论必须与首跑一致。
func runReplay(dir string) int {
	if dir == "" {
		fmt.Fprintln(os.Stderr, "-dir 必填:首跑结果目录(含 results.jsonl)")
		return 2
	}
	records, err := evalsuite.ReadRecords(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		log.Println(err)
		return 2
	}

	meta, err := readMeta(dir)
	if err != nil {
		log.Println(err)
		return 2
	}
	if meta.RecordSchemaVersion > 1 {
		log.Println("不支持的记录格式版本")
		return 2
	}
	meta.ReplaySkipped = nil
	savedCases := map[string]evalsuite.Case{}
	if meta.SuiteSHA256 != "" {
		saved, cases, err := evalsuite.ReadSuiteSnapshot(filepath.Join(dir, "cases.json"), meta.SuiteSHA256)
		if err != nil {
			log.Println(err)
			return 2
		}
		if saved.Manifest.Version != meta.SuiteVersion {
			log.Println("suite version mismatch")
			return 2
		}
		for _, c := range cases {
			savedCases[c.ID] = c
		}
		if err := checkRecords(records, savedCases, meta.RequestedSeeds); err != nil {
			log.Println(err)
			return 2
		}
	}
	var mismatches []string
	replayed := make([]evalsuite.CaseRecord, 0, len(records))
	for _, record := range records {
		if record.RunErr != "" {
			// 执行错误不视作有效模型回复;保留错误,不将残留文本重判为成功。
			replayed = append(replayed, record)
			continue
		}
		c := evalsuite.Case{ID: record.CaseID, Title: record.Title, Stage: record.Stage, Expect: record.Expect}
		if saved, ok := savedCases[record.CaseID]; ok {
			c = saved
		}
		var verdict evalsuite.Verdict
		if record.Stage == evalsuite.StageScreening {
			if record.Screening == nil {
				if meta.RecordSchemaVersion >= 1 {
					log.Printf("用例 %s seed=%d 缺少 screening 回复原文", record.CaseID, record.Seed)
					return 2
				}
				meta.ReplaySkipped = append(meta.ReplaySkipped, fmt.Sprintf("%s/seed=%d", record.CaseID, record.Seed))
				replayed = append(replayed, record)
				continue
			}
			verdict = evalsuite.AssertScreeningCase(c, record.Screening.Text)
		} else {
			if record.Result == nil {
				log.Printf("用例 %s 缺少 build 结果且无执行错误", record.CaseID)
				return 2
			}
			if _, ok := savedCases[record.CaseID]; !ok {
				spec, err := schemas.DecodeRequirementSpec(record.Requirement)
				if err != nil {
					log.Printf("用例 %s 需求单解码失败: %v", record.CaseID, err)
					return 2
				}
				c.Requirement = spec
			}
			verdict = evalsuite.AssertCase(c, *record.Result, record.Snapshot)
		}
		attribution := evalsuite.Attribute(verdict.Failures)
		if !reflect.DeepEqual(verdict, record.Verdict) || !reflect.DeepEqual(attribution, record.Attribution) {
			mismatches = append(mismatches, fmt.Sprintf("%s/seed=%d:首跑 verdict=%+v attribution=%+v;重放 verdict=%+v attribution=%+v",
				record.CaseID, record.Seed, record.Verdict, record.Attribution, verdict, attribution))
		}
		record.Verdict = verdict
		record.Attribution = attribution
		replayed = append(replayed, record)
	}

	replayIdentity := codeIdentity()
	meta.ReplayCode = &replayIdentity
	meta.Mode = "replay"
	meta.GeneratedAt = time.Now()
	summary := evalsuite.Summarize(meta, replayed)
	if err := summary.WriteReport(dir); err != nil {
		log.Println(err)
		return 2
	}

	fmt.Printf("重放完成:报告已更新 %s\n", filepath.Join(dir, "report.md"))
	if len(mismatches) > 0 {
		fmt.Fprintln(os.Stderr, "复现性破坏:以下用例重放结论与首跑不一致:")
		for _, m := range mismatches {
			fmt.Fprintln(os.Stderr, "  "+m)
		}
		return 1
	}
	if len(meta.ReplaySkipped) > 0 {
		fmt.Printf("部分重放: %d 条历史 screening 记录未复验;其余可重判记录一致\n", len(meta.ReplaySkipped))
	} else {
		fmt.Printf("可重判记录的断言与归因均与首跑一致(Pass@1 = %.1f%%);执行错误保留原记录\n", summary.PassRate*100)
	}
	return 0
}

// pinnedCatalogSource 把候选视图钉死在指定快照批次:ActiveCatalogSnapshot 语义
// 被替换为"返回钉死批次",语义检索委托真实 store。
type pinnedCatalogSource struct {
	store  *store.Store
	pinned store.CatalogSnapshot
}

func (s pinnedCatalogSource) ActiveCatalogSnapshot(context.Context) (store.CatalogSnapshot, error) {
	return s.pinned, nil
}

func (s pinnedCatalogSource) SemanticCandidates(ctx context.Context, q store.SemanticQuery) (store.SemanticResult, error) {
	return s.store.SemanticCandidates(ctx, q)
}

// pinnedResolver 让校验与报价同样钉死:LatestSnapshot 返回钉死批次,其余委托。
type pinnedResolver struct {
	store *store.Store
	snap  store.Snapshot
}

func (r pinnedResolver) ResolveBuild(ctx context.Context, sel schemas.BuildSelection) (schemas.ResolvedBuild, error) {
	return r.store.ResolveBuild(ctx, sel)
}

func (r pinnedResolver) LatestSnapshot(context.Context) (store.Snapshot, error) {
	return r.snap, nil
}

func (r pinnedResolver) PricesBySnapshot(ctx context.Context, id int64) ([]store.Price, error) {
	return r.store.PricesBySnapshot(ctx, id)
}

func writeMeta(dir string, meta evalsuite.ReportMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: 序列化 meta 失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o644); err != nil {
		return fmt.Errorf("eval: 写入 meta.json 失败: %w", err)
	}
	return nil
}

func readMeta(dir string) (evalsuite.ReportMeta, error) {
	var meta evalsuite.ReportMeta
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		// 旧产物没有 meta.json:以首条记录的快照日期兜底,模型标识留空。
		return meta, fmt.Errorf("eval: %s 缺少 meta.json,无法还原运行口径", dir)
	}
	if err != nil {
		return meta, fmt.Errorf("eval: 读取 meta.json 失败: %w", err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("eval: 解析 meta.json 失败: %w", err)
	}
	return meta, nil
}
