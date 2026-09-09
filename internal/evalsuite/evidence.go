package evalsuite

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// PromptIdentity 标识实际编译的静态提示词组件，不声称覆盖每次动态模型请求。
// 输入、候选、前轮回复等动态上下文继续由 cases/results 保存，组装逻辑由 Code 标识。
type PromptIdentity struct {
	SchemaVersion int               `json:"schema_version"`
	SHA256        string            `json:"sha256"`
	SnapshotFile  string            `json:"snapshot_file"`
	Components    map[string]string `json:"components"`
}

type PromptComponent struct {
	Role string `json:"role"`
	Name string `json:"name"`
	Text string `json:"text"`
}

type PromptSnapshot struct {
	SchemaVersion int               `json:"schema_version"`
	Components    []PromptComponent `json:"components"`
}

// NewPromptEvidence 冻结调用方从编译常量取得的原文，内容排序与版本计算不依赖文件系统。
func NewPromptEvidence(components []PromptComponent) (PromptSnapshot, PromptIdentity, error) {
	snapshot := PromptSnapshot{SchemaVersion: 1, Components: append([]PromptComponent(nil), components...)}
	sort.Slice(snapshot.Components, func(i, j int) bool {
		a, b := snapshot.Components[i], snapshot.Components[j]
		return a.Role+"/"+a.Name < b.Role+"/"+b.Name
	})
	id := PromptIdentity{SchemaVersion: 1, SnapshotFile: "prompts.json", Components: map[string]string{}}
	if len(snapshot.Components) == 0 {
		return snapshot, id, fmt.Errorf("没有可记录的提示词组件")
	}
	for _, component := range snapshot.Components {
		key := component.Role + "/" + component.Name
		if component.Role == "" || component.Name == "" || component.Text == "" || id.Components[key] != "" {
			return snapshot, id, fmt.Errorf("空白或重复的提示词组件: %s", key)
		}
		id.Components[key] = fmt.Sprintf("%x", sha256.Sum256([]byte(component.Text)))
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return snapshot, id, err
	}
	id.SHA256, err = JSONHash(raw)
	return snapshot, id, err
}

// Write 只创建新证据文件；不会以当前源码覆盖任何已存在的运行快照。
func (s PromptSnapshot) Write(dir string) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "prompts.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return f.Close()
}

// DataIdentity 的 v1 内容与严格对照的 SnapshotView JSON 相同，覆盖实际目录规格及价格。
// 它包含快照标识和日期，但不以日期替代数据，也不包含未冻结的语义向量索引。
type DataIdentity struct {
	SchemaVersion int    `json:"schema_version"`
	SHA256        string `json:"sha256"`
	Scope         string `json:"scope"`
}

func NewDataIdentity(snapshot SnapshotView) (DataIdentity, error) {
	id := DataIdentity{SchemaVersion: 1, Scope: "catalog_prices"}
	if snapshot.Catalog == nil {
		return id, fmt.Errorf("缺少实际加载的完整商品目录，不能生成数据指纹")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return id, err
	}
	id.SHA256, err = JSONHash(raw)
	return id, err
}

// VerifyRunEvidence 仅复验已声明的新证据；历史缺失保持未记录，不使用当前源码回填。
func VerifyRunEvidence(dir string, meta ReportMeta, records []CaseRecord) error {
	if meta.Prompts != nil {
		if meta.Prompts.SchemaVersion != 1 || meta.Prompts.SnapshotFile != "prompts.json" {
			return fmt.Errorf("不支持的提示词证据格式")
		}
		raw, err := os.ReadFile(filepath.Join(dir, "prompts.json"))
		if err != nil {
			return fmt.Errorf("读取声明的提示词原文失败: %w", err)
		}
		var snapshot PromptSnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.SchemaVersion != 1 {
			return fmt.Errorf("提示词原文格式无效")
		}
		_, id, err := NewPromptEvidence(snapshot.Components)
		if err != nil || !reflect.DeepEqual(id, *meta.Prompts) {
			return fmt.Errorf("提示词原文与记录的版本指纹不一致")
		}
	}
	if meta.Data != nil {
		if meta.Data.SchemaVersion != 1 || meta.Data.Scope != "catalog_prices" || meta.Data.SHA256 == "" {
			return fmt.Errorf("不支持的商品数据指纹格式")
		}
		for _, record := range records {
			id, err := NewDataIdentity(record.Snapshot)
			if err != nil || id != *meta.Data {
				return fmt.Errorf("%s/%d 冻结目录与商品数据指纹不一致", record.CaseID, record.Seed)
			}
		}
	}
	return nil
}
