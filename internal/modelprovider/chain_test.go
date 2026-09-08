package modelprovider

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

// fakeChainInner 按调用次数返回预置结果,驱动链的切换分支。
type fakeChainInner struct {
	mu    sync.Mutex
	calls int
	resps []*model.LLMResponse
	errs  []error
}

func (f *fakeChainInner) Name() string { return "fake-chain-inner" }

func (f *fakeChainInner) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		f.mu.Lock()
		index := f.calls
		f.calls++
		f.mu.Unlock()
		if index >= len(f.errs) {
			yield(nil, errors.New("fake-chain-inner: 预置序列耗尽"))
			return
		}
		var resp *model.LLMResponse
		if f.errs[index] == nil {
			resp = f.resps[index]
		}
		yield(resp, f.errs[index])
	}
}

func (f *fakeChainInner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func okResponse() *model.LLMResponse {
	return &model.LLMResponse{Content: genai.NewContentFromText("OK", genai.RoleModel)}
}

func quotaErr(role Role) error {
	return upstream.New(string(ProviderBailian), string(role), upstream.KindAuthentication, 403, "PERMISSION_DENIED",
		errors.New("Free quota exhausted"))
}

func newTestChain(t *testing.T, role Role, inners []model.LLM) *chainModel {
	t.Helper()
	chain := &chainModel{role: role}
	for _, inner := range inners {
		candidate := &chainCandidate{name: "model-" + inner.Name()}
		candidate.build = func(context.Context) (model.LLM, error) { return inner, nil }
		chain.candidates = append(chain.candidates, candidate)
	}
	return chain
}

func request() *model.LLMRequest {
	return &model.LLMRequest{Model: "stale-name", Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}}
}

func TestChainSwitchesOnQuotaExhaustion(t *testing.T) {
	primary := &fakeChainInner{errs: []error{quotaErr(RoleBuilder)}}
	backup := &fakeChainInner{resps: []*model.LLMResponse{okResponse(), okResponse()}, errs: []error{nil, nil}}
	chain := newTestChain(t, RoleBuilder, []model.LLM{primary, backup})

	var got *model.LLMResponse
	for resp, err := range chain.GenerateContent(context.Background(), request(), false) {
		if err != nil {
			t.Fatalf("应切换到备用候选:%v", err)
		}
		got = resp
	}
	if got == nil {
		t.Fatal("未收到响应")
	}
	if primary.callCount() != 1 || backup.callCount() != 1 {
		t.Fatalf("调用次数不符:primary=%d backup=%d", primary.callCount(), backup.callCount())
	}
	// 首候选已耗尽:下一次请求直接落到备用。
	for _, err := range chain.GenerateContent(context.Background(), request(), false) {
		if err != nil {
			t.Fatalf("第二次请求应直达备用:%v", err)
		}
	}
	if backup.callCount() != 2 {
		t.Fatalf("备用被再次调用一次,实际 %d", backup.callCount())
	}
}

func TestChainDoesNotSwitchOnTransientError(t *testing.T) {
	rateLimited := upstream.New(string(ProviderBailian), string(RoleBuilder), upstream.KindRateLimit, 429, "Throttling", errors.New("429"))
	primary := &fakeChainInner{errs: []error{rateLimited}}
	backup := &fakeChainInner{resps: []*model.LLMResponse{okResponse()}, errs: []error{nil}}
	chain := newTestChain(t, RoleBuilder, []model.LLM{primary, backup})

	for _, err := range chain.GenerateContent(context.Background(), request(), false) {
		if err == nil {
			t.Fatal("429 不得切换,应原样上抛")
		}
		var ue *upstream.Error
		if !errors.As(err, &ue) || ue.HTTPStatus != 429 {
			t.Fatalf("上抛错误应保持原样:%v", err)
		}
	}
	if backup.callCount() != 0 {
		t.Fatalf("429 时备用不得被调用,实际 %d", backup.callCount())
	}
}

func TestChainBuildFailureSkipsCandidate(t *testing.T) {
	backup := &fakeChainInner{resps: []*model.LLMResponse{okResponse()}, errs: []error{nil}}
	chain := &chainModel{role: RoleScreening}
	broken := &chainCandidate{name: "broken-model", build: func(context.Context) (model.LLM, error) {
		return nil, errors.New("无效模型名")
	}}
	good := &chainCandidate{name: "good-model", build: func(context.Context) (model.LLM, error) { return backup, nil }}
	chain.candidates = []*chainCandidate{broken, good}

	for resp, err := range chain.GenerateContent(context.Background(), request(), false) {
		if err != nil {
			t.Fatalf("构建失败应跳过候选:%v", err)
		}
		if resp == nil {
			t.Fatal("未收到响应")
		}
	}
}

func TestChainAllExhausted(t *testing.T) {
	first := &fakeChainInner{errs: []error{quotaErr(RoleScreening)}}
	second := &fakeChainInner{errs: []error{quotaErr(RoleScreening)}}
	chain := newTestChain(t, RoleScreening, []model.LLM{first, second})

	var lastErr error
	for _, err := range chain.GenerateContent(context.Background(), request(), false) {
		lastErr = err
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "全部候选不可用") {
		t.Fatalf("全耗尽应返回汇总错误:%v", lastErr)
	}
}

func TestParseModelChain(t *testing.T) {
	got := parseModelChain(" a , b,,a\n, c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("parseModelChain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseModelChain[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if parseModelChain(" , ") != nil {
		t.Fatal("空链应返回 nil")
	}
}

func TestSwitchWorthy(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   bool
	}{
		{403, "PERMISSION_DENIED", true},
		{404, "ModelNotFound", true},
		{403, "AllocationQuota", true},
		{429, "Throttling", false},
		{400, "InvalidParameter", false},
		{0, "", false},
	}
	for _, tc := range cases {
		err := upstream.New("bailian", "builder", upstream.KindUnknown, tc.status, tc.code, errors.New("x"))
		if got := switchWorthy(err); got != tc.want {
			t.Errorf("switchWorthy(%d,%q) = %v, want %v", tc.status, tc.code, got, tc.want)
		}
	}
	if switchWorthy(errors.New("普通错误")) {
		t.Error("非 upstream 错误不得触发切换")
	}
}
