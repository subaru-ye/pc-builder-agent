package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ManageWebSession 串行化会话管理与新运行；删除在同一事务移除版本及其分享。
func (s *Store) ManageWebSession(ctx context.Context, owner, id string, title *string, archived *bool, remove bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var found string
	if err = tx.QueryRow(ctx, `SELECT id FROM web_sessions WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWebSessionNotFound
		}
		return err
	}
	if remove {
		var busy bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runs WHERE session_id=$1 AND status='running')`, id).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrSessionBusy
		}
		// builds/requirements 也服务旧开发会话，未设置 web_sessions 外键，显式清理。
		for _, query := range []string{
			`DELETE FROM session_proposals WHERE session_id=$1`,
			`DELETE FROM builds WHERE session_id=$1`,
			`DELETE FROM requirements r WHERE session_id=$1 AND NOT EXISTS(SELECT 1 FROM builds b WHERE b.requirement_id=r.id)`,
			`DELETE FROM web_sessions WHERE id=$1`,
		} {
			if _, err = tx.Exec(ctx, query, id); err != nil {
				return err
			}
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE web_sessions SET title=COALESCE($2,title), title_custom=title_custom OR $2 IS NOT NULL,
			archived=COALESCE($3,archived), updated_at=now() WHERE id=$1`, id, title, archived)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
