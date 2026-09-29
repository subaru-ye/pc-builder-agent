package planningeval

// 此文件只供离线实验使用。输入字段由上游 RequirementState 提供，本策略不从
// 自由文本提取字段值，也不持有 Service/store，不能保存记忆或推断 subject。
import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

const preferenceCandidatePolicy = "lifetime-v1.1"

type preferenceCandidateMessage struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type PreferenceCandidate struct {
	Field         string                   `json:"field"`
	Value         json.RawMessage          `json:"value"`
	Strength      string                   `json:"strength"`
	Evidence      string                   `json:"evidence"`
	Source        schemas.PreferenceSource `json:"source"`
	SubjectStatus string                   `json:"subject_status"`
}

type PreferenceCandidateDecision struct {
	Field     string               `json:"field"`
	Reason    string               `json:"reason"`
	Candidate *PreferenceCandidate `json:"candidate,omitempty"`
}

// preferenceCandidates 返回最终状态每个已出现字段的决定；历史值、被撤销的值
// 不回退成候选。候选只表达“值得向用户询问是否保存”，不表达保存授权。
func preferenceCandidates(sessionID string, state schemas.RequirementState, messages []preferenceCandidateMessage, lifetime bool) []PreferenceCandidateDecision {
	keys := make([]string, 0, len(state.Fields))
	for key, f := range state.Fields {
		if f.Status != "unknown" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	decisions := make([]PreferenceCandidateDecision, 0, len(keys))
	for _, key := range keys {
		f := state.Fields[key]
		reason, text := preferenceCandidateEligibility(sessionID, key, f, messages)
		if reason == "" && lifetime {
			reason = preferenceLifetimeReason(key, f.Value, f.Source.Quote, text)
		}
		d := PreferenceCandidateDecision{Field: key, Reason: reason}
		if reason == "" {
			d.Reason = "candidate_needs_user_confirmation"
			d.Candidate = &PreferenceCandidate{
				Field: key, Value: append(json.RawMessage(nil), f.Value...), Strength: f.Strength, Evidence: f.Evidence,
				Source:        schemas.PreferenceSource{Kind: "chat", SessionID: sessionID, MessageID: f.Source.MessageID, Quote: f.Source.Quote},
				SubjectStatus: "requires_user_selection",
			}
		}
		decisions = append(decisions, d)
	}
	return decisions
}

func preferenceCandidateEligibility(sessionID, key string, f schemas.RequirementField, messages []preferenceCandidateMessage) (string, string) {
	switch {
	case !schemas.IsStablePreferenceField(key):
		return "field_not_storable", ""
	case f.Status != "active":
		return "field_not_active", ""
	case f.Scope != "session":
		return "scope_not_session", ""
	case f.Evidence != "stated" && f.Evidence != "accepted_proposal":
		return "evidence_not_admissible", ""
	case f.Source == nil || f.Source.Kind != "chat" || f.Source.MessageID == "" || f.Source.Quote == "":
		return "source_not_chat", ""
	}
	var text string
	found := 0
	for _, m := range messages {
		if m.ID == f.Source.MessageID {
			found++
			if m.Role != "user" || !strings.Contains(m.Content, f.Source.Quote) {
				return "source_not_user_quote", ""
			}
			text = m.Content
		}
	}
	if found != 1 {
		return "source_not_unique", ""
	}
	// 复用 schema 校验形状及字段取值。占位 owner/subject 仅用于校验，不出现在输出。
	m := schemas.PreferenceMemory{OwnerID: "offline", Subject: "unresolved", Field: key, Value: f.Value,
		Strength: f.Strength, Evidence: f.Evidence,
		Source: schemas.PreferenceSource{Kind: "chat", SessionID: sessionID, MessageID: f.Source.MessageID, Quote: f.Source.Quote}}
	if schemas.ValidatePreferenceMemory(m) != nil {
		return "invalid_field", ""
	}
	_, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{{Op: "set", Field: key, Value: f.Value, Strength: f.Strength, Scope: "session", Kind: f.Kind}},
	}, schemas.RequirementSource{Kind: "edit"})
	if err != nil {
		return "invalid_field", ""
	}
	// 离线状态无法核验提案是否实际展示、由哪条用户消息接受；不复制产品侧授权器。
	if f.Evidence == "accepted_proposal" {
		return "proposal_needs_product_verification", ""
	}
	return "", text
}

func preferenceLifetimeReason(field string, value json.RawMessage, quote, message string) string {
	// ponytail: v1 用小词表作保守基线；否定作用域、转述、跨分句指代仍会误判。
	// 后续升级须使用独立标注的新样本比较，不能为本轮验证题临时加词。
	for _, guard := range []struct {
		reason string
		words  []string
	}{
		{"temporary_language", []string{"这次", "这台", "暂时", "临时", "就一次", "先试试"}},
		{"uncertain_language", []string{"也许", "可能", "不确定", "拿不准", "还没决定", "如果", "假如", "吗", "？", "?"}},
		{"negated_language", []string{"别记", "不记", "不要记", "不用记", "不想记", "不再", "不是", "不喜欢", "别保存", "不要保存"}},
		{"attributed_language", []string{"他说", "她说", "朋友说", "客户说", "原话", "举例", "例如", "假设", "“", "”", "\""}},
	} {
		if containsAny(message, guard.words) {
			return guard.reason
		}
	}
	for _, clause := range strings.FieldsFunc(quote, func(r rune) bool {
		return strings.ContainsRune("，,。；;！!\n", r)
	}) {
		if containsAny(clause, []string{"一直", "一向", "长期", "今后", "以后", "通常", "每次", "记住", "记下", "记着"}) && preferenceValueGrounded(field, value, clause) {
			return ""
		}
	}
	return "no_bound_lifetime_evidence"
}

func preferenceValueGrounded(field string, raw json.RawMessage, text string) bool {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" || value == "any" {
		return false
	}
	if field == "appearance" {
		return strings.Contains(text, value)
	}
	anchors := map[string]map[string][]string{
		"brand_pref.cpu": {"amd": {"AMD", "A家"}, "intel": {"Intel", "英特尔"}},
		"brand_pref.gpu": {"amd": {"A卡", "AMD显卡"}, "nvidia": {"N卡", "NVIDIA"}},
		"noise_pref":     {"silent": {"安静", "静音", "低噪"}, "normal": {"常规", "普通噪音", "正常噪音"}},
		"size_pref":      {"itx": {"ITX", "小机箱", "小主机"}, "atx": {"ATX", "标准机箱"}},
	}
	// 不做裸子串品牌匹配，避免 Intel 出现在非品牌英文单词里。
	for _, anchor := range anchors[field][value] {
		upper, target := strings.ToUpper(text), strings.ToUpper(anchor)
		targetRunes := []rune(target)
		for offset := 0; offset < len(upper); {
			i := strings.Index(upper[offset:], target)
			if i < 0 {
				break
			}
			i += offset
			before, after := []rune(upper[:i]), []rune(upper[i+len(target):])
			asciiLetter := func(r rune) bool { return r < 128 && unicode.IsLetter(r) }
			if (!asciiLetter(targetRunes[0]) || len(before) == 0 || !asciiLetter(before[len(before)-1])) &&
				(!asciiLetter(targetRunes[len(targetRunes)-1]) || len(after) == 0 || !asciiLetter(after[0])) {
				return true
			}
			offset = i + len(target)
		}
	}
	return false
}
