package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func TestOwnedScreeningGrounding(t *testing.T) {
	const cpu = `{"schema_version":1,"budget_cny":6000,"use_case":{"type":"general"},"existing_parts":["cpu"],"owned_parts":[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1}],"budget_basis":"new_purchase"}`
	const gpu = `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"},"existing_parts":["gpu"],"budget_basis":"new_purchase"}`
	for _, tc := range []struct {
		name, reply      string
		sources, missing []string
	}{
		{"missing basis", cpu, []string{"配日常办公电脑，预算6000元，已有一颗AMD Ryzen 5 7600 CPU，其他配件都没有。"}, []string{"budget_basis"}},
		{"missing model", gpu, []string{"已有显卡，玩2K游戏，新增购买预算8000元。"}, []string{"owned_parts.gpu.model"}},
		{"invented model", cpu, []string{"已有CPU，新增购买预算6000元。"}, []string{"owned_parts.cpu.model"}},
		{"two missing", cpu, []string{"有CPU，预算6000元。"}, []string{"owned_parts.cpu.model", "budget_basis"}},
		{"known model and basis", cpu, []string{"已有一颗 AMD Ryzen 5 7600，新增购买预算6000元。"}, nil},
		{"multiple turns", cpu, []string{"已有 AMD Ryzen 5 7600，预算6000元。", "只算新增购买费用。"}, nil},
		{"negated basis", cpu, []string{"已有 AMD Ryzen 5 7600，6000元不是新增预算。"}, []string{"budget_basis"}},
		{"full build", strings.ReplaceAll(cpu, "new_purchase", "full_build"), []string{"已有 AMD Ryzen 5 7600，6000元包含已有CPU价值。"}, nil},
		{"new build", `{"budget_cny":6000,"use_case":{"type":"general"}}`, []string{"新装办公电脑6000元"}, nil},
		{"question", "请提供已有显卡型号。", []string{"已有显卡"}, nil},
		{"change", `{"intent":"swap_part","existing_parts":["gpu"]}`, []string{"换显卡"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, missing := guardOwnedScreening(tc.reply, tc.sources)
			if !reflect.DeepEqual(missing, tc.missing) {
				t.Fatalf("missing=%v want=%v", missing, tc.missing)
			}
			if len(missing) == 0 && text != tc.reply {
				t.Fatal("changed valid output")
			}
			if len(missing) > 0 && (extractJSONObject(text) != nil || strings.Contains(text, "6000") || strings.Contains(text, "分辨率")) {
				t.Fatalf("not a focused question: %s", text)
			}
		})
	}
}

func TestOwnedScreeningPreservesKnownInformation(t *testing.T) {
	input := `{"budget_cny":6000,"use_case":{"type":"general"},"existing_parts":["case"],"owned_parts":[{"category":"case","model":"Fractal Design Terra"}],"budget_basis":"new_purchase"}`
	text, missing := guardOwnedScreening(input, []string{"已有 Fractal Design Terra，预算6000元包含已有机箱价值。"})
	if len(missing) != 0 {
		t.Fatalf("重复追问已知信息: %v", missing)
	}
	var output struct {
		Basis string `json:"budget_basis"`
	}
	if json.Unmarshal([]byte(text), &output) != nil || output.Basis != "full_build" {
		t.Fatalf("没有按用户口径修正: %s", text)
	}
	text, missing = guardOwnedScreening(`{"budget_cny":6000,"use_case":{"type":"general"},"existing_parts":["gpu"]}`, []string{"已有显卡，新增购买预算6000元，办公。"})
	if !reflect.DeepEqual(missing, []string{"owned_parts.gpu.model"}) || strings.Contains(text, "预算") {
		t.Fatalf("已知口径被再次追问: %s %v", text, missing)
	}
}

func TestOwnedScreeningDraftMissingFields(t *testing.T) {
	const knownCPU = `"existing_parts":["cpu"],"owned_parts":[{"category":"cpu","model":"Intel Core i5-12400F"}]`
	for _, tc := range []struct {
		fields  string
		missing []string
	}{
		{`"use_case":{"type":"general"}`, []string{"budget_cny"}},
		{`"budget_cny":5000`, []string{"use_case"}},
		{`"budget_cny":5000,"use_case":{"type":"gaming"}`, []string{"resolution"}},
	} {
		text, missing := guardOwnedScreening("{"+knownCPU+","+tc.fields+"}", []string{"已有 Intel Core i5-12400F，预算5000元只算新购费用。"})
		if !reflect.DeepEqual(missing, tc.missing) || extractJSONObject(text) != nil {
			t.Fatalf("partial draft escaped: %s %v", text, missing)
		}
	}
}

