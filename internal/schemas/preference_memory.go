package schemas

// 跨会话装机偏好记忆(L1 原子记忆)与会话需求(RequirementState)分开建模:
// 写入链路须阻止会话内 temporary/uncertain 内容升级;本类型没有 scope 概念,
// 因而校验器本身无法核对原始会话 scope;
// 召回一律降级为待确认建议,不得自动成为硬约束。设计见 docs/tech/偏好记忆设计草案.md。
import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// ErrPreferenceMemoryInvalid 是偏好记忆字段校验失败的稳定错误:
// 写入边界拒绝,不做默认值补齐或静默降级。
var ErrPreferenceMemoryInvalid = errors.New("preference memory: 校验失败")

// 偏好记忆的取值口径与 RequirementField/RequirementSource 对齐:
// strength 保留用户表达强度,但召回出口一律按建议处理;
// evidence 只接受用户明确表达(stated)或明确接受(accepted_proposal),
// 推断(inferred)准入待写入链路定型后另定,当前不允许写入。
const (
	PreferenceSubjectSelf     = "self"
	PreferenceStrengthPrefer  = "prefer"
	PreferenceStrengthMust    = "must"
	PreferenceEvidenceStated  = "stated"
	PreferenceEvidenceAccept  = "accepted_proposal"
	PreferenceStatusActive    = "active"
	PreferenceStatusSupersede = "superseded"
	PreferenceStatusRetract   = "retracted"
)

// PreferenceSource 记录偏好来自哪次会话哪条消息的哪段原话。
// kind 与 RequirementSource 同口径(chat|edit);message_id 由服务端注入,模型不可指定。
type PreferenceSource struct {
	Kind      string `json:"kind"`
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Quote     string `json:"quote"`
}

// PreferenceMemory 是 (owner, subject, field) 上一条用户表达的原子偏好。
// 同一三元组至多一条 active(库内部分唯一索引),改主意走 supersede 链。
type PreferenceMemory struct {
	ID         string           `json:"id,omitempty"`
	OwnerID    string           `json:"owner_id"`
	Subject    string           `json:"subject"`
	Field      string           `json:"field"`
	Value      json.RawMessage  `json:"value"`
	Strength   string           `json:"strength"`
	Evidence   string           `json:"evidence"`
	Volatile   bool             `json:"volatile,omitempty"`
	ObservedAt string           `json:"observed_at,omitempty"` // YYYY-MM-DD;volatile 必填,易失证据的时效基准
	Status     string           `json:"status,omitempty"`      // 写入侧只允许 active;superseded/retracted 为墓碑
	Supersedes string           `json:"supersedes,omitempty"`
	Source     PreferenceSource `json:"source"`
	CreatedAt  time.Time        `json:"created_at,omitempty"`
	UpdatedAt  time.Time        `json:"updated_at,omitempty"`
}

// DecodePreferenceSource 是读取持久化来源的严格入口:未知字段拒绝,防止形状漂移。
func DecodePreferenceSource(raw []byte) (PreferenceSource, error) {
	var source PreferenceSource
	if err := decodeStrict(raw, &source); err != nil {
		return source, fmt.Errorf("preference source: %w", err)
	}
	return source, nil
}

// ValidPreferenceField 限定偏好维度词表:已知需求字段或 free.* 自由条目,
// 总长受列约束(64)限制。
func ValidPreferenceField(field string) bool {
	return len(field) <= 64 && (slices.Contains(RequirementFieldKeys, field) || FreeField(field))
}

