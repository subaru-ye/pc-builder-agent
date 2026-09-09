// evalchange 提供零模型规划、显式真实回归和离线逐题比较；不会升级或覆盖基线。
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

type sourceManifest struct {
	Version      int               `json:"version"`
	Scope        string            `json:"scope"`
	Files        map[string]string `json:"files"`
	SHA256       string            `json:"sha256"`
	BinarySHA256 string            `json:"binary_sha256"`
	BuildCommand string            `json:"build_command"`
	BuiltAt      time.Time         `json:"built_at"`
}

// 保守记录本仓库所有 Go 源码以及依赖锁定文件，涵盖未提交改动。
// 当前 eval 没有 go:embed；发现嵌入指令时拒绝宣称输入完整，要求扩展采集逻辑。
func captureSource() (sourceManifest, error) {
	m := sourceManifest{Version: 1, Scope: "repository Go sources and go.mod/go.sum; no go:embed", Files: map[string]string{}, BuildCommand: "go build -trimpath -o <fresh binary> ./cmd/eval (GOWORK=off, GOFLAGS=)"}
	add := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".go") && strings.Contains(string(data), "//go:"+"embed ") {
			return fmt.Errorf("%s 使用嵌入资源，请先完善构建输入采集", path)
		}
		m.Files[filepath.ToSlash(path)] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	}
	for _, file := range []string{"go.mod", "go.sum"} {
		if err := add(file); err != nil {
			return m, err
		}
	}
	for _, root := range []string{"cmd", "internal"} {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".go") {
				if d.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("不支持符号链接源码 %s", path)
				}
				return add(path)
			}
			return nil
		}); err != nil {
			return m, err
		}
	}
	raw, _ := json.Marshal(m.Files)
	m.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	return m, nil
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func sourceMatches(m sourceManifest, binary string) bool {
	if m.Version != 1 || binary == "" || m.BinarySHA256 != binary || len(m.Files) == 0 {
		return false
	}
	raw, err := json.Marshal(m.Files)
	return err == nil && m.SHA256 == fmt.Sprintf("%x", sha256.Sum256(raw))
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	f := flag.NewFlagSet("evalchange", flag.ContinueOnError)
	mode := f.String("mode", "plan", "plan（零调用）/ run（构建、真实执行并比较）/ compare（离线）")
	baseline := f.String("baseline", "", "保存的基线目录")
	candidate := f.String("candidate", "", "compare 模式的候选运行目录")
	out := f.String("out", "artifacts/evalchange", "新产物根目录")
	maxCalls := f.Int64("max-calls", 0, "run 必填：模型与 Embedding 合计逻辑调用上限")
	if err := f.Parse(args); err != nil {
		return 2
	}
	if f.NArg() != 0 || *baseline == "" || (*mode != "plan" && *mode != "run" && *mode != "compare") {
		fmt.Fprintln(os.Stderr, "需要有效 mode 和 baseline，不能有位置参数")
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 2 }
	base, err := evalsuite.AuditRun(*baseline)
	if err != nil {
		return fail(err)
	}
	if err := evalsuite.CheckChangeConditions(base.Meta, base.Meta); err != nil {
		return fail(err)
	}
	if *mode == "compare" {
		if *candidate == "" {
			return fail(fmt.Errorf("compare 需要 candidate"))
		}
		return compare(*baseline, *candidate, *out)
	}
	src, err := captureSource()
	if err != nil {
		return fail(err)
	}
	fmt.Printf("基线 %s；题库 %s；快照 %s；%d 题 × %d 次 = %d 条执行。\n", *baseline, base.Meta.SuiteVersion, base.Meta.SnapshotDate, len(base.Current)/base.Meta.RequestedSeeds, base.Meta.RequestedSeeds, len(base.Current))
	fmt.Printf("Builder=%v；Screening=%v。历史记录按当前判卷复核改变 %d 条（不覆盖）。\n", base.Meta.Models["builder"]["model"], base.Meta.Models["screening"]["model"], base.RegradedTrials)
	var previous sourceManifest
	if raw, err := os.ReadFile(filepath.Join(*baseline, "source.json")); err == nil && json.Unmarshal(raw, &previous) == nil && sourceMatches(previous, base.Meta.Code.BinarySHA256) {
		var changes []string
		for file, hash := range src.Files {
			if previous.Files[file] != hash {
				changes = append(changes, file)
			}
		}
		for file := range previous.Files {
			if _, ok := src.Files[file]; !ok {
				changes = append(changes, file)
			}
		}
		sort.Strings(changes)
		fmt.Printf("已记录构建输入的变化：%d 个文件。\n", len(changes))
		for _, file := range changes {
			fmt.Println(" ", file)
		}
	} else {
		fmt.Println("历史基线缺少与二进制关联的构建输入清单，无法准确列出源码差异。")
	}
	fmt.Println("执行范围：全题库。源码依赖影响尚未自动证明，不自动缩题；实际受影响题以比较报告为准。")
	if *mode == "plan" {
		fmt.Printf("零模型调用。真实执行须显式使用 -mode run -max-calls N；本次拟设上限 %d（0 表示尚未设定）。\n", *maxCalls)
		return 0
	}
	if *maxCalls < 1 {
		return fail(fmt.Errorf("run 必须显式设置正整数 max-calls"))
	}
	fmt.Printf("即将执行，逻辑调用上限 %d；不会修改模型或基线。\n", *maxCalls)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fail(err)
	}
	work, err := os.MkdirTemp(*out, time.Now().Format("20060102-150405")+"-run-")
	if err != nil {
		return fail(err)
	}
	exe := filepath.Join(work, "eval")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fail(err)
	}
	build := exec.Command("go", "build", "-trimpath", "-o", exe, "./cmd/eval")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GOWORK=") && !strings.HasPrefix(value, "GOFLAGS=") {
			build.Env = append(build.Env, value)
		}
	}
	build.Env = append(build.Env, "GOWORK=off", "GOFLAGS=")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fail(err)
	}
	after, err := captureSource()
	if err != nil {
		return fail(err)
	}
	if src.SHA256 != after.SHA256 {
		return fail(fmt.Errorf("构建期间源码变化，未调用模型，请重新执行"))
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		return fail(err)
	}
	src.BinarySHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	src.BuiltAt = time.Now()
	if err := writeJSON(filepath.Join(work, "source.json"), src); err != nil {
		return fail(err)
	}
	pathFile := filepath.Join(work, "result-path.txt")
	profile := base.Meta.HarnessProfile
	cmd := exec.Command(exe, "-mode", "run", "-baseline", *baseline, "-snapshot-date", base.Meta.SnapshotDate, "-seeds", fmt.Sprint(base.Meta.RequestedSeeds), "-attempt-limit", fmt.Sprint(profile.AttemptLimit), fmt.Sprintf("-semantic=%v", profile.Semantic), "-max-calls", fmt.Sprint(*maxCalls), "-out", filepath.Join(work, "runs"), "-result-path", pathFile)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var e *exec.ExitError
		if !errors.As(err, &e) || e.ExitCode() != 1 {
			return fail(err)
		}
	}
	path, err := os.ReadFile(pathFile)
	if err != nil {
		return fail(err)
	}
	runDir := string(path)
	m, _, err := evalsuite.ReadVerifiedRun(runDir)
	if err != nil {
		return fail(err)
	}
	if m.Code == nil || m.Code.BinarySHA256 != src.BinarySHA256 {
		return fail(fmt.Errorf("实际执行程序与构建产物不一致"))
	}
	if err := writeJSON(filepath.Join(runDir, "source.json"), src); err != nil {
		return fail(err)
	}
	return compare(*baseline, runDir, work)
}

func compare(baseline, candidate, root string) int {
	r, err := evalsuite.CompareChangeDirs(baseline, candidate)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	dir, err := os.MkdirTemp(root, time.Now().Format("20060102-150405")+"-comparison-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := evalsuite.WriteChangeReport(dir, r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Printf("报告：%s；退步 %d 题、改善 %d 题、当前失败 %d 题。\n", filepath.Join(dir, "report.md"), r.Regressed, r.Improved, r.RemainingFailures)
	if !r.GatePassed {
		return 1
	}
	return 0
}
