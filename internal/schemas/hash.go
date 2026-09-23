package schemas

// 规范化 hash:确认快照的 review_hash 与 build run 的 builder_input_hash 共用
// 同一算法。语义规范化(unmarshal→重 marshal,Go 按 key 排序序列化)保证
// JSON key/顺序差异不改变 hash;与评估层 normalizedHash 保持同一口径。
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalHash 返回 JSON 的语义规范化 SHA256(hex)。解析失败的输入返回错误,
// 调用方不得对无法解析的载荷产生 hash。
func CanonicalHash(raw json.RawMessage) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:]), nil
}
