package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrOwnerAlreadyClaimed = errors.New("匿名身份已被其他账号认领")

type ProductUser struct {
	ID          string
	AuthSubject string
	Email       string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func scanProductUser(row interface{ Scan(...any) error }) (ProductUser, error) {
	var u ProductUser
	err := row.Scan(&u.ID, &u.AuthSubject, &u.Email, &u.DisplayName, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// UpsertProductUserAndClaim 在一个事务中建立身份投影并认领当前匿名 owner。
// 它不改写 web_sessions.owner_id，从而保持 Agent contextID 与分享 token 稳定。
func (s *Store) UpsertProductUserAndClaim(ctx context.Context, authSubject, email, displayName, ownerID string) (ProductUser, int, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProductUser{}, 0, fmt.Errorf("store: 开始账号认领事务: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := scanProductUser(tx.QueryRow(ctx, `
		INSERT INTO product_users (auth_subject, email, display_name)
		VALUES ($1, $2, COALESCE(NULLIF($3, ''), split_part($2, '@', 1)))
		ON CONFLICT (auth_subject) DO UPDATE SET
			email = EXCLUDED.email,
			display_name = CASE WHEN $3 = '' THEN product_users.display_name ELSE EXCLUDED.display_name END,
			updated_at = now()
		RETURNING id::text, auth_subject::text, email, display_name, created_at, updated_at`,
		authSubject, email, displayName))
	if err != nil {
		return ProductUser{}, 0, fmt.Errorf("store: 写入产品用户: %w", err)
	}

	var existingUser string
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM product_user_owners WHERE owner_id = $1 FOR UPDATE`, ownerID).Scan(&existingUser)
	if err == nil && existingUser != u.ID {
		return ProductUser{}, 0, ErrOwnerAlreadyClaimed
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ProductUser{}, 0, fmt.Errorf("store: 查询 owner 认领状态: %w", err)
	}

	claimedSessions := 0
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM web_sessions WHERE owner_id = $1`, ownerID).Scan(&claimedSessions); err != nil {
			return ProductUser{}, 0, fmt.Errorf("store: 统计待认领会话: %w", err)
		}
		var hasPrimary bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_user_owners WHERE user_id = $1 AND is_primary)`, u.ID).Scan(&hasPrimary); err != nil {
			return ProductUser{}, 0, fmt.Errorf("store: 查询主 owner: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_user_owners (user_id, owner_id, is_primary)
			VALUES ($1, $2, $3)`, u.ID, ownerID, !hasPrimary); err != nil {
			return ProductUser{}, 0, fmt.Errorf("store: 认领 owner: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ProductUser{}, 0, fmt.Errorf("store: 提交账号认领事务: %w", err)
	}
	return u, claimedSessions, nil
}

func (s *Store) ProductUser(ctx context.Context, id string) (ProductUser, error) {
	u, err := scanProductUser(s.pool.QueryRow(ctx, `
		SELECT id::text, auth_subject::text, email, display_name, created_at, updated_at
		FROM product_users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductUser{}, ErrWebSessionNotFound
	}
	if err != nil {
		return ProductUser{}, fmt.Errorf("store: 查询产品用户: %w", err)
	}
	return u, nil
}

func (s *Store) ProductUserOwners(ctx context.Context, userID string) ([]string, string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT owner_id, is_primary FROM product_user_owners
		WHERE user_id = $1 ORDER BY is_primary DESC, claimed_at, owner_id`, userID)
	if err != nil {
		return nil, "", fmt.Errorf("store: 查询账号 owner: %w", err)
	}
	defer rows.Close()
	var owners []string
	var primary string
	for rows.Next() {
		var owner string
		var isPrimary bool
		if err := rows.Scan(&owner, &isPrimary); err != nil {
			return nil, "", fmt.Errorf("store: 读取账号 owner: %w", err)
		}
		owners = append(owners, owner)
		if isPrimary {
			primary = owner
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: 遍历账号 owner: %w", err)
	}
	return owners, primary, nil
}

func (s *Store) OwnerClaimed(ctx context.Context, ownerID string) (bool, error) {
	var claimed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_user_owners WHERE owner_id = $1)`, ownerID).Scan(&claimed); err != nil {
		return false, fmt.Errorf("store: 查询 owner 认领状态: %w", err)
	}
	return claimed, nil
}

func (s *Store) UpdateProductUserDisplayName(ctx context.Context, userID, displayName string) (ProductUser, error) {
	u, err := scanProductUser(s.pool.QueryRow(ctx, `
		UPDATE product_users SET display_name = $2, updated_at = now() WHERE id = $1
		RETURNING id::text, auth_subject::text, email, display_name, created_at, updated_at`, userID, displayName))
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductUser{}, ErrWebSessionNotFound
	}
	if err != nil {
		return ProductUser{}, fmt.Errorf("store: 更新显示名称: %w", err)
	}
	return u, nil
}
