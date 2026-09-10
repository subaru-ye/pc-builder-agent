package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSessionManagementPersistenceAndDeletion(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	id, owner := uuid.NewString(), "session-management-owner"
	if _, err := s.CreateWebSession(ctx, id, owner, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	title := "新会话"
	if err := s.ManageWebSession(ctx, "other-owner", id, &title, nil, true); !errors.Is(err, ErrWebSessionNotFound) {
		t.Fatalf("ownership: %v", err)
	}
	if err := s.ManageWebSession(ctx, owner, id, &title, nil, false); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.StartMessageRun(ctx, StartMessageRunParams{OwnerID: owner, SessionID: id, RequestID: uuid.NewString(), RunID: uuid.NewString(), MessageID: uuid.NewString(), Text: "预算8000", Title: "自动标题"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ManageWebSession(ctx, owner, id, nil, nil, true); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("active run deletion: %v", err)
	}
	ws, err := s.WebSessionByOwner(ctx, owner, id)
	if err != nil || ws.Title != title {
		t.Fatalf("custom title overwritten: %+v %v", ws, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE agent_runs SET status='succeeded' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	var req, build1, build2 int64
	if err = s.pool.QueryRow(ctx, `INSERT INTO requirements(session_id,spec) VALUES($1,'{}') RETURNING id`, id).Scan(&req); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `INSERT INTO builds(session_id,version,requirement_id,draft,validation,quote) VALUES($1,1,$2,'{}','{}','{}') RETURNING id`, id, req).Scan(&build1); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `INSERT INTO builds(session_id,version,parent_id,requirement_id,draft,validation,quote) VALUES($1,2,$2,$3,'{}','{}','{}') RETURNING id`, id, build1, req).Scan(&build2); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO build_shares(build_id,token_hash) VALUES($1,$2)`, build2, []byte("management-share")); err != nil {
		t.Fatal(err)
	}
	for _, archived := range []bool{true, false} {
		title = "朋友的电脑"
		if err = s.ManageWebSession(ctx, owner, id, &title, &archived, false); err != nil {
			t.Fatal(err)
		}
		ws, err = s.WebSessionByOwner(ctx, owner, id)
		if err != nil || ws.Archived != archived || ws.Title != title || ws.VersionCount != 2 {
			t.Fatalf("metadata must preserve builds: %+v %v", ws, err)
		}
		messages, err := s.WebMessages(ctx, id)
		if err != nil || len(messages) != 1 {
			t.Fatalf("messages changed: %v", err)
		}
	}
	if err = s.ManageWebSession(ctx, owner, id, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WebSessionByOwner(ctx, owner, id); !errors.Is(err, ErrWebSessionNotFound) {
		t.Fatalf("deleted session accessible: %v", err)
	}
	if _, err = s.RunByOwner(ctx, owner, run.ID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("deleted run accessible: %v", err)
	}
	for _, table := range []string{"builds", "requirements", "web_messages", "agent_runs"} {
		var count int
		if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE session_id=$1", id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s not deleted: %d %v", table, count, err)
		}
	}
	var shares int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM build_shares WHERE build_id=$1`, build2).Scan(&shares); err != nil || shares != 0 {
		t.Fatalf("share remains: %d %v", shares, err)
	}
}
