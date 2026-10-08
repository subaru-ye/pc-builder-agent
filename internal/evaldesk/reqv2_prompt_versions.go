package evaldesk

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

type ReqV2PromptVersion struct {
	ID         string                 `json:"id"`
	Role       string                 `json:"role"`
	Source     string                 `json:"source"` // git | run_snapshot
	Commit     string                 `json:"commit,omitempty"`
	CreatedAt  string                 `json:"created_at"`
	Subject    string                 `json:"subject,omitempty"`
	Runs       []string               `json:"runs,omitempty"`
	Components []ReqV2PromptComponent `json:"components"`
	Review     *ReqV2PromptReview     `json:"review,omitempty"`
}

type ReqV2PromptReview struct {
	Commit       string `json:"commit"`
	Role         string `json:"role"`
	Title        string `json:"title"`
	Reason       string `json:"reason"`
	Changes      string `json:"changes"`
	Verification struct {
		Kind       string `json:"kind"` // live_record | offline_record | tests_added | not_recorded
		Summary    string `json:"summary"`
		Limitation string `json:"limitation"`
	} `json:"verification"`
	Evidence []struct {
		Kind      string `json:"kind"`
		Reference string `json:"reference"`
		Excerpt   string `json:"excerpt"`
	} `json:"evidence"`
	RunNames []string `json:"run_names"`
}

// 固定的人工溯源记录；按完整提交与角色绑定，不按日期或提交标题猜测归因。
func (s *Store) promptReviews() (map[string]*ReqV2PromptReview, string) {
	index := map[string]*ReqV2PromptReview{}
	raw, err := s.read(filepath.Join(s.root, "docs", "eval"), "prompt-iterations.json")
	if os.IsNotExist(err) {
		return index, ""
	}
	var data struct {
		SchemaVersion int                 `json:"schema_version"`
		Reviews       []ReqV2PromptReview `json:"reviews"`
	}
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &data) != nil || data.SchemaVersion != 1 {
		return index, "提示词溯源记录无法读取或格式无效；原文与 diff 仍可查看。"
	}
	for i := range data.Reviews {
		review := &data.Reviews[i]
		_, hashErr := hex.DecodeString(review.Commit)
		key := review.Commit + ":" + review.Role
		if len(review.Commit) != 40 || hashErr != nil || (review.Role != "screening" && review.Role != "builder") || index[key] != nil || review.Title == "" || review.Reason == "" {
			return map[string]*ReqV2PromptReview{}, "提示词溯源记录的提交、角色或必填字段无效，未关联说明。"
		}
		if kind := review.Verification.Kind; kind != "live_record" && kind != "offline_record" && kind != "tests_added" && kind != "not_recorded" {
			return map[string]*ReqV2PromptReview{}, "提示词溯源记录的验证类型无效，未关联说明。"
		}
		index[key] = review
	}
	return index, ""
}

type ReqV2PromptVersions struct {
	Versions []ReqV2PromptVersion `json:"versions"`
	Notes    []string             `json:"notes"`
}

// 固定路径与常量名；历史源码只解析字符串，不编译或执行旧版本。
var promptHistoryFiles = []struct {
	role, path string
	names      map[string]string
}{
	{"screening", "internal/agents/pipeline/screening_requirement_state.go", map[string]string{"requirementStateInstruction": "system", "formatFallbackInstruction": "format_retry"}},
	{"builder", "internal/buildharness/prompt.go", map[string]string{"builderV2Instruction": "system"}},
}

func promptSourceComponents(raw []byte, role string, names map[string]string) ([]evalsuite.PromptComponent, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "prompt.go", raw, 0)
	if err != nil {
		return nil, err
	}
	var components []evalsuite.PromptComponent
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value := spec.(*ast.ValueSpec)
			for i, name := range value.Names {
				componentName := names[name.Name]
				if componentName == "" || i >= len(value.Values) {
					continue
				}
				literal, ok := value.Values[i].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return nil, fmt.Errorf("提示词常量不是独立字符串")
				}
				text, err := strconv.Unquote(literal.Value)
				if err != nil {
					return nil, err
				}
				components = append(components, evalsuite.PromptComponent{Role: role, Name: componentName, Text: text})
			}
		}
	}
	if len(components) == 0 {
		return nil, fmt.Errorf("该提交没有可读取的提示词常量")
	}
	return components, nil
}

type promptGitOutput struct{ bytes.Buffer }

func (b *promptGitOutput) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > 1<<20 {
		return 0, fmt.Errorf("Git 输出超过读取上限")
	}
	return b.Buffer.Write(raw)
}

