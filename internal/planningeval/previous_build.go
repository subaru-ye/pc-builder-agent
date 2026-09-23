package planningeval

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// Restore the confirmed session precondition as well as its immutable version.
// A saved version alone does not grant the session permission to continue a plan.
func seedSnapshotConfirmation(ctx context.Context, st *store.Store, svc *product.Service, owner, sessionID string, f PreviousBuildFixture) error {
	state := schemas.LegacyPlanningState(f.Requirement)
	var ops []schemas.RequirementOperation
	for key, value := range state.Fields {
		if value.Status == "active" {
			ops = append(ops, schemas.RequirementOperation{Op: "set", Field: key, Value: value.Value, Kind: value.Kind, Strength: value.Strength})
		}
	}
	if _, err := svc.EditRequirement(ctx, owner, sessionID, uuid.NewString(), product.RequirementEdit{Operations: ops}); err != nil {
		return err
	}
	run, _, _, err := st.StartConfirmRun(ctx, store.StartConfirmRunParams{OwnerID: owner, SessionID: sessionID, RequestID: uuid.NewString(), RunID: uuid.NewString()})
	if err != nil {
		return err
	}
	_, err = st.CompleteRun(ctx, store.CompleteRunParams{RunID: run.ID, SessionID: sessionID, Status: store.RunSucceeded, Phase: store.PhaseReady})
	return err
}

func validatePreviousBuild(f PreviousBuildFixture) error {
	if f.Source == "" {
		return fmt.Errorf("historical build source required")
	}
	selection, err := schemas.DecodeBuildSelection(f.Selection)
	if err != nil {
		return err
	}
	if f.Snapshot != nil {
		draft, decodeErr := schemas.DecodeBuildDraft(f.Snapshot.Draft)
		if decodeErr != nil || f.Snapshot.Validation == nil || f.Snapshot.Quote == nil || len(f.Snapshot.Candidates) == 0 {
			return fmt.Errorf("historical snapshot requires a valid draft, candidates, validation and quote")
		}
		if !reflect.DeepEqual(draft.Selection, selection) {
			return fmt.Errorf("historical snapshot selection differs from precondition")
		}
	}
	_, err = schemas.DecodeLegacyRequirementSpec(f.Requirement)
	return err
}

func seedPreviousBuild(ctx context.Context, st *store.Store, sessionID string, f PreviousBuildFixture) (store.BuildVersion, error) {
	if err := validatePreviousBuild(f); err != nil {
		return store.BuildVersion{}, err
	}
	selection, _ := schemas.DecodeBuildSelection(f.Selection)
	if f.Snapshot != nil {
		// Do not reprice an immutable old version using the current catalog or
		// import its retired candidates into the new planning search space.
		report, _ := json.Marshal(f.Snapshot.Validation)
		quote := *f.Snapshot.Quote
		quote.SnapshotID = 0 // Source database IDs cannot identify test DB snapshots.
		quoteJSON, _ := json.Marshal(quote)
		snapshotJSON, _ := json.Marshal(f.Snapshot)
		saved, err := st.SaveBuildVersion(ctx, store.SaveBuildVersionParams{SessionID: sessionID, RequirementSpec: f.Requirement,
			Draft: f.Snapshot.Draft, Validation: report, Quote: quoteJSON, CandidateSnapshot: snapshotJSON})
		if err != nil {
			return store.BuildVersion{}, err
		}
		return st.BuildByVersion(ctx, sessionID, saved.Version)
	}
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
