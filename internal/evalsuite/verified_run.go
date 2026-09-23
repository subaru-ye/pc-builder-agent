package evalsuite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// ReadVerifiedRun 复验冻结题目、记录完整性、判卷和多轮来源，供离线对照与诊断共享。
func ReadVerifiedRun(dir string) (ReportMeta, []CaseRecord, error) {
	var meta ReportMeta
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return meta, nil, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, nil, err
	}
	if meta.RecordSchemaVersion < 0 || meta.RecordSchemaVersion > 1 {
		return meta, nil, fmt.Errorf("不支持的运行记录格式版本")
	}
	if err := ValidateGraderVersion(meta.GraderVersion); err != nil {
		return meta, nil, err
	}
	suite, cases, err := ReadSuiteSnapshot(filepath.Join(dir, "cases.json"), meta.SuiteSHA256)
	if err != nil {
		return meta, nil, err
	}
	if meta.SuiteVersion != "" && meta.SuiteVersion != suite.Manifest.Version {
		return meta, nil, fmt.Errorf("运行元数据与冻结题库版本名不一致")
	}
	records, err := ReadRecords(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		return meta, nil, err
	}
	if len(records) != len(cases)*meta.RequestedSeeds {
		return meta, nil, fmt.Errorf("首跑记录数量不完整")
	}
	if err := VerifyRunEvidence(dir, meta, records); err != nil {
		return meta, nil, err
	}
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	for _, r := range records {
		c, ok := byID[r.CaseID]
		key := fmt.Sprintf("%s/%d", r.CaseID, r.Seed)
		if !ok || r.Stage != c.Stage || r.Seed < 1 || r.Seed > meta.RequestedSeeds || seen[key] {
			return meta, nil, fmt.Errorf("不匹配或重复记录 %s", key)
		}
		seen[key] = true
		if c.Stage == StageBuild {
			// 记录侧与题目侧走同一评估专用解码 + 规范编码路径,
			// v1 冻结记录与 v2 新记录按语义比较,不做两套判定。
			canonical, err := schemas.EncodeRequirementSpec(c.Requirement)
			recorded, derr := schemas.DecodeLegacyRequirementSpec(r.Requirement)
			canonicalRecorded, eerr := schemas.EncodeRequirementSpec(recorded)
			var actual, expected any
			if err != nil || derr != nil || eerr != nil || json.Unmarshal(canonicalRecorded, &actual) != nil || json.Unmarshal(canonical, &expected) != nil || !reflect.DeepEqual(actual, expected) {
				return meta, nil, fmt.Errorf("%s 记录需求与冻结题目不一致", key)
			}
		}
		if meta.HarnessProfile != nil {
			if r.Result != nil && (r.Result.Attempts < 0 || r.Result.Attempts > meta.HarnessProfile.AttemptLimit) {
				return meta, nil, fmt.Errorf("%s 实际尝试次数超出声明配置", key)
			}
			if !meta.HarnessProfile.Semantic && r.Usage != nil && r.Usage.EmbeddingCalls != 0 {
				return meta, nil, fmt.Errorf("%s 声明关闭语义检索但仍调用 Embedding", key)
			}
		}
		if r.RunErr != "" {
			v := Verdict{Failures: []AssertionFailure{{ID: "RUN", Name: "执行错误", Detail: r.RunErr}}}
			if !reflect.DeepEqual(v, r.Verdict) || !reflect.DeepEqual(Attribute(v.Failures), r.Attribution) {
				return meta, nil, fmt.Errorf("%s 执行错误判分或归因不一致", key)
			}
			continue
		}
		var v Verdict
		if c.Stage == StageScreening {
			if r.Screening == nil {
				return meta, nil, fmt.Errorf("缺少初筛原文")
			}
			if err := CheckDialogueEvidence(c, *r.Screening); err != nil {
				return meta, nil, fmt.Errorf("%s: %w", key, err)
			}
			v = AssertScreeningOutput(c, *r.Screening)
		} else {
			if r.Result == nil {
				return meta, nil, fmt.Errorf("缺少构建轨迹")
			}
			v, err = GradeBuild(c, r, meta.GraderVersion)
			if err != nil {
				return meta, nil, err
			}
		}
		if !reflect.DeepEqual(v, r.Verdict) || !reflect.DeepEqual(Attribute(v.Failures), r.Attribution) {
			return meta, nil, fmt.Errorf("%s 判卷与保存结果不一致", key)
		}
	}
	return meta, records, nil
}
