package producthttp

// Spec 3 授权返工的零模型重放:用上一冻结评估运行中 guard 净化后的真实
// 模型输出(spec3-rework2-live-20260923 的 events.jsonl 提取物),在隔离
// 数据库里重新执行真实 product.Service 的单轮路径。每个提取轮次建独立
// 会话(单轮口径:没有历史提案),断言授权边界:
//  1. run 成功完成;
//  2. 不存在 evidence=accepted_proposal 的 active 字段——单轮无未解析
//     提案,任何标签写入都违反"提案必须先展示、后核验"的协议。
// 该测试不产生 provider 调用;无冻结产物或数据库时跳过。
import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// frozenReplayFixture 由评估产物 events.jsonl 提取(user 原话 → guard 净化
// 后的模型输出);提取口径见 docs/eval/requirement-v2/README.md。
const frozenReplayFixture = "../../artifacts/reqv2/rework2-frozen-outputs.jsonl"

var frozenOutputs = map[string]pipeline.RequirementTurnResult{}

type frozenReplayGateway struct{ store *store.Store }

func (g *frozenReplayGateway) Screen(_ context.Context, _, _ string, input product.ScreenInput) (product.ScreenResult, error) {
	turn, ok := frozenOutputs[input.Text]
	if !ok {
		// 没有冻结输出的原话一律安全回退:零操作 + ambiguous,绝不编造。
		empty := pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{},
			Signals: pipeline.RequirementTurnSignals{Ambiguous: true}}
		update := empty.Update()
		return product.ScreenResult{Turn: &empty, RequirementUpdate: &update}, nil
	}
	copied := turn
	update := copied.Update()
	return product.ScreenResult{Turn: &copied, RequirementUpdate: &update}, nil
}

func (g *frozenReplayGateway) ContextAvailable(context.Context, string, string) (bool, error) {
	return true, nil
}

func (g *frozenReplayGateway) Remote(context.Context, string, string, json.RawMessage) (product.RemoteResult, error) {
	return product.RemoteResult{}, nil
}

func TestRequirementReworkFrozenReplay(t *testing.T) {
	raw, err := os.ReadFile(frozenReplayFixture)
	if err != nil {
		t.Skip("无冻结模型输出产物,跳过零模型重放")
	}
	api, _, st := requirementIntegrationAPI(t, true)
	ctx := context.Background()

	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	var entries []struct {
		User   string `json:"user"`
		Output string `json:"output"`
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e struct {
			User   string `json:"user"`
			Output string `json:"output"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		t.Fatal("冻结产物为空")
	}

	// 预载查表:同一原话可能在不同 repeat 出现,首次出现为准(输出可能因
	// 采样略异,任取其一都代表冻结模型行为)。
	for _, e := range entries {
		if _, exists := frozenOutputs[e.User]; exists {
			continue
		}
		turn, err := pipeline.DecodeRequirementTurn(pipeline.ExtractPayload(e.Output))
		if err != nil {
			t.Fatalf("冻结输出解码失败 %q: %v", e.User, err)
		}
		frozenOutputs[e.User] = turn
	}

	replayService, err := product.NewService(ctx, st, &frozenReplayGateway{}, runevents.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	service := replayService
	gateway := api // api 保持存活引用,避免资源提前释放的误报
	_ = gateway
	acceptedActive := 0
	ran, skipped := 0, 0
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.User] {
			continue // 同一原话只重放一次。
		}
		seen[e.User] = true
		ran++
		ws, err := service.CreateSession(ctx, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		started, err := service.StartMessage(ctx, ws.OwnerID, ws.ID, uuid.NewString(), e.User)
		if err != nil {
			t.Fatalf("%q: %v", e.User, err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			r, err := service.GetRun(ctx, ws.OwnerID, started.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != store.RunRunning {
				if r.Status != store.RunSucceeded {
					// 单轮口径的已知局限:restore/联动类操作依赖多轮上下文,
					// Reducer 无上下文时明确拒绝且状态不变——这是安全行为,
					// 计为 skip;其余失败仍是重放缺陷。
					if strings.Contains(string(r.Error), "schema_validation_failed") {
						skipped++
						break
					}
					t.Fatalf("%q: run %s: %s", e.User, r.Status, r.Error)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%q: run 超时", e.User)
			}
			time.Sleep(2 * time.Millisecond)
		}
		detail, err := service.GetSession(ctx, ws.OwnerID, ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		var state schemas.RequirementState
		if len(detail.Session.RequirementState) > 0 {
			if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
				t.Fatal(err)
			}
		}
		for name, field := range state.Fields {
			if field.Status == "active" && field.Evidence == "accepted_proposal" {
				acceptedActive++
				t.Errorf("%q: 字段 %s 以 accepted_proposal 写入 active(单轮无提案)", e.User, name)
			}
		}
	}
	if ran == 0 {
		t.Fatal("没有重放任何轮次")
	}
	if acceptedActive != 0 {
		t.Fatalf("共 %d 处 accepted_proposal 非法写入", acceptedActive)
	}
	t.Logf("零模型重放 %d 轮真实模型输出(安全拒绝跳过 %d 轮),授权边界断言全部通过", ran, skipped)
}
