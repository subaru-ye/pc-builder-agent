package producthttp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestOwnershipEditPersistence(t *testing.T) {
	_, service, _ := requirementIntegrationAPI(t, true)
	ctx := context.Background()
	owner := "offline-ownership-owner"
	ws, err := service.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	read := func() (product.SessionDetail, schemas.RequirementState) {
		t.Helper()
		detail, err := service.GetSession(ctx, owner, ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		var state schemas.RequirementState
		if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
			t.Fatal(err)
		}
		return detail, state
	}
	edit := func(op schemas.RequirementOperation) schemas.RequirementState {
		t.Helper()
		_, before := read()
		request := product.RequirementEdit{ExpectedRevision: before.Revision, Operations: []schemas.RequirementOperation{op}}
		key := uuid.NewString()
		first, err := service.EditRequirement(ctx, owner, ws.ID, key, request)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := service.EditRequirement(ctx, owner, ws.ID, key, request)
		if err != nil || !reflect.DeepEqual(first.Session.RequirementState, retry.Session.RequirementState) {
			t.Fatal("edit retry duplicated or changed linked state")
		}
		_, refreshed := read()
		if refreshed.Revision != before.Revision+1 {
			t.Fatal("linked operation must share one revision")
		}
		return refreshed
	}
	state := edit(schemas.RequirementOperation{Op: "set", Field: "owned_parts", Value: json.RawMessage(`[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1}]`), Kind: "fact"})
	if string(state.Fields["existing_parts"].Value) != `["cpu"]` {
		t.Fatal("category did not persist")
	}
	state = edit(schemas.RequirementOperation{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Scope: "temporary", Kind: "fact"})
	if string(state.Fields["owned_parts"].Value) != `[]` || state.Fields["owned_parts"].Previous == nil {
		t.Fatal("temporary removal not persisted")
	}
	state = edit(schemas.RequirementOperation{Op: "restore", Field: "existing_parts"})
	var owned []schemas.OwnedPart
	if err := json.Unmarshal(state.Fields["owned_parts"].Value, &owned); err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0].Category != schemas.CategoryCPU || owned[0].Model != "AMD Ryzen 5 7600" || owned[0].Quantity != 1 || len(state.Changes) != 2 {
		t.Fatal("refresh/restore lost linked models or history")
	}
	state = edit(schemas.RequirementOperation{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Scope: "session", Kind: "fact"})
	if string(state.Fields["owned_parts"].Value) != `[]` || state.Fields["owned_parts"].Previous != nil {
		t.Fatal("permanent removal kept recoverable model")
	}
	state = edit(schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`), Kind: "constraint"})
	if string(state.Fields["owned_parts"].Value) != `[]` {
		t.Fatal("budget update revived CPU")
	}
	first, _ := read()
	second, _ := read()
	if !reflect.DeepEqual(first.Session.RequirementState, second.Session.RequirementState) || second.Session.VersionCount != 0 {
		t.Fatal("read changed state or generated a version")
	}
}
