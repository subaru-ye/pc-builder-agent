package evaldesk

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

// These summaries are presentation evidence, never inputs to strict comparison.
// Arbitrary configuration, filesystem paths, source text and Git output are not DTOs.
func change(key, label string, conditions []Condition, keys ...string) ChangeSummary {
	c := ChangeSummary{Key: key, Label: label, State: "same", Details: []ChangeDetail{}, Notes: []string{}}
	unknown := false
	for _, condition := range conditions {
		for _, key := range keys {
			if condition.Key == key {
				unknown = unknown || condition.State == "unknown"
				if condition.State == "changed" {
					c.State = "changed"
				}
			}
		}
	}
	if c.State != "changed" && unknown {
		c.State = "unknown"
	}
	return c
}

func detail(label string, a, b *string) ChangeDetail {
	return ChangeDetail{Label: label, Baseline: a, Candidate: b}
}

func roleLabel(role string) string {
	switch role {
	case "builder":
		return "选配"
	case "screening":
		return "初筛"
	case "embedding":
		return "检索"
	default:
		return "其他角色"
	}
}

var modelFields = []struct{ key, label, kind string }{
	{"model", "模型", "string"}, {"provider", "服务商", "string"},
	{"reasoning_effort", "推理强度", "string"}, {"timeout", "超时", "duration"},
	{"max_retries", "请求重试上限", "number"}, {"session_cache", "会话缓存", "bool"},
	{"dimensions", "向量维度", "number"}, {"model_chain", "自动切换模型", "chain"},
}

// Type-check allowed fields so a nested credential object cannot hide in a model name.
func modelValue(config map[string]any, key, kind string) *string {
	value, ok := config[key]
	if !ok || value == nil {
		return nil
	}
	switch kind {
	case "string", "duration":
		s, ok := value.(string)
		if !ok || len(s) > 256 {
			return nil
		}
		if s == "" {
			return strptr("未设置（已记录为空）")
		}
		if kind == "duration" {
			if duration, err := time.ParseDuration(s); err == nil && duration >= 0 {
				return strptr(fmt.Sprintf("%g 秒", duration.Seconds()))
			}
		}
		if key == "reasoning_effort" {
			labels := map[string]string{"none": "关闭", "minimal": "最低", "low": "低", "medium": "中", "high": "高", "xhigh": "更高", "max": "最高"}
			if label, ok := labels[s]; ok {
				return strptr(label)
			}
		}
		if key == "provider" && s == "bailian" {
			return strptr("阿里云百炼")
		}
		return strptr(s)
	case "bool":
		if b, ok := value.(bool); ok {
			if b {
				return strptr("开启")
			}
			return strptr("关闭")
		}
	case "number":
		var number float64
		switch v := value.(type) {
		case float64:
			number = v
		case int:
			number = float64(v)
		default:
			return nil
		}
		if number >= 0 && number <= 1e9 && math.Trunc(number) == number {
			if key == "dimensions" && number == 0 {
				return strptr("未指定（记录值 0）")
			}
			return strptr(fmt.Sprintf("%.0f", number))
		}
	case "chain":
		items := []string{}
		switch values := value.(type) {
		case []any:
			for _, value := range values {
				item, ok := value.(string)
				if !ok || len(item) > 256 {
					return nil
				}
				items = append(items, item)
			}
		case []string:
			items = values
			for _, item := range items {
				if len(item) > 256 {
					return nil
				}
			}
		default:
			return nil
		}
		if len(items) > 20 {
			return nil
		}
		if len(items) == 0 {
			return strptr("关闭（未配置切换链）")
		}
		return strptr(strings.Join(items, " → "))
	}
	return nil
}

func displayedModel(run *savedRun, role string) *string {
	for _, model := range run.summary.Versions.Models {
		if model.Role == role {
			return strptr(model.Model)
		}
	}
	return nil
}

