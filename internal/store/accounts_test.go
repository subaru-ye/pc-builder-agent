package store

import (
	"context"
	"errors"
	"testing"
)

func TestProductUserClaimsMultipleOwnersWithoutRewritingSessions(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	_, err := s.CreateWebSession(ctx, "claim-session-a", "owner-a", "00000000-0000-4000-8000-000000000011")
	if err != nil {
		t.Fatal(err)
	}
	u, claimed, err := s.UpsertProductUserAndClaim(ctx, "00000000-0000-4000-8000-000000000012", "a@example.com", "用户 A", "owner-a")
	if err != nil || claimed != 1 {
		t.Fatalf("user=%+v claimed=%d err=%v", u, claimed, err)
	}
	if _, err := s.WebSessionByOwner(ctx, "owner-a", "claim-session-a"); err != nil {
		t.Fatalf("认领不应改写历史 owner: %v", err)
	}
	_, claimed, err = s.UpsertProductUserAndClaim(ctx, u.AuthSubject, u.Email, "", "owner-b")
	if err != nil || claimed != 0 {
		t.Fatalf("第二 owner 认领失败 claimed=%d err=%v", claimed, err)
	}
	owners, primary, err := s.ProductUserOwners(ctx, u.ID)
	if err != nil || len(owners) != 2 || primary != "owner-a" {
		t.Fatalf("owners=%v primary=%q err=%v", owners, primary, err)
	}
	_, _, err = s.UpsertProductUserAndClaim(ctx, "00000000-0000-4000-8000-000000000013", "b@example.com", "用户 B", "owner-a")
	if !errors.Is(err, ErrOwnerAlreadyClaimed) {
		t.Fatalf("跨账号重复认领应隔离: %v", err)
	}
}
