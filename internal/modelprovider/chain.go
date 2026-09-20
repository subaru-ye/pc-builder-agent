// 模型额度降级链(P13):chat 角色可配置 *_MODEL_CHAIN(逗号分隔,顺序即优先级)。
// 运行中候选返回 403/404 或配额类错误时,本进程内记下该候选不可用并自动切换
// 下一个;全部候选耗尽才返回错误。跨供应商回退仍然禁止,启动时探测仍然禁止。
package modelprovider

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log"
	"strings"
	"sync"

	"google.golang.org/adk/v2/model"

	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

// parseModelChain 解析逗号分隔的链配置:去空白、去空段、按序去重;空串返回 nil。
func parseModelChain(raw string) []string {
	seen := map[string]bool{}
	var chain []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		chain = append(chain, name)
	}
	return chain
}

// switchWorthy 判断错误是否表示"该候选暂时/永久不可用,应切换下一个":
// 403(百炼 /responses 端点把免费额度耗尽报成 PERMISSION_DENIED)、404(模型
// 不存在或无权限)以及显式配额类机器码。429 限流是瞬时的,不切换。
func switchWorthy(err error) bool {
	var ue *upstream.Error
	if !errors.As(err, &ue) {
		return false
	}
	if ue.HTTPStatus == 403 || ue.HTTPStatus == 404 {
		return true
	}
	code := strings.ToLower(ue.Code)
	return strings.Contains(code, "quota") || strings.Contains(code, "allocation")
}

// transientUpstream 是同模型值得再试一次的瞬时错误:响应体被截断(protocol)、
// 上游不可用(unavailable)与超时(timeout)。openai-go 的 WithMaxRetries 实测
// 不会重发被截断的 200 响应(请求数仍为 1),所以这类失败只能在本层吸收;
// 额度与鉴权类错误不在此列,交给模型链或如实抛给调用方。
func transientUpstream(err error) bool {
	var ue *upstream.Error
	if !errors.As(err, &ue) {
		return false
	}
	switch ue.Kind {
	case upstream.KindProtocol, upstream.KindUnavailable, upstream.KindTimeout:
		return true
	}
	return false
}

// chainCandidate 是链上一个候选;inner 由 factory 惰性构建,构建失败视为该
// 候选不可用(如模型名错误),同样切换下一个。
type chainCandidate struct {
	name  string
	build func(context.Context) (model.LLM, error)

	mu        sync.Mutex
	inner     model.LLM
	exhausted bool
	reason    string
}

func (c *chainCandidate) isExhausted() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exhausted, c.reason
}

func (c *chainCandidate) markExhausted(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.exhausted {
		log.Printf("[modelchain] 候选 %s 不可用,切换下一个:%s", c.name, reason)
	}
	c.exhausted = true
	c.reason = reason
}

func (c *chainCandidate) resolve(ctx context.Context) (model.LLM, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner != nil {
		return c.inner, nil
	}
	inner, err := c.build(ctx)
	if err != nil {
		c.exhausted = true
		c.reason = "构建失败:" + err.Error()
		return nil, err
	}
	c.inner = inner
	return inner, nil
}

// chainModel 是 model.LLM 的降级链实现:对每次请求从首个未耗尽候选开始尝试,
// 遇到 switchWorthy 错误切下一个;非 switch 错误原样上抛(沿用零自动重试纪律)。
type chainModel struct {
	role       Role
	candidates []*chainCandidate
}

func (m *chainModel) Name() string {
	return fmt.Sprintf("chain:%s(%s + %d 个备用)", m.role, m.candidates[0].name, len(m.candidates)-1)
}

func (m *chainModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var exhaustedReasons []string
		for _, candidate := range m.candidates {
			if done, _ := candidate.isExhausted(); done {
				continue
			}
			inner, err := candidate.resolve(ctx)
			if err != nil {
				exhaustedReasons = append(exhaustedReasons, candidate.name+": "+err.Error())
				continue
			}
			// 各候选绑定各自的模型名,避免沿用上一候选的 req.Model。
			attempt := *req
			attempt.Model = candidate.name
			resp, err := collectOne(inner.GenerateContent(ctx, &attempt, stream))
			if err == nil {
				yield(resp, nil)
				return
			}
			if switchWorthy(err) {
				candidate.markExhausted(upstreamBrief(err))
				exhaustedReasons = append(exhaustedReasons, candidate.name+": "+upstreamBrief(err))
				continue
			}
			yield(nil, err)
			return
		}
		yield(nil, fmt.Errorf("modelprovider: 模型链全部候选不可用(role=%s):%s",
			m.role, strings.Join(exhaustedReasons, "; ")))
	}
}

// collectOne 归一内层模型的单结果迭代;内层各实现每次调用至多产出一对结果。
func collectOne(seq iter.Seq2[*model.LLMResponse, error]) (*model.LLMResponse, error) {
	var (
		resp *model.LLMResponse
		err  error
		got  bool
	)
	seq(func(r *model.LLMResponse, e error) bool {
		resp, err, got = r, e, true
		return false
	})
	if !got {
		return nil, errors.New("modelchain: 内层模型未产出结果")
	}
	return resp, err
}

func upstreamBrief(err error) string {
	var ue *upstream.Error
	if errors.As(err, &ue) {
		return fmt.Sprintf("kind=%s status=%d code=%s", ue.Kind, ue.HTTPStatus, ue.Code)
	}
	return err.Error()
}