func modelChanges(a, b *savedRun, conditions []Condition) ChangeSummary {
	out := change("models", "模型与调用设置", conditions, "models", "modelParameters")
	changedLabels, sameRoles := []string{}, []string{}
	missing := false
	for _, role := range []string{"builder", "screening", "embedding"} {
		roleChanges := 0
		for _, field := range modelFields {
			av, bv := modelValue(a.meta.Models[role], field.key, field.kind), modelValue(b.meta.Models[role], field.key, field.kind)
			if field.key == "model" {
				if av == nil {
					av = displayedModel(a, role)
				}
				if bv == nil {
					bv = displayedModel(b, role)
				}
			}
			if deref(av) != deref(bv) {
				missing = missing || av == nil || bv == nil
				label := roleLabel(role) + " · " + field.label
				out.Details = append(out.Details, detail(label, av, bv))
				changedLabels = append(changedLabels, roleLabel(role)+field.label)
				roleChanges++
			}
		}
		if roleChanges == 0 && a.meta.Models[role] != nil && b.meta.Models[role] != nil && hashJSON(a.meta.Models[role]) == hashJSON(b.meta.Models[role]) {
			sameRoles = append(sameRoles, roleLabel(role))
		}
	}
	switch {
	case len(changedLabels) > 0:
		out.Summary = strings.Join(changedLabels, "、") + "有变化"
	case out.State == "changed":
		out.Summary = "模型配置有变化，变化字段未对前端开放或无法解析"
	case out.State == "same":
		out.Summary = "已记录的模型和调用设置相同"
	default:
		out.Summary = "模型配置未完整记录，不能确认设置是否相同"
	}
	// Compare the private remainder without projecting its keys or values. Even
	// a sole endpoint/credential-field change must not be reported as no change.
	private := func(run *savedRun) map[string]map[string]any {
		out := map[string]map[string]any{}
		for role, config := range run.meta.Models {
			out[role] = map[string]any{}
			for key, value := range config {
				visible := false
				for _, field := range modelFields {
					visible = visible || (key == field.key && modelValue(config, key, field.kind) != nil)
				}
				if !visible || (role != "builder" && role != "screening" && role != "embedding") {
					out[role][key] = value
				}
			}
		}
		return out
	}
	hiddenChanged := len(a.meta.Models) > 0 && len(b.meta.Models) > 0 && hashJSON(private(a)) != hashJSON(private(b))
	if hiddenChanged {
		out.Notes = append(out.Notes, "另有未对前端开放或无法解析的配置字段发生变化；其内容不展示，不能仅凭可见模型名称判断完整配置相同。")
	}
	if !hiddenChanged && len(changedLabels) == 0 && out.State == "changed" {
		out.Summary = "配置记录有变化，可见字段的可读值相同"
		out.Notes = append(out.Notes, "记录形式可能不同，不能据此宣称实际调用设置发生了变化。")
	}
	if missing {
		out.Notes = append(out.Notes, "部分字段仅一侧有记录；未记录不表示未设置，不能据此断言设置被增加或删除。")
	}
	if len(sameRoles) > 0 {
		out.Notes = append(out.Notes, strings.Join(sameRoles, "、")+"的已记录配置相同。")
	}
	return out
}

var commitID = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
var digestID = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

// Only read an already stored object. No ref names, revisions, shell expansion,
// lazy fetching, checkout, hooks, diff drivers or writes are involved.
func (s *Store) commitDescription(commit *string) *string {
	if commit == nil {
		return nil
	}
	if !commitID.MatchString(*commit) {
		return strptr("提交标题不可读取（记录的提交标识格式无效）")
	}
	info := s.commitSummary(commit)
	if info.Status != "available" {
		return strptr("本机未找到该提交的标题和时间")
	}
	when, err := time.Parse(time.RFC3339, deref(info.CommittedAt))
	if err != nil {
		return strptr("本机提交说明无法解析")
	}
	return strptr(when.In(time.FixedZone("北京", 8*60*60)).Format("2006-01-02 15:04:05") + " · " + deref(info.Subject))
}

type savedSource struct {
	Version      int               `json:"version"`
	Scope        string            `json:"scope"`
	Files        map[string]string `json:"files"`
	SHA256       string            `json:"sha256"`
	BinarySHA256 string            `json:"binary_sha256"`
}

func (s *Store) sourceFiles(run *savedRun) (map[string]string, string) {
	raw, err := s.read(run.dir, "source.json")
	if err != nil {
		return nil, "源码清单未记录或不可读取"
	}
	var source savedSource
	if json.Unmarshal(raw, &source) != nil || source.Version != 1 || source.Scope != "repository Go sources and go.mod/go.sum; no go:embed" || len(source.Files) == 0 || len(source.Files) > 10000 || !digestID.MatchString(source.BinarySHA256) || source.BinarySHA256 != deref(run.summary.Versions.Binary) {
		return nil, "源码清单无法通过格式或程序身份校验"
	}
	files, _ := json.Marshal(source.Files)
	if source.SHA256 != fmt.Sprintf("%x", sha256.Sum256(files)) {
		return nil, "源码清单无法通过内容校验"
	}
	for name, hash := range source.Files {
		if path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00\r\n") || (!strings.HasPrefix(name, "cmd/") && !strings.HasPrefix(name, "internal/") && name != "go.mod" && name != "go.sum") || !digestID.MatchString(hash) || ((strings.HasPrefix(name, "cmd/") || strings.HasPrefix(name, "internal/")) && !strings.HasSuffix(name, ".go")) {
			return nil, "源码清单含不支持的路径或内容标识"
		}
	}
	for _, required := range []string{"go.mod", "go.sum", "cmd/eval/main.go"} {
		if source.Files[required] == "" {
			return nil, "源码清单未覆盖必要构建输入"
		}
	}
	return source.Files, fmt.Sprintf("已校验 %d 个构建输入文件", len(source.Files))
}