func (s *Store) promptGit(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--no-replace-objects"}, args...)...)
	cmd.Dir = s.root
	var output promptGitOutput
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func promptVersion(components []evalsuite.PromptComponent) ([]ReqV2PromptComponent, string, error) {
	snapshot, identity, err := evalsuite.NewPromptEvidence(components)
	var out []ReqV2PromptComponent
	for _, component := range snapshot.Components {
		out = append(out, ReqV2PromptComponent{PromptComponent: component, SHA256: identity.Components[component.Role+"/"+component.Name]})
	}
	return out, identity.SHA256, err
}

func verifyPromptSnapshot(raw []byte, declared *evalsuite.PromptIdentity) (evalsuite.PromptSnapshot, error) {
	var snapshot evalsuite.PromptSnapshot
	if declared == nil || declared.SnapshotFile != "prompts.json" || json.Unmarshal(raw, &snapshot) != nil || snapshot.SchemaVersion != 1 {
		return snapshot, fmt.Errorf("提示词原文缺失或格式无效")
	}
	_, identity, err := evalsuite.NewPromptEvidence(snapshot.Components)
	if err != nil || !reflect.DeepEqual(identity, *declared) {
		return snapshot, fmt.Errorf("提示词原文与记录指纹不一致")
	}
	return snapshot, nil
}

func (s *Store) PromptVersionsReqV2() ReqV2PromptVersions {
	out := ReqV2PromptVersions{Versions: []ReqV2PromptVersion{}, Notes: []string{}}
	reviews, note := s.promptReviews()
	if note != "" {
		out.Notes = append(out.Notes, note)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, source := range promptHistoryFiles {
		log, err := s.promptGit(ctx, "log", "-80", "--format=%H%x09%cI%x09%s", "--", source.path)
		if err != nil {
			out.Notes = append(out.Notes, source.role+"：Git 历史暂不可读取，当前原文仍可查看。")
			continue
		}
		seen := map[string]bool{}
		lines := strings.Split(strings.TrimSpace(string(log)), "\n")
		if len(lines) == 80 {
			out.Notes = append(out.Notes, source.role+"：当前列出最近 80 个文件提交中的内容版本。")
		}
		// 从旧到新取首次出现的内容身份，普通代码改动不冒充提示词迭代。
		for index := len(lines) - 1; index >= 0; index-- {
			line := lines[index]
			fields := strings.SplitN(line, "\t", 3)
			if len(fields) != 3 || len(fields[0]) != 40 {
				continue
			}
			if _, err := hex.DecodeString(fields[0]); err != nil {
				continue
			}
			raw, err := s.promptGit(ctx, "show", fields[0]+":"+source.path)
			if err != nil {
				out.Notes = append(out.Notes, source.role+"：部分历史源码读取失败。")
				break
			}
			components, err := promptSourceComponents(raw, source.role, source.names)
			if err != nil {
				out.Notes = append(out.Notes, source.role+"：跳过一份无法解析的历史提示词。")
				continue
			}
			projected, hash, err := promptVersion(components)
			if err != nil || seen[hash] {
				continue
			}
			seen[hash] = true
			out.Versions = append(out.Versions, ReqV2PromptVersion{ID: "git:" + fields[0] + ":" + source.role, Role: source.role, Source: "git", Commit: fields[0], CreatedAt: fields[1], Subject: fields[2], Components: projected, Review: reviews[fields[0]+":"+source.role]})
		}
	}
	// 原文来自运行内固定文件，指纹与 plan 声明逐项一致才展示。
	dirs, _ := s.reqV2Directories()
	for id, dir := range dirs {
		planRaw, err := s.read(dir, "plan.json")
		if err != nil {
			continue
		}
		var plan struct {
			Prompts   *evalsuite.PromptIdentity `json:"prompts"`
			CreatedAt string                    `json:"created_at"`
		}
		if json.Unmarshal(planRaw, &plan) != nil || plan.Prompts == nil {
			continue
		}
		raw, err := s.read(dir, "prompts.json")
		if err != nil {
			out.Notes = append(out.Notes, "一份运行提示词原文缺失或格式无效，未展示。")
			continue
		}
		snapshot, err := verifyPromptSnapshot(raw, plan.Prompts)
		if err != nil {
			out.Notes = append(out.Notes, "一份运行提示词原文与记录指纹不一致，未展示。")
			continue
		}
		for _, role := range []string{"screening", "builder"} {
			var components []evalsuite.PromptComponent
			for _, component := range snapshot.Components {
				if component.Role == role {
					components = append(components, component)
				}
			}
			if len(components) == 0 {
				continue
			}
			projected, hash, _ := promptVersion(components)
			// 多次测试用同一原文时归为一个内容版本。
			found := false
			for i := range out.Versions {
				if out.Versions[i].ID == "saved:"+hash {
					out.Versions[i].Runs = append(out.Versions[i].Runs, id)
					created, err := time.Parse(time.RFC3339, plan.CreatedAt)
					existing, _ := time.Parse(time.RFC3339, out.Versions[i].CreatedAt)
					if err == nil && (existing.IsZero() || created.Before(existing)) {
						out.Versions[i].CreatedAt = plan.CreatedAt
					}
					found = true
				}
			}
			if !found {
				out.Versions = append(out.Versions, ReqV2PromptVersion{ID: "saved:" + hash, Role: role, Source: "run_snapshot", CreatedAt: plan.CreatedAt, Runs: []string{id}, Components: projected})
			}
		}
	}
	sort.Slice(out.Versions, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339, out.Versions[i].CreatedAt)
		b, _ := time.Parse(time.RFC3339, out.Versions[j].CreatedAt)
		if !a.Equal(b) {
			return a.After(b)
		}
		return out.Versions[i].ID < out.Versions[j].ID
	})
	for i := range out.Versions {
		sort.Strings(out.Versions[i].Runs)
	}
	return out
}
