package evalsuite

import (
	"encoding/json"
	"sort"
	"strings"
)

// MatchEvidence 仅表示本轮检索返回了这段文本；Selected 不等于偏好质量通过。
type MatchEvidence struct {
	SKU      string `json:"sku"`
	Category string `json:"category"`
	Text     string `json:"text"`
	Selected bool   `json:"selected_in_delivered_build"`
}
type CandidateEvidence struct {
	Recorded     bool            `json:"recorded"`
	BundleSHA256 string          `json:"bundle_sha256,omitempty"`
	SKUs         []string        `json:"candidate_skus,omitempty"`
	Delivered    bool            `json:"delivered"`
	SelectedSKUs []string        `json:"selected_skus,omitempty"`
	Matches      []MatchEvidence `json:"matches,omitempty"`
}
type SemanticTrial struct {
	Seed        int               `json:"repeat"`
	Requirement json.RawMessage   `json:"requirement"`
	A           CandidateEvidence `json:"a"`
	B           CandidateEvidence `json:"b"`
	AddedInB    []string          `json:"added_candidate_skus_in_b,omitempty"`
	RemovedInB  []string          `json:"removed_candidate_skus_in_b,omitempty"`
}

func semanticTrial(a, b CaseRecord) SemanticTrial {
	t := SemanticTrial{Seed: a.Seed, Requirement: a.Requirement, A: candidateEvidence(a), B: candidateEvidence(b)}
	if t.A.Recorded && t.B.Recorded {
		t.AddedInB = difference(t.B.SKUs, t.A.SKUs)
		t.RemovedInB = difference(t.A.SKUs, t.B.SKUs)
	}
	return t
}
func candidateEvidence(r CaseRecord) CandidateEvidence {
	e := CandidateEvidence{Recorded: r.Candidates != nil}
	selected := map[string]bool{}
	if r.Result != nil && r.Result.Succeeded {
		e.Delivered = true
		for _, sku := range r.Result.Draft.Selection.SKUs() {
			if sku != "" {
				selected[sku] = true
				e.SelectedSKUs = append(e.SelectedSKUs, sku)
			}
		}
	}
	if r.Candidates == nil {
		return e
	}
	raw, _ := json.Marshal(r.Candidates)
	e.BundleSHA256, _ = JSONHash(raw)
	for _, group := range r.Candidates.Groups {
		for _, c := range group.Candidates {
			e.SKUs = append(e.SKUs, c.SKU)
			if strings.TrimSpace(c.MatchText) != "" {
				e.Matches = append(e.Matches, MatchEvidence{SKU: c.SKU, Category: string(group.Category), Text: c.MatchText, Selected: selected[c.SKU]})
			}
		}
	}
	sort.Strings(e.SKUs)
	return e
}
func difference(a, b []string) []string {
	set := map[string]bool{}
	for _, v := range b {
		set[v] = true
	}
	var result []string
	for _, v := range a {
		if !set[v] {
			result = append(result, v)
		}
	}
	return result
}
