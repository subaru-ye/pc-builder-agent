package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrShareNotFound      = errors.New("分享不存在")
	ErrShareTokenMismatch = errors.New("分享幂等 token 不一致")
)

// BuildShare 是分享存储投影。PublicID 可安全暴露给所有者，内部 bigint 主键不出 API。
type BuildShare struct {
	PublicID        string
	BuildID         int64
	SessionID       string
	Version         int
	ClientRequestID string
	TokenHash       []byte
	CreatedAt       time.Time
	RevokedAt       *time.Time
}

type CreateBuildShareParams struct {
	OwnerID, SessionID, PublicID, ClientRequestID string
	Version                                       int
	TokenHash                                     []byte
}

func scanBuildShare(row interface{ Scan(...any) error }) (BuildShare, error) {
	var share BuildShare
	err := row.Scan(&share.PublicID, &share.BuildID, &share.SessionID, &share.Version,
		&share.ClientRequestID, &share.TokenHash, &share.CreatedAt, &share.RevokedAt)
	return share, err
}

const buildShareColumns = `s.public_id::text, s.build_id, b.session_id, b.version,
       s.client_request_id::text, s.token_hash, s.created_at, s.revoked_at`

// CreateBuildShare 在单事务中验证所有权并创建分享；相同 build/request 返回原记录。
func (s *Store) CreateBuildShare(ctx context.Context, p CreateBuildShareParams) (BuildShare, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BuildShare{}, false, fmt.Errorf("store: 开启分享事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var buildID int64
	err = tx.QueryRow(ctx, `
		SELECT b.id FROM builds b
		JOIN web_sessions ws ON ws.id = b.session_id
		WHERE b.session_id = $1 AND b.version = $2 AND ws.owner_id = $3
		FOR UPDATE OF b`, p.SessionID, p.Version, p.OwnerID).Scan(&buildID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BuildShare{}, false, ErrBuildNotFound
	}
	if err != nil {
		return BuildShare{}, false, fmt.Errorf("store: 校验分享版本所有权失败: %w", err)
	}

	share, err := scanBuildShare(tx.QueryRow(ctx, `
		SELECT `+buildShareColumns+` FROM build_shares s
		JOIN builds b ON b.id = s.build_id
		WHERE s.build_id = $1 AND s.client_request_id = $2`, buildID, p.ClientRequestID))
	if err == nil {
		if !bytes.Equal(share.TokenHash, p.TokenHash) {
			return BuildShare{}, false, ErrShareTokenMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return BuildShare{}, false, fmt.Errorf("store: 提交分享幂等查询失败: %w", err)
		}
		return share, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BuildShare{}, false, fmt.Errorf("store: 查询分享幂等记录失败: %w", err)
	}

	share, err = scanBuildShare(tx.QueryRow(ctx, `
		INSERT INTO build_shares (build_id, public_id, client_request_id, token_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING public_id::text, build_id, $5::text, $6::int,
		          client_request_id::text, token_hash, created_at, revoked_at`,
		buildID, p.PublicID, p.ClientRequestID, p.TokenHash, p.SessionID, p.Version))
	if err != nil {
		return BuildShare{}, false, fmt.Errorf("store: 创建分享失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BuildShare{}, false, fmt.Errorf("store: 提交分享事务失败: %w", err)
	}
	return share, true, nil
}

// BuildSharesByOwner 列出一个 Web build 的全部分享，包括已撤销记录。
func (s *Store) BuildSharesByOwner(ctx context.Context, ownerID, sessionID string, version int) ([]BuildShare, error) {
	var buildID int64
	if err := s.pool.QueryRow(ctx, `
		SELECT b.id FROM builds b
		JOIN web_sessions ws ON ws.id = b.session_id
		WHERE b.session_id = $1 AND b.version = $2 AND ws.owner_id = $3`,
		sessionID, version, ownerID).Scan(&buildID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBuildNotFound
		}
		return nil, fmt.Errorf("store: 校验分享所属配置失败: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+buildShareColumns+` FROM build_shares s
		JOIN builds b ON b.id = s.build_id
		WHERE s.build_id = $1
		ORDER BY s.created_at DESC, s.public_id`, buildID)
	if err != nil {
		return nil, fmt.Errorf("store: 列出分享失败: %w", err)
	}
	defer rows.Close()
	shares := make([]BuildShare, 0)
	for rows.Next() {
		share, err := scanBuildShare(rows)
		if err != nil {
			return nil, fmt.Errorf("store: 读取分享失败: %w", err)
		}
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历分享失败: %w", err)
	}
	return shares, nil
}

func (s *Store) RevokeBuildShareByID(ctx context.Context, ownerID, sessionID string, version int, publicID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE build_shares s SET revoked_at = COALESCE(s.revoked_at, now())
		FROM builds b, web_sessions ws
		WHERE s.build_id = b.id AND ws.id = b.session_id
		  AND s.public_id = $1 AND b.session_id = $2 AND b.version = $3 AND ws.owner_id = $4`,
		publicID, sessionID, version, ownerID)
	if err != nil {
		return fmt.Errorf("store: 按 ID 撤销分享失败: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrShareNotFound
	}
	return nil
}

func (s *Store) RevokeBuildShareByToken(ctx context.Context, ownerID string, tokenHash []byte) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE build_shares s SET revoked_at = COALESCE(s.revoked_at, now())
		FROM builds b, web_sessions ws
		WHERE s.build_id = b.id AND ws.id = b.session_id
		  AND s.token_hash = $1 AND ws.owner_id = $2`, tokenHash, ownerID)
	if err != nil {
		return fmt.Errorf("store: 按 token 撤销分享失败: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrShareNotFound
	}
	return nil
}

// PublicBuildShare 只返回未撤销分享的内部定位信息，公开 DTO 由分享 service 映射。
func (s *Store) PublicBuildShare(ctx context.Context, tokenHash []byte) (BuildShare, error) {
	share, err := scanBuildShare(s.pool.QueryRow(ctx, `
		SELECT `+buildShareColumns+` FROM build_shares s
		JOIN builds b ON b.id = s.build_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL`, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return BuildShare{}, ErrShareNotFound
	}
	if err != nil {
		return BuildShare{}, fmt.Errorf("store: 查询公开分享失败: %w", err)
	}
	return share, nil
}