// ValidatePreferenceMemory 校验一条待写入的偏好记忆。ID 与时间戳由服务端生成,
// 客户端携带即拒绝;通过校验的记录才允许进入存储层。
func ValidatePreferenceMemory(m PreferenceMemory) error {
	switch {
	case m.ID != "":
		return invalidPreference("id 由服务端生成,写入时不允许携带")
	case m.OwnerID == "":
		return invalidPreference("owner_id 必填")
	case m.Subject == "" || len(m.Subject) > 40:
		return invalidPreference("subject 必填且不超过 40 字符(self 表示本人,其余为具名代配对象)")
	case !ValidPreferenceField(m.Field):
		return invalidPreference(fmt.Sprintf("field 必须是已知需求字段或 free.* 条目: %q", m.Field))
	case !json.Valid(m.Value) || len(m.Value) == 0:
		return invalidPreference("value 必须是非空合法 JSON")
	case m.Strength != PreferenceStrengthPrefer && m.Strength != PreferenceStrengthMust:
		return invalidPreference("strength 仅允许 prefer 或 must")
	case m.Evidence != PreferenceEvidenceStated && m.Evidence != PreferenceEvidenceAccept:
		return invalidPreference("evidence 仅允许 stated 或 accepted_proposal;不确定/推断内容不升级为长期记忆")
	case m.Status != "" && m.Status != PreferenceStatusActive:
		return invalidPreference("写入记录的 status 只能为空或 active")
	case m.Supersedes != "":
		return invalidPreference("supersedes 由存储层在冲突更新时写入")
	case !m.CreatedAt.IsZero() || !m.UpdatedAt.IsZero():
		return invalidPreference("时间戳由服务端生成,写入时不允许携带")
	case m.Source.Kind != "chat" && m.Source.Kind != "edit":
		return invalidPreference("source.kind 仅允许 chat 或 edit")
	case m.Source.SessionID == "" || len(m.Source.SessionID) > 200:
		return invalidPreference("source.session_id 必填且不超过 200 字符")
	case m.Source.MessageID == "" || len(m.Source.MessageID) > 200:
		return invalidPreference("source.message_id 必填且不超过 200 字符")
	case m.Source.Quote == "" || len(m.Source.Quote) > 2000:
		return invalidPreference("source.quote 必填且不超过 2000 字符(用户原话)")
	}
	if _, err := ParsePreferenceDate(m.ObservedAt); err != nil {
		return err
	}
	if m.Volatile && m.ObservedAt == "" {
		return invalidPreference("易失事实(价格/现货等)必须携带 observed_at,否则召回无法判定时效")
	}
	return nil
}

func invalidPreference(detail string) error {
	return fmt.Errorf("%w: %s", ErrPreferenceMemoryInvalid, detail)
}

// ParsePreferenceDate 解析 observed_at 的唯一日期格式;空值返回零值时间。
func ParsePreferenceDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, invalidPreference("observed_at 必须是 YYYY-MM-DD 日期: " + value)
	}
	return t, nil
}

// PreferenceStale 报告一条易失记忆是否超出新鲜度窗口:过期即不可作为建议复用,
// 宁可重新询问也不拿旧价格/旧规格当事实。非易失偏好不过期;
// 窗口由调用方显式传入,阶段内不冻结默认值。观察日期无法解析按过期处理。
func PreferenceStale(m PreferenceMemory, today time.Time, maxAge time.Duration) bool {
	if !m.Volatile {
		return false
	}
	observed, err := ParsePreferenceDate(m.ObservedAt)
	if err != nil {
		return true
	}
	return today.Sub(observed) > maxAge
}

// FilterPreferenceSuggestions 是记忆召回的唯一出口:仅保留 active 记录并剔除
// 超出新鲜度窗口的易失事实,保持原顺序。返回值是待确认建议——调用方必须让用户
// 确认后才能进入当前需求,不得自动写入 RequirementState 或规则引擎;
// 当前会话中用户明确表达的需求永远优先于历史记忆。
func FilterPreferenceSuggestions(records []PreferenceMemory, today time.Time, maxVolatileAge time.Duration) []PreferenceMemory {
	suggestions := make([]PreferenceMemory, 0, len(records))
	for _, m := range records {
		if m.Status != PreferenceStatusActive || PreferenceStale(m, today, maxVolatileAge) {
			continue
		}
		suggestions = append(suggestions, m)
	}
	return suggestions
}

// EqualPreferenceValue 以 JSON 语义比较偏好值:换行/键序差异不算变化,
// 重申相同值不构成"改主意",不应产生新的 supersede 链。
func EqualPreferenceValue(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}
