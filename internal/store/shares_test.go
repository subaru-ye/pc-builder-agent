package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBuildShareLifecycleAndOwnership(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	owner := "owner-a"
	if _, err := s.CreateWebSession(ctx, "share-session", owner, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveBuildVersion(ctx, SaveBuildVersionParams{
		SessionID: "share-session", RequirementSpec: json.RawMessage(`{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"}}`),
		Draft:      json.RawMessage(`{"build_ref":"b","selection":{},"rationale":{}}`),
		Validation: json.RawMessage(`{"build_ref":"b","overall_status":"pass","checks":[]}`),
		Quote:      json.RawMessage(`{"schema_version":1,"build_ref":"b","snapshot_date":"2026-08-09","lines":[],"total_cny":"0.00","missing_count":0,"missing_skus":[]}`),
	}); err != nil {
		t.Fatal(err)
	}
	hash := bytes.Repeat([]byte{1}, 32)
	params := CreateBuildShareParams{OwnerID: owner, SessionID: "share-session", Version: 1,
		PublicID: uuid.NewString(), ClientRequestID: uuid.NewString(), TokenHash: hash}
	first, created, err := s.CreateBuildShare(ctx, params)
	if err != nil || !created {
		t.Fatalf("首次创建 err=%v created=%v", err, created)
	}
	params.PublicID = uuid.NewString()
	second, created, err := s.CreateBuildShare(ctx, params)
	if err != nil || created || second.PublicID != first.PublicID {
		t.Fatalf("幂等创建 second=%+v created=%v err=%v", second, created, err)
	}
	list, err := s.BuildSharesByOwner(ctx, owner, "share-session", 1)
	if err != nil || len(list) != 1 || !bytes.Equal(list[0].TokenHash, hash) {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if _, err := s.BuildSharesByOwner(ctx, "owner-b", "share-session", 1); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("越权列表 err=%v", err)
	}
	if _, err := s.PublicBuildShare(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeBuildShareByID(ctx, "owner-b", "share-session", 1, first.PublicID); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("越权撤销 err=%v", err)
	}
	if err := s.RevokeBuildShareByID(ctx, owner, "share-session", 1, first.PublicID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeBuildShareByID(ctx, owner, "share-session", 1, first.PublicID); err != nil {
		t.Fatalf("重复撤销必须幂等:%v", err)
	}
	if _, err := s.PublicBuildShare(ctx, hash); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("撤销后公开读取 err=%v", err)
	}
}

func TestBuildShareRejectsHashMismatchForSameRequest(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	if _, err := s.CreateWebSession(ctx, "share-conflict", "owner", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveBuildVersion(ctx, SaveBuildVersionParams{SessionID: "share-conflict",
		RequirementSpec: json.RawMessage(`{"schema_version":1}`), Draft: json.RawMessage(`{}`), Validation: json.RawMessage(`{}`), Quote: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	base := CreateBuildShareParams{OwnerID: "owner", SessionID: "share-conflict", Version: 1,
		PublicID: uuid.NewString(), ClientRequestID: requestID, TokenHash: bytes.Repeat([]byte{1}, 32)}
	if _, _, err := s.CreateBuildShare(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.PublicID, base.TokenHash = uuid.NewString(), bytes.Repeat([]byte{2}, 32)
	if _, _, err := s.CreateBuildShare(ctx, base); !errors.Is(err, ErrShareTokenMismatch) {
		t.Fatalf("hash mismatch err=%v", err)
	}
}
