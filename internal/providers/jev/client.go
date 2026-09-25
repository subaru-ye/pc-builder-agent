// Package jev implements the TypeSafe SystemOne decision API for the intent
// domain with the Go standard library only. It never retries: evaluation
// records provider availability honestly, and production retry policy is a
// separate concern.
package jev

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/decision"
)

const (
	// ModelPin is the evaluated Jev release. Response model IDs are recorded
	// separately so drift stays visible.
	ModelPin = "jev-1.13.0"
	// DefaultBaseURL is the native TypeSafe API endpoint origin.
	DefaultBaseURL = "https://api.typesafe.ai"
	// Path is the native decision endpoint.
	Path = "/v1/systemone"
	// questionID is the single bounded Choice question per request.
	questionID = "next_action"
	// SumTolerance is the documented distribution-sum tolerance. Calibrated
	// provider output is rounded; values outside this window are contract
	// failures, not scores.
	SumTolerance = 0.02
	// maxBodyBytes bounds a single response read.
	maxBodyBytes = 1 << 20
)

// Error classes recorded verbatim in evaluation evidence. They separate
// "Jev is unavailable" from "Jev answered outside the contract".
const (
	ClassAuth               = "auth"
	ClassInvalidRequest     = "invalid_request"
	ClassRateLimited        = "rate_limited"
	ClassProviderOverloaded = "provider_overloaded"
	ClassTimeout            = "timeout"
	ClassCanceled           = "canceled"
	ClassTransport          = "transport"
	ClassDecode             = "decode"
	ClassContract           = "contract"
	ClassUnexpectedStatus   = "unexpected_status"
)

// Error is a classified provider failure. Class strings are stable report keys.
type Error struct {
	Class      string
	StatusCode int
	Err        error
}

func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("jev %s (http %d): %v", e.Class, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("jev %s: %v", e.Class, e.Err)
}
func (e *Error) Unwrap() error { return e.Err }

// ErrorClass exposes the stable classification for consumers that stay
// provider-agnostic (they see only a decision.IntentClassifier).
func (e *Error) ErrorClass() string { return e.Class }

// Class returns the recorded class of a Classify error, or "" for nil.
func Class(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return ""
}

// QuestionInstructions is the fixed Choice instruction. It restates the
// product authority boundary; it never contains evaluated examples.
const QuestionInstructions = "根据装机助手的已有需求状态、执行事实和当前用户消息，判断本轮应对该消息执行的控制动作。只能选择一个选项。"

// QuestionCriteria maps each intent to its product semantics. The map is part
// of the hashed question definition.
func QuestionCriteria() map[decision.Intent]string {
	return map[decision.Intent]string{
		decision.IntentCollect:   "只记录、讨论或追问信息；本轮没有授权生成、修改或比较配置，不执行规划。",
		decision.IntentConfirm:   "已有意图已足够完整，正在等待用户对首次执行的明确授权；本轮没有给出该授权。",
		decision.IntentPlan:      "本轮明确授权生成、修改、比较、继续或重新规划配置，可以直接执行对应动作。",
		decision.IntentAmbiguous: "无法安全选择上述任一动作，或本轮包含相互冲突的动作。",
	}
}

// QuestionJSON returns the canonical question definition that is hashed into
// run provenance, so a report can be tied to the exact asked question.
func QuestionJSON() []byte {
	raw, _ := json.Marshal(map[string]any{"instructions": QuestionInstructions, "criteria": QuestionCriteria()})
	return raw
}

// QuestionHash is the stable SHA256 of QuestionJSON.
func QuestionHash() string { return fmt.Sprintf("%x", sha256.Sum256(QuestionJSON())) }

type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	// Timeout bounds each Classify call; zero means the caller's context is
	// the only bound. It is an evaluation input, recorded in run provenance.
	Timeout time.Duration
}

type Client struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("jev: API key required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("jev: model required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("jev: invalid base URL %q", cfg.BaseURL)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &Client{cfg: cfg, client: cfg.HTTPClient}, nil
}

// Host reports the endpoint origin recorded in run provenance.
func (c *Client) Host() string {
	u, _ := url.Parse(c.cfg.BaseURL)
	return u.Host
}

type intentState struct {
	CurrentTurn      string          `json:"current_turn"`
	RequirementState json.RawMessage `json:"requirement_state,omitempty"`
	HasBuild         bool            `json:"has_build"`
	CanPlan          bool            `json:"can_plan"`
	BuildVersion     int             `json:"build_version,omitempty"`
	LastAssistant    string          `json:"last_assistant,omitempty"`
}

type choiceQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type wireRequest struct {
	Model     string                    `json:"model"`
	State     json.RawMessage           `json:"state"`
	Questions map[string]choiceQuestion `json:"questions"`
}

type choiceAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type usage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

type wireResponse struct {
	Model   string                  `json:"model"`
	Answers map[string]choiceAnswer `json:"answers"`
	Usage   usage                   `json:"usage"`
}

// ChoiceResult 是一次通用 Choice 调用的已校验观测。
type ChoiceResult struct {
	Choice              string
	Probabilities       map[string]float64
	Confidence          float64
	SelectedProbability float64
	ResponseModel       string
	InputTokens         int
	OutputTokens        int
	Duration            time.Duration
}

// Question 是参数化的单 Choice 问题定义；Options 参与响应校验，并整体
// 进入调用方自己的问题哈希。
type Question struct {
	ID           string
	Instructions string
	Options      map[string]string
}

// Ask 执行一次通用单 Choice 决策调用。state 必须是已按领域契约裁剪过的
// JSON 对象（本包不理解其内部结构）；返回的 error 恒为分类后的 *Error，
// 不做客户端重试。
func (c *Client) Ask(ctx context.Context, state json.RawMessage, q Question) (ChoiceResult, error) {
	if q.ID == "" {
		return ChoiceResult{}, classifyErr(ClassContract, 0, fmt.Errorf("question id required"))
	}
	if len(q.Options) == 0 || q.Instructions == "" {
		return ChoiceResult{}, classifyErr(ClassContract, 0, fmt.Errorf("question instructions and options required"))
	}
	if len(json.RawMessage(state)) == 0 {
		return ChoiceResult{}, classifyErr(ClassContract, 0, fmt.Errorf("state required"))
	}
	res := ChoiceResult{}
	start := time.Now()
	if c.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	body, err := json.Marshal(wireRequest{
		Model: c.cfg.Model,
		State: state,
		Questions: map[string]choiceQuestion{q.ID: {
			Type: "choice", Instructions: q.Instructions, Criteria: q.Options,
		}},
	})
	if err != nil {
		return res, classifyErr(ClassContract, 0, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+Path, bytes.NewReader(body))
	if err != nil {
		return res, classifyErr(ClassContract, 0, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	httpRes, err := c.client.Do(req)
	res.Duration = time.Since(start)
	if err != nil {
		class, wrapped := transportClass(err)
		return res, &Error{Class: class, Err: wrapped}
	}
	defer func() { _ = httpRes.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(httpRes.Body, maxBodyBytes))
	if err != nil {
		return res, classifyErr(ClassTransport, httpRes.StatusCode, err)
	}
	if httpRes.StatusCode != http.StatusOK {
		class := ClassUnexpectedStatus
		switch httpRes.StatusCode {
		case http.StatusUnauthorized:
			class = ClassAuth
		case http.StatusUnprocessableEntity:
			class = ClassInvalidRequest
		case http.StatusTooManyRequests:
			class = ClassRateLimited
		case 529:
			class = ClassProviderOverloaded
		}
		return res, classifyErr(class, httpRes.StatusCode, errors.New(strings.TrimSpace(string(raw))))
	}
	var decoded wireResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return res, classifyErr(ClassDecode, httpRes.StatusCode, err)
	}
	result, err := validateChoice(decoded, c.cfg.Model, q)
	if err != nil {
		return res, classifyErr(ClassContract, httpRes.StatusCode, err)
	}
	result.Duration = res.Duration
	return result, nil
}

// Classify asks the single bounded intent question. The returned error is
// always a classified *Error; no client-side retry happens.
func (c *Client) Classify(ctx context.Context, in decision.IntentInput) (decision.IntentResult, error) {
	res := decision.IntentResult{RequestedModel: c.cfg.Model, Probabilities: map[decision.Intent]float64{}}
	requirementState := in.RequirementState
	if len(requirementState) == 0 {
		requirementState = nil
	}
	state, err := json.Marshal(intentState{
		CurrentTurn:      in.CurrentTurn,
		RequirementState: requirementState,
		HasBuild:         in.HasBuild,
		CanPlan:          in.CanPlan,
		BuildVersion:     in.BuildVersion,
		LastAssistant:    in.LastAssistant,
	})
	if err != nil {
		return res, classifyErr(ClassContract, 0, err)
	}
	question := intentQuestion()
	choice, err := c.Ask(ctx, state, question)
	if err != nil {
		return res, err
	}
	res.Duration = choice.Duration
	res.ResponseModel = choice.ResponseModel
	res.InputTokens, res.OutputTokens = choice.InputTokens, choice.OutputTokens
	res.Confidence, res.SelectedProbability = choice.Confidence, choice.SelectedProbability
	res.Intent = decision.Intent(choice.Choice)
	for raw, p := range choice.Probabilities {
		res.Probabilities[decision.Intent(raw)] = p
	}
	return res, nil
}

func intentQuestion() Question {
	options := map[string]string{}
	for intent, text := range QuestionCriteria() {
		options[string(intent)] = text
	}
	return Question{ID: questionID, Instructions: QuestionInstructions, Options: options}
}

func transportClass(err error) (string, error) {
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassTimeout, err
	}
	if errors.Is(err, context.Canceled) {
		return ClassCanceled, err
	}
	return ClassTransport, err
}

func classifyErr(class string, status int, err error) error {
	return &Error{Class: class, StatusCode: status, Err: err}
}

// validate enforces the response contract: answer type, selected option,
// finite probabilities in [0,1], distribution sum, confidence and usage.
func validate(decoded wireResponse, requestedModel string) (decision.IntentResult, error) {
	res := decision.IntentResult{RequestedModel: requestedModel, Probabilities: map[decision.Intent]float64{}}
	choice, err := validateChoice(decoded, requestedModel, intentQuestion())
	if err != nil {
		return res, err
	}
	res.ResponseModel = choice.ResponseModel
	res.InputTokens, res.OutputTokens = choice.InputTokens, choice.OutputTokens
	res.Intent = decision.Intent(choice.Choice)
	for raw, p := range choice.Probabilities {
		res.Probabilities[decision.Intent(raw)] = p
	}
	res.SelectedProbability = choice.SelectedProbability
	res.Confidence = choice.Confidence
	return res, nil
}

// validateChoice enforces the response contract against the asked question:
// answer type, selected option, finite probabilities in [0,1], distribution
// sum, confidence and usage.
func validateChoice(decoded wireResponse, requestedModel string, q Question) (ChoiceResult, error) {
	res := ChoiceResult{Probabilities: map[string]float64{}}
	if decoded.Model == "" {
		return res, fmt.Errorf("response model id missing")
	}
	res.ResponseModel = decoded.Model
	if decoded.Usage.InputTokens == nil || decoded.Usage.OutputTokens == nil || *decoded.Usage.InputTokens < 0 || *decoded.Usage.OutputTokens < 0 {
		return res, fmt.Errorf("usage fields missing or negative")
	}
	res.InputTokens, res.OutputTokens = *decoded.Usage.InputTokens, *decoded.Usage.OutputTokens
	answer, ok := decoded.Answers[q.ID]
	if !ok {
		return res, fmt.Errorf("answer %q missing", q.ID)
	}
	if answer.Type != "choice" {
		return res, fmt.Errorf("answer type %q is not choice", answer.Type)
	}
	if _, known := q.Options[answer.Choice]; !known {
		return res, fmt.Errorf("unknown option %q", answer.Choice)
	}
	if len(answer.Probabilities) != len(q.Options) {
		return res, fmt.Errorf("probabilities must cover exactly the %d options, got %d", len(q.Options), len(answer.Probabilities))
	}
	sum := 0.0
	for raw, p := range answer.Probabilities {
		if _, known := q.Options[raw]; !known {
			return res, fmt.Errorf("unknown probability option %q", raw)
		}
		if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return res, fmt.Errorf("probability %q=%v outside [0,1]", raw, p)
		}
		sum += p
		res.Probabilities[raw] = p
	}
	if math.Abs(sum-1) > SumTolerance {
		return res, fmt.Errorf("probability sum %v outside tolerance %v", sum, SumTolerance)
	}
	if math.IsNaN(answer.Confidence) || math.IsInf(answer.Confidence, 0) || answer.Confidence < 0 || answer.Confidence > 1 {
		return res, fmt.Errorf("confidence %v outside [0,1]", answer.Confidence)
	}
	res.Choice = answer.Choice
	res.SelectedProbability = answer.Probabilities[answer.Choice]
	res.Confidence = answer.Confidence
	return res, nil
}