func dirtyText(value *bool) *string {
	if value == nil {
		return nil
	}
	if *value {
		return strptr("有未提交改动")
	}
	return strptr("无未提交改动")
}

func (s *Store) codeChanges(a, b *savedRun, conditions []Condition) ChangeSummary {
	out := change("code", "程序与代码", conditions, "binary", "commit", "source")
	ab, bb := a.summary.Versions.Binary, b.summary.Versions.Binary
	switch {
	case ab == nil || bb == nil:
		out.Summary = "至少一侧实际程序标识未记录，不能确认程序是否变化"
	case *ab != *bb:
		out.Summary = "实际执行的程序不同"
	default:
		out.Summary = "实际执行的程序相同"
		if deref(a.summary.Versions.Commit) != deref(b.summary.Versions.Commit) {
			out.Summary += "，启动时仓库提交不同"
		}
	}
	ac, bc := s.commitDescription(a.summary.Versions.Commit), s.commitDescription(b.summary.Versions.Commit)
	out.Details = append(out.Details, detail("启动时仓库提交（本机 Git）", ac, bc), detail("启动时工作区", dirtyText(a.summary.Versions.Dirty), dirtyText(b.summary.Versions.Dirty)))
	out.Notes = append(out.Notes, "提交标题来自本机 Git，只说明评估启动时的仓库状态；不代表实际执行程序的完整源码改动。")
	if (a.summary.Versions.Dirty != nil && *a.summary.Versions.Dirty) || (b.summary.Versions.Dirty != nil && *b.summary.Versions.Dirty) {
		out.Notes = append(out.Notes, "运行时存在未提交改动，提交说明不能覆盖这些改动。")
	}
	af, an := s.sourceFiles(a)
	bf, bn := s.sourceFiles(b)
	out.Details = append(out.Details, detail("冻结源码清单", strptr(an), strptr(bn)))
	if af == nil || bf == nil {
		missing := []string{}
		if af == nil {
			missing = append(missing, "基线"+an)
		}
		if bf == nil {
			missing = append(missing, "候选"+bn)
		}
		out.Notes = append(out.Notes, strings.Join(missing, "；")+"，无法列出两次实际程序之间的源码文件改动。")
		return out
	}
	names := map[string]bool{}
	for name := range af {
		names[name] = true
	}
	for name := range bf {
		names[name] = true
	}
	changed := []string{}
	for name := range names {
		if af[name] != bf[name] {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	if len(changed) == 0 {
		out.Notes = append(out.Notes, "已校验的源码清单内容相同；程序文件不同仍可能包含构建工具或环境差异。")
	} else {
		out.Summary += fmt.Sprintf("；冻结构建输入有 %d 个文件变化", len(changed))
		for i, name := range changed {
			if i >= 100 {
				out.Notes = append(out.Notes, fmt.Sprintf("另有 %d 个变化文件未展开。", len(changed)-100))
				break
			}
			av, bv := "原内容", "已修改"
			if af[name] == "" {
				av, bv = "不存在", "新增"
			}
			if bf[name] == "" {
				bv = "已删除"
			}
			out.Details = append(out.Details, detail("源码 · "+clean(name), strptr(av), strptr(bv)))
		}
		out.Notes = append(out.Notes, "文件变化来自两侧与程序身份匹配的冻结构建输入清单；清单包含仓库 Go 文件和依赖锁定文件，不证明每个变化文件都影响了评估结果。")
	}
	return out
}

func promptComponentLabel(key string) string {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return "其他提示词组件"
	}
	names := map[string]string{"system": "系统指令", "new_build_system": "新装机系统指令", "format_retry": "格式重试指令", "output_contract_attempt_1": "第 1 次生成输出约定", "output_contract_attempt_2": "第 2 次生成输出约定", "output_contract_attempt_3": "第 3 次生成输出约定"}
	name := names[parts[1]]
	if name == "" {
		name = "其他组件"
	}
	return roleLabel(parts[0]) + " · " + name
}

func promptChanges(a, b *savedRun, conditions []Condition) ChangeSummary {
	out := change("prompt", "提示词", conditions, "prompt")
	out.Summary = map[string]string{"same": "已记录的静态提示词相同", "changed": "静态提示词有变化", "unknown": "提示词独立版本未完整记录，无法判断是否变化"}[out.State]
	if a.meta.Prompts != nil && b.meta.Prompts != nil && a.summary.Verified && b.summary.Verified {
		keys := map[string]bool{}
		for key := range a.meta.Prompts.Components {
			keys[key] = true
		}
		for key := range b.meta.Prompts.Components {
			keys[key] = true
		}
		ordered := []string{}
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			av, bv := a.meta.Prompts.Components[key], b.meta.Prompts.Components[key]
			if av == bv {
				continue
			}
			left, right := "原内容", "已修改"
			if av == "" {
				left, right = "不存在", "新增"
			}
			if bv == "" {
				right = "已删除"
			}
			out.Details = append(out.Details, detail(promptComponentLabel(key), strptr(left), strptr(right)))
		}
	} else if out.State == "changed" {
		out.Notes = append(out.Notes, "提示词组件缺失或未通过运行证据校验，无法确认具体改动组件。")
	}
	out.Notes = append(out.Notes, "这里只比较已保存的静态提示词；动态用户输入和输出在单题详情查看。")
	return out
}