func TestBudgetBasisConflictsAndNegation(t *testing.T) {
	for _, text := range []string{"预算不包括已有CPU价值", "预算不计入已有配件价值", "预算不包含已有件"} {
		if got := explicitBudgetBasis([]string{text}); got != "new_purchase" {
			t.Fatalf("否定包含被误判: %q %s", text, got)
		}
	}
	for _, text := range []string{"预算6000元", "其他配件都要新买", "不是新增购买预算", "不只是新增预算", "是否只算新购费用", "新增预算还是整机总预算", "新增预算6000元。整机总预算也是6000元。"} {
		if got := explicitBudgetBasis([]string{text}); got != "" {
			t.Fatalf("%q inferred %s", text, got)
		}
	}
}

type guardTestModel struct {
	text  string
	calls int
}

func (m *guardTestModel) Name() string { return "guard-test" }
func (m *guardTestModel) GenerateContent(_ context.Context, _ *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		if stream {
			panic("unchecked partial output")
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText(m.text, genai.RoleModel)}, nil)
	}
}

// 验证产品与 dev UI 共用的 ADK 入口：可见事件和 OutputKey 都必须是安全追问。
func TestScreeningGuardRunsBeforeEventsAndOutputKey(t *testing.T) {
	for _, product := range []bool{false, true} {
		m := &guardTestModel{text: `{"schema_version":1,"budget_cny":7000,"use_case":{"type":"general"},"existing_parts":["cpu"],"owned_parts":[{"category":"cpu","model":"Intel Core i5-12400F"}],"budget_basis":"new_purchase"}`}
		var a agent.Agent
		var err error
		if product {
			a, err = NewProductScreening(m)
		} else {
			a, err = NewScreening(m)
		}
		if err != nil {
			t.Fatal(err)
		}
		sessions := session.InMemoryService()
		r, err := runner.New(runner.Config{AppName: "guard-test", Agent: a, SessionService: sessions, AutoCreateSession: true})
		if err != nil {
			t.Fatal(err)
		}
		input := "已有CPU，新增购买预算7000元，办公。"
		ctx := context.Background()
		if product {
			ctx = WithScreeningSources(ctx, []string{input})
			input = "助手：例如 Intel Core i5-12400F。\n用户：" + input
		}
		var raw string
		ctx = WithScreeningObserver(ctx, func(text string, _ []string) { raw = text })
		for ev, err := range r.Run(ctx, "user", "session", genai.NewContentFromText(input, genai.RoleUser), agent.RunConfig{}) {
			if err != nil {
				t.Fatal(err)
			}
			if ev != nil && ev.Author == a.Name() && ev.Content != nil {
				if got := screeningText(ev.Content); got != "请提供已有CPU的完整型号。" {
					t.Fatalf("unsafe event: %s", got)
				}
			}
		}
		stored, err := sessions.Get(ctx, &session.GetRequest{AppName: "guard-test", UserID: "user", SessionID: "session"})
		if err != nil {
			t.Fatal(err)
		}
		value, err := stored.Session.State().Get(stateKeyRequirementSpec)
		if err != nil || value != "请提供已有CPU的完整型号。" {
			t.Fatalf("unsafe OutputKey: %v %v", value, err)
		}
		if m.calls != 1 || raw != m.text || !json.Valid([]byte(raw)) {
			t.Fatal("lost raw trace or added model calls")
		}
		// 第二轮只补型号，必须复用第一轮的预算口径，不能让用户再完整说一遍。
		next := "已有CPU的型号是 Intel Core i5-12400F。"
		if product {
			ctx = WithScreeningSources(ctx, []string{"已有CPU，新增购买预算7000元，办公。", next})
		}
		for ev, err := range r.Run(ctx, "user", "session", genai.NewContentFromText(next, genai.RoleUser), agent.RunConfig{}) {
			if err != nil {
				t.Fatal(err)
			}
			if ev != nil && ev.Author == a.Name() && ev.Content != nil && screeningText(ev.Content) != m.text {
				t.Fatalf("补全后仍追问: %s", screeningText(ev.Content))
			}
		}
		stored, err = sessions.Get(ctx, &session.GetRequest{AppName: "guard-test", UserID: "user", SessionID: "session"})
		if err != nil {
			t.Fatal(err)
		}
		value, err = stored.Session.State().Get(stateKeyRequirementSpec)
		if err != nil || value != m.text || m.calls != 2 {
			t.Fatalf("补全后需求单未生效: %v %v", value, err)
		}
	}
}

