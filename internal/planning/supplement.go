package planning

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// supplement reuses page registration checks, then fills only missing local
// facts. No catalog writer is available here; quotes always come from the catalog.
func (x *execution) supplement(c Candidate) error {
	index := -1
	for i, old := range x.candidates {
		if old.ID == c.ID && !old.External {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("本地候选不存在；新型号请使用ext-编号")
	}
	old := x.candidates[index]
	if c.Category != old.Category || c.Brand != old.Brand || c.Model != old.Model {
		return fmt.Errorf("补充规格必须对应本地候选的准确品类、品牌和型号")
	}
	verified := execution{evidence: x.evidence}
	c.ID = "ext-supplement"
	c.Price = nil
	if err := verified.register(c); err != nil {
		return err
	}
	c = verified.candidates[0]
	if err := localSpecShape(c); err != nil {
		return fmt.Errorf("补充规格格式无效：%w", err)
	}
	var current, additions map[string]json.RawMessage
	if json.Unmarshal(old.Specs, &current) != nil || current == nil {
		return fmt.Errorf("本地规格无法读取，不能覆盖")
	}
	_ = json.Unmarshal(c.Specs, &additions)
	old.FieldEvidence = copyFieldMap(old.FieldEvidence)
	old.FieldQuotes = copyFieldMap(old.FieldQuotes)
	bindings := []Evidence{}
	for field, value := range additions {
		if string(value) == "null" {
			continue
		}
		if existing := current[field]; len(existing) > 0 && string(existing) != "null" {
			var a, b any
			_ = json.Unmarshal(existing, &a)
			_ = json.Unmarshal(value, &b)
			if !reflect.DeepEqual(a, b) {
				return fmt.Errorf("%s 与本地已知规格冲突，保留原值；请继续核实准确变体", field)
			}
			continue
		}
		current[field] = value
		for _, source := range x.evidence {
			if source.ID == c.FieldEvidence[field] {
				source.ID = "supplement:" + old.ID + ":" + field
				source.CandidateID, source.Field, source.Text = old.ID, field, c.FieldQuotes[field]
				bindings = append(bindings, source)
				old.FieldEvidence[field], old.FieldQuotes[field] = source.ID, source.Text
				break
			}
		}
	}
	old.Specs, _ = json.Marshal(current)
	old.FieldEvidence["model"] = c.FieldEvidence["model"]
	old.FieldQuotes["model"] = c.FieldQuotes["model"]
	old.Evidence = append(append([]string{}, old.Evidence...), c.Evidence...)
	old.Unknown = append([]string{}, c.Unknown...)
	// An earlier successful evaluation cannot validate newly registered facts.
	if string(old.Specs) != string(x.candidates[index].Specs) {
		if draft, err := schemas.DecodeBuildDraft(x.result.Draft); err == nil {
			for _, id := range draft.Selection.SKUs() {
				if id == old.ID {
					x.result.Validation, x.result.Quote = nil, nil
				}
			}
		}
	}
	x.candidates[index] = old
	for _, binding := range bindings {
		found := false
		for i := range x.evidence {
			if x.evidence[i].ID == binding.ID {
				x.evidence[i], found = binding, true
				break
			}
		}
		if !found {
			x.evidence = append(x.evidence, binding)
		}
	}
	return nil
}

// Reuse the canonical decoders at this write boundary so a malformed value
// cannot become a known session fact that later corrections cannot replace.
func localSpecShape(c Candidate) (err error) {
	switch c.Category {
	case schemas.CategoryCPU:
		_, err = schemas.DecodeCPUSpec(c.Specs)
	case schemas.CategoryGPU:
		_, err = schemas.DecodeGPUSpec(c.Specs)
	case schemas.CategoryMotherboard:
		_, err = schemas.DecodeMotherboardSpec(c.Specs)
	case schemas.CategoryMemory:
		_, err = schemas.DecodeMemorySpec(c.Specs)
	case schemas.CategorySSD:
		_, err = schemas.DecodeSSDSpec(c.Specs)
	case schemas.CategoryPSU:
		_, err = schemas.DecodePSUSpec(c.Specs)
	case schemas.CategoryCase:
		_, err = schemas.DecodeCaseSpec(c.Specs)
	case schemas.CategoryCooler:
		_, err = schemas.DecodeCoolerSpec(c.Specs)
	}
	return err
}

// Only sourced additions are restored. Fresh catalog identity, known facts and
// prices take precedence; a conflict remains visible instead of replacing them.
func (x *execution) restoreSupplement(c Candidate) {
	var specs map[string]json.RawMessage
	if json.Unmarshal(c.Specs, &specs) != nil {
		return
	}
	for field := range specs {
		if c.FieldEvidence[field] == "" && c.FieldEvidence["specs."+field] == "" {
			delete(specs, field)
		}
	}
	c.Specs, _ = json.Marshal(specs)
	if err := x.supplement(c); err != nil {
		for i := range x.candidates {
			if x.candidates[i].ID == c.ID {
				x.candidates[i].Unknown = append(x.candidates[i].Unknown, "上次规格补充未沿用："+strings.TrimSpace(err.Error()))
			}
		}
	}
}
