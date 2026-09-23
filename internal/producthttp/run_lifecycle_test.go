package producthttp

// F1 取消生命周期离线验证:阻塞 gateway 模拟 10 分钟 A2A 调用,用户取消后
// run 必须落 interrupted(非 technical_fault),会话锁立即释放可再发消息。
import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const cancelBlockMarker = "开始取消测试"

type blockingLifecycleGateway struct{ *requirementReplayGateway }

func (g *blockingLifecycleGateway) Screen(ctx context.Context, owner, sessionID string, input product.ScreenInput) (product.ScreenResult, error) {
	if strings.Contains(input.Text, cancelBlockMarker) {
		<-ctx.Done()
		return product.ScreenResult{}, ctx.Err()
	}
	return g.requirementReplayGateway.Screen(ctx, owner, sessionID, input)
}

func (blockingLifecycleGateway) Remote(ctx context.Context, _, _ string, _ json.RawMessage) (product.RemoteResult, error) {
	<-ctx.Done()
	return product.RemoteResult{}, ctx.Err()
}

func TestRunCancelLifecycle(t *testing.T) {
	_, _, st := requirementIntegrationAPI(t)
	raw, err := os.ReadFile("testdata/requirement_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture requirementReplay
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc, err := product.NewService(ctx, st, &blockingLifecycleGateway{&requirementReplayGateway{store: st, fixture: fixture}}, runevents.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })
	owner := "offline-cancel-owner"
	ws, err := svc.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal := func(runID string) store.AgentRun {
		t.Helper()
		for i := 0; i < 500; i++ {
			run, e := svc.GetRun(ctx, owner, runID)
			if e != nil {
				t.Fatal(e)
			}
			if run.Status != store.RunRunning {
				return run
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("取消后 run 未进入终态")
		return store.AgentRun{}
	}

	// screening run:取消 → interrupted,problem 为 run_cancelled 而非技术故障。
	screening, err := svc.StartMessage(ctx, owner, ws.ID, uuid.NewString(), cancelBlockMarker)
	if err != nil {
		t.Fatal(err)
	}
	if _, requested, e := svc.RequestCancel(ctx, owner, ws.ID, screening.Run.ID); e != nil || !requested {
		t.Fatalf("RequestCancel requested=%v err=%v", requested, e)
	}
	run := waitTerminal(screening.Run.ID)
	if run.Status != store.RunInterrupted {
		t.Fatalf("取消后 screening run 状态=%s, want interrupted:%s", run.Status, run.Error)
	}
	var problem struct{ Code string }
	if e := json.Unmarshal(run.Error, &problem); e != nil || problem.Code != "run_cancelled" {
		t.Fatalf("取消 problem=%s err=%v", run.Error, e)
	}

	// 取消后同会话可继续对话并走到 requirement_ready。
	_, err = svc.StartMessage(ctx, owner, ws.ID, uuid.NewString(), "预算8000，玩游戏，要安静一点，尽量用N卡，帮朋友装机，2K分辨率，配件全部新买")
	if err != nil {
		t.Fatalf("取消后会话仍被锁:%v", err)
	}
	for i := 0; i < 500; i++ {
		detail, e := svc.GetSession(ctx, owner, ws.ID)
		if e != nil {
			t.Fatal(e)
		}
		if detail.ActiveRun == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
		if i == 499 {
			t.Fatal("screening 未完成")
		}
	}

	// build run:确认后进入阻塞的 Remote,取消 → interrupted,会话再次解锁。
	confirmed, err := svc.StartConfirm(ctx, owner, ws.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, requested, e := svc.RequestCancel(ctx, owner, ws.ID, confirmed.Run.ID); e != nil || !requested {
		t.Fatalf("build RequestCancel requested=%v err=%v", requested, e)
	}
	run = waitTerminal(confirmed.Run.ID)
	if run.Status != store.RunInterrupted {
		t.Fatalf("取消后 build run 状态=%s, want interrupted:%s", run.Status, run.Error)
	}
	if _, err = svc.StartMessage(ctx, owner, ws.ID, uuid.NewString(), "预算改成6000"); err != nil {
		t.Fatalf("build 取消后会话仍被锁:%v", err)
	}
}