// 显式指定已保存运行，零模型复核当前程序对原始模型回复的处理结果。
// 不替代 eval replay（后者验证完整题目哈希与判卷），也不改写首跑记录。
func TestScreeningGuardSavedRun(t *testing.T) {
	dir := os.Getenv("SCREENING_GUARD_RUN_DIR")
	if dir == "" {
		t.Skip("set SCREENING_GUARD_RUN_DIR for a saved-run guard audit")
	}
	read := func(name string, value any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, value); err != nil {
			t.Fatal(err)
		}
	}
	type savedCase struct {
		ID, Input, Stage string
		Turns            []struct{ Input string } `json:"turns"`
	}
	var suite struct {
		Cases []savedCase `json:"cases"`
	}
	var meta struct {
		Repeats int `json:"requested_seeds"`
	}
	read("cases.json", &suite)
	read("meta.json", &meta)
	inputs := map[string]savedCase{}
	for _, c := range suite.Cases {
		if c.Stage == "screening" {
			inputs[c.ID] = c
		}
	}
	f, err := os.Open(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(f)
	seen := map[string]bool{}
	type savedOutput struct {
		Text          string        `json:"text"`
		ModelText     string        `json:"model_text"`
		ModelAttempts []string      `json:"model_attempts"`
		Missing       []string      `json:"missing_fields"`
		GuardText     string        `json:"guard_text"`
		UserSources   []string      `json:"user_sources"`
		Turns         []savedOutput `json:"turns"`
	}
	check := func(key string, output savedOutput, sources []string) {
		t.Helper()
		if output.ModelText == "" {
			t.Fatalf("missing model text: %s", key)
		}
		raw := output.ModelText
		if len(output.ModelAttempts) > 0 {
			if output.ModelAttempts[len(output.ModelAttempts)-1] != raw || len(output.ModelAttempts) > 2 {
				t.Fatalf("invalid attempt evidence: %s", key)
			}
			raw = normalizeDraftProtocol(raw)
		}
		text, missing := guardOwnedScreening(raw, sources)
		want := output.Text
		if output.GuardText != "" {
			want = output.GuardText
		}
		if text != want || !reflect.DeepEqual(missing, output.Missing) {
			t.Errorf("current guard differs: %s", key)
		}
	}
	for {
		var record struct {
			ID        string       `json:"case_id"`
			Seed      int          `json:"seed"`
			Screening *savedOutput `json:"screening"`
		}
		if err := decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if record.Screening == nil {
			continue
		}
		input, ok := inputs[record.ID]
		key := record.ID + "/" + strconv.Itoa(record.Seed)
		if !ok || seen[key] || record.Seed < 1 || record.Seed > meta.Repeats {
			t.Fatalf("incomplete or duplicate trace: %s", record.ID)
		}
		seen[key] = true
		if len(input.Turns) == 0 {
			check(key, *record.Screening, []string{input.Input})
			continue
		}
		if len(input.Turns) != len(record.Screening.Turns) {
			t.Fatalf("missing dialogue rounds: %s", key)
		}
		for i, output := range record.Screening.Turns {
			// 完整有界上下文重建由正常 replay 负责；本审计还要求来源是本题截至本轮的有序用户原话。
			at := 0
			for _, source := range output.UserSources {
				for at <= i && input.Turns[at].Input != source {
					at++
				}
				if at > i {
					t.Fatalf("invalid user source: %s/%d", key, i+1)
				}
				at++
			}
			if len(output.UserSources) == 0 || output.UserSources[len(output.UserSources)-1] != input.Turns[i].Input {
				t.Fatalf("missing latest input: %s/%d", key, i+1)
			}
			check(key+"/turn"+strconv.Itoa(i+1), output, output.UserSources)
		}
	}
	if len(inputs) == 0 || meta.Repeats < 1 || len(seen) != len(inputs)*meta.Repeats {
		t.Fatalf("incomplete screening run: %d", len(seen))
	}
	t.Logf("current guard reproduced %d saved screening outputs and missing-field lists", len(seen))
}