func (s *Store) changeSummaries(a, b *savedRun, out CompareResponse) []ChangeSummary {
	result := []ChangeSummary{modelChanges(a, b, out.Conditions), s.codeChanges(a, b, out.Conditions)}
	suite := change("suite", "题库与题目", out.Conditions, "suite", "suiteHash")
	suite.Baseline, suite.Candidate = a.summary.Versions.Suite, b.summary.Versions.Suite
	if len(a.cases) > 0 && len(b.cases) > 0 {
		suite.Summary = fmt.Sprintf("%d 道共同题；新增 %d 道、删除 %d 道、修改 %d 道", out.Counts["common"], out.Counts["added"], out.Counts["removed"], out.Counts["modified"])
		if out.Counts["added"]+out.Counts["removed"]+out.Counts["modified"] == 0 {
			suite.Summary = fmt.Sprintf("%d 道题的内容和期望均相同", out.Counts["common"])
		}
	} else {
		suite.Summary = "冻结题库未完整记录，不能确认哪些题发生变化"
	}
	result = append(result, suite)
	data := change("data", "商品数据", out.Conditions, "snapshotDate", "data", "frozenData")
	data.Baseline, data.Candidate = a.summary.Versions.SnapshotDate, b.summary.Versions.SnapshotDate
	af, bf := frozenDataHash(a), frozenDataHash(b)
	switch {
	case af != nil && bf != nil && *af == *bf:
		data.Summary = "两次产物保存的完整商品目录、规格与价格相同"
	case af != nil && bf != nil:
		data.Summary = "产物中的商品快照内容不同（含标识、日期、目录、规格及价格）"
	default:
		data.Summary = "完整商品数据未保存齐全，不能只凭日期判断数据相同"
	}
	if a.summary.Versions.DataFingerprint == nil || b.summary.Versions.DataFingerprint == nil {
		data.Notes = append(data.Notes, "历史运行时数据指纹未完整记录；以上目录核对由已保存产物现场计算。")
	}
	data.Notes = append(data.Notes, "向量索引状态未记录，无法判断检索索引是否变化。")
	result = append(result, data, promptChanges(a, b, out.Conditions))
	for _, field := range []struct {
		key, label string
		a, b       *string
	}{
		{"harness", "执行机制", profileText(a), profileText(b)},
		{"repeats", "每题重复次数", repeatsText(a), repeatsText(b)},
		{"grader", "判卷口径", a.summary.Versions.Grader, b.summary.Versions.Grader},
	} {
		item := change(field.key, field.label, out.Conditions, field.key)
		item.Baseline, item.Candidate = field.a, field.b
		item.Summary = map[string]string{"same": field.label + "相同", "changed": field.label + "有变化", "unknown": field.label + "未完整记录"}[item.State]
		if field.key == "grader" {
			item.Notes = append(item.Notes, "两侧原成绩保留各自历史判卷；对比结果统一使用 "+evalsuite.CurrentGraderVersion+" 在内存复核。")
		}
		result = append(result, item)
	}
	return result
}
