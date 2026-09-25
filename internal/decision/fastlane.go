package decision

// Fastlane 是"可安全跳过 Screening 的单字段预算更新"判定域。Jev 在这里只
// 回答一个语义问题：本轮是否在确定该预算字段的值；数值提取、状态写入、
// 需求确认与 Builder 启动全部属于确定性代码，Jev 无权生成任何事实。
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// FastlaneVerdict 是快走语义判定的五个封闭选项。
type FastlaneVerdict string

const (
	// VerdictDetermine: 本轮在设定/修改该预算字段的值（"预算改成7500"）。
	VerdictDetermine FastlaneVerdict = "determine"
	// VerdictAsk: 本轮在提问或征求意见（"7500够吗"）。
	VerdictAsk FastlaneVerdict = "ask"
	// VerdictQuoteReference: 本轮在引用/谈论某报价或价格信息（"看到7500的报价"）。
	VerdictQuoteReference FastlaneVerdict = "quote_reference"
	// VerdictReject: 本轮明确拒绝或撤回该预算（"7500不行""先不算了"）。
	VerdictReject FastlaneVerdict = "reject"
	// VerdictUncertain: 无法安全判断；与任何 fallthrough 一样回退 Screening。
	VerdictUncertain FastlaneVerdict = "uncertain"
)

// FastlaneField 是 v1 快走范围：只研究预算金额这一个字段。
const FastlaneField = "budget_cny"

// FastlaneInput 只携带判定所需的最小信息：本轮原话与确定性提取出的候选
// 字段/值。不发送需求全量状态、聊天历史或任何密钥。
type FastlaneInput struct {
	CurrentTurn    string
	CandidateField string
	CandidateValue json.RawMessage
}

// FastlaneResult 是一次判定的完整观测；概率与置信度分开保存，阈值策略
// 属于评估器，不属于本域。
type FastlaneResult struct {
	Verdict             FastlaneVerdict
	Probabilities       map[FastlaneVerdict]float64
	Confidence          float64
	SelectedProbability float64
	RequestedModel      string
	ResponseModel       string
	InputTokens         int
	OutputTokens        int
	Duration            time.Duration
}

// FastlaneJudge 是判定接口；实现方（jev.Client 适配器）负责传输与校验。
type FastlaneJudge interface {
	Judge(context.Context, FastlaneInput) (FastlaneResult, error)
}

// FastlaneInstructions 是固定的 Choice 指令。它只描述装机助手的权威边界，
// 不含任何被评轮次原话。
const FastlaneInstructions = "装机助手正在判断一条用户消息能否直接确定一个预算字段的值。该字段的候选值已由程序从这条消息中提取。判断这条消息本身是否在设定或修改该预算。只能选择一个选项。"

// FastlaneCriteria 把每个选项映射为产品语义，是可哈希问题定义的一部分。
func FastlaneCriteria() map[FastlaneVerdict]string {
	return map[FastlaneVerdict]string{
		VerdictDetermine:      "这条消息在设定或修改该预算：用户把（该候选值的）金额作为自己的预算要求表达出来。",
		VerdictAsk:            "这条消息在提问或征求意见（例如问金额够不够、行不行、怎么样），没有设定预算。",
		VerdictQuoteReference: "这条消息在引用或谈论某个报价、价格或别人的配置（例如看到某个报价），不是在设定自己的预算。",
		VerdictReject:         "这条消息明确拒绝、撤销或不设定该预算。",
		VerdictUncertain:      "无法安全判断，或消息同时表达多种意图。",
	}
}

// FastlaneQuestionJSON 返回规范问题定义，供运行产物哈希绑定。
func FastlaneQuestionJSON() []byte {
	raw, _ := json.Marshal(map[string]any{"instructions": FastlaneInstructions, "criteria": FastlaneCriteria()})
	return raw
}

// FastlaneQuestionHash 是问题定义的稳定 SHA256。
func FastlaneQuestionHash() string { return fmt.Sprintf("%x", sha256.Sum256(FastlaneQuestionJSON())) }
