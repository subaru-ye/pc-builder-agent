package planningeval

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func validatePreviousBuild(f PreviousBuildFixture) error {
	if f.Source == "" {
		return fmt.Errorf("historical build source required")
	}
	if _, err := schemas.DecodeBuildSelection(f.Selection); err != nil {
		return err
	}
	_, err := schemas.DecodeRequirementSpec(f.Requirement)
	return err
}

func seedPreviousBuild(ctx context.Context, st *store.Store, sessionID string, f PreviousBuildFixture) (store.BuildVersion, error) {
	if err := validatePreviousBuild(f); err != nil {
		return store.BuildVersion{}, err
	}
	selection, _ := schemas.DecodeBuildSelection(f.Selection)
	// Recompute facts against this evaluation's frozen catalog. Do not copy an
	// old 'pass' or claim old prices are current; gaps remain in the base record.
	facts, err := validate.New(st).Evaluate(ctx, selection)
	if err != nil {
		return store.BuildVersion{}, err
	}
	var wire struct{ Parts json.RawMessage }
	_ = json.Unmarshal(f.Selection, &wire) // Already checked with the strict decoder.
	draft, err := json.Marshal(map[string]any{"schema_version": 1, "requirement_ref": "historical-fixture", "build_ref": selection.BuildRef, "selection": wire.Parts})
	if err != nil {
		return store.BuildVersion{}, err
	}
	report, _ := json.Marshal(facts.Report)
	quote, _ := json.Marshal(facts.Quote)
	saved, err := st.SaveBuildVersion(ctx, store.SaveBuildVersionParams{SessionID: sessionID, RequirementSpec: f.Requirement, Draft: draft, Validation: report, Quote: quote})
	if err != nil {
		return store.BuildVersion{}, err
	}
	return st.BuildByVersion(ctx, sessionID, saved.Version)
}
