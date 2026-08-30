package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

var (
	ErrDisabled            = errors.New("账号功能未启用")
	ErrOwnerClaimed        = errors.New("匿名身份已被认领")
	ErrIdempotencyConflict = errors.New("认证幂等键冲突")
)

type AccountStore interface {
	UpsertProductUserAndClaim(context.Context, string, string, string, string) (store.ProductUser, int, error)
	ProductUser(context.Context, string) (store.ProductUser, error)
	ProductUserOwners(context.Context, string) ([]string, string, error)
	OwnerClaimed(context.Context, string) (bool, error)
	UpdateProductUserDisplayName(context.Context, string, string) (store.ProductUser, error)
}

type Config struct {
	Enabled bool
}

type Principal struct {
	Authenticated bool
	User          *store.ProductUser
	Owners        []string
	PrimaryOwner  string
}

type AuthResult struct {
	Principal           Principal
	SessionToken        string
	ClaimedSessionCount int
}

type Service struct {
	cfg      Config
	provider Provider
	vault    *SessionVault
	store    AccountStore
	now      func() time.Time
}

func NewService(cfg Config, provider Provider, vault *SessionVault, st AccountStore) (*Service, error) {
	if st == nil {
		return nil, fmt.Errorf("auth: account store 不能为空")
	}
	if cfg.Enabled && (provider == nil || vault == nil) {
		return nil, fmt.Errorf("auth: 启用时 provider/vault 不能为空")
	}
	return &Service{cfg: cfg, provider: provider, vault: vault, store: st, now: time.Now}, nil
}

func (s *Service) Enabled() bool { return s.cfg.Enabled }

func (s *Service) Health(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}
	return s.provider.Health(ctx)
}

func (s *Service) Resolve(ctx context.Context, sessionToken, anonymousOwner string) (Principal, error) {
	if sessionToken == "" {
		claimed, err := s.store.OwnerClaimed(ctx, anonymousOwner)
		if err != nil {
			return Principal{}, err
		}
		if claimed {
			return Principal{}, ErrOwnerClaimed
		}
		return Principal{PrimaryOwner: anonymousOwner, Owners: []string{anonymousOwner}}, nil
	}
	if !s.cfg.Enabled {
		return Principal{}, ErrDisabled
	}
	record, err := s.vault.Get(ctx, sessionToken)
	if err != nil {
		return Principal{}, err
	}
	if record.ExpiresAt.Before(s.now().Add(2 * time.Minute)) {
		record, err = s.vault.RefreshLocked(ctx, sessionToken, record, func(refreshCtx context.Context) (SessionRecord, error) {
			tokens, err := s.provider.Refresh(refreshCtx, record.RefreshToken)
			if err != nil {
				return SessionRecord{}, err
			}
			return recordFromTokens(record.UserID, tokens, s.now()), nil
		})
		if err != nil {
			return Principal{}, err
		}
	}
	u, err := s.store.ProductUser(ctx, record.UserID)
	if err != nil {
		return Principal{}, ErrSessionExpired
	}
	owners, primary, err := s.store.ProductUserOwners(ctx, u.ID)
	if err != nil {
		return Principal{}, err
	}
	if primary == "" || len(owners) == 0 {
		return Principal{}, ErrSessionExpired
	}
	return Principal{Authenticated: true, User: &u, Owners: owners, PrimaryOwner: primary}, nil
}

func (s *Service) Register(ctx context.Context, anonymousOwner, email, password, displayName, key string) (AuthResult, error) {
	if !s.cfg.Enabled {
		return AuthResult{}, ErrDisabled
	}
	if err := validateIdentityInput(email, password, displayName); err != nil {
		return AuthResult{}, err
	}
	payload, _ := json.Marshal([]string{anonymousOwner, strings.ToLower(strings.TrimSpace(email)), password, strings.TrimSpace(displayName)})
	encoded, err := s.vault.DoIdempotent(ctx, "register", key, payload, func() ([]byte, error) {
		tokens, err := s.provider.Register(ctx, strings.ToLower(strings.TrimSpace(email)), password, strings.TrimSpace(displayName))
		if err != nil {
			return nil, err
		}
		result, err := s.finishLogin(ctx, anonymousOwner, strings.TrimSpace(displayName), tokens)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
	if err != nil {
		return AuthResult{}, err
	}
	var result AuthResult
	if json.Unmarshal(encoded, &result) != nil {
		return AuthResult{}, ErrProtocol
	}
	return result, nil
}

func (s *Service) Login(ctx context.Context, anonymousOwner, email, password, key string) (AuthResult, error) {
	if !s.cfg.Enabled {
		return AuthResult{}, ErrDisabled
	}
	if strings.TrimSpace(email) == "" || password == "" {
		return AuthResult{}, ErrInvalidCredentials
	}
	payload, _ := json.Marshal([]string{anonymousOwner, strings.ToLower(strings.TrimSpace(email)), password})
	encoded, err := s.vault.DoIdempotent(ctx, "login", key, payload, func() ([]byte, error) {
		tokens, err := s.provider.PasswordGrant(ctx, strings.ToLower(strings.TrimSpace(email)), password)
		if err != nil {
			return nil, err
		}
		result, err := s.finishLogin(ctx, anonymousOwner, "", tokens)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
	if err != nil {
		return AuthResult{}, err
	}
	var result AuthResult
	if json.Unmarshal(encoded, &result) != nil {
		return AuthResult{}, ErrProtocol
	}
	return result, nil
}

func (s *Service) finishLogin(ctx context.Context, anonymousOwner, displayName string, tokens Tokens) (AuthResult, error) {
	if tokens.User.ID == "" || tokens.User.Email == "" || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return AuthResult{}, ErrProtocol
	}
	if displayName == "" {
		if value, ok := tokens.User.UserMetadata["display_name"].(string); ok {
			displayName = strings.TrimSpace(value)
		}
	}
	u, claimed, err := s.store.UpsertProductUserAndClaim(ctx, tokens.User.ID, tokens.User.Email, displayName, anonymousOwner)
	if err != nil {
		return AuthResult{}, err
	}
	sessionToken, err := NewOpaqueToken()
	if err != nil {
		return AuthResult{}, err
	}
	if err := s.vault.Put(ctx, sessionToken, recordFromTokens(u.ID, tokens, s.now())); err != nil {
		return AuthResult{}, err
	}
	owners, primary, err := s.store.ProductUserOwners(ctx, u.ID)
	if err != nil {
		_ = s.vault.Delete(ctx, sessionToken)
		return AuthResult{}, err
	}
	return AuthResult{Principal: Principal{Authenticated: true, User: &u, Owners: owners, PrimaryOwner: primary}, SessionToken: sessionToken, ClaimedSessionCount: claimed}, nil
}

func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	if !s.cfg.Enabled {
		return ErrDisabled
	}
	record, err := s.vault.Get(ctx, sessionToken)
	if err != nil && !errors.Is(err, ErrSessionExpired) {
		return err
	}
	if err == nil {
		_ = s.provider.Logout(ctx, record.AccessToken, "local")
	}
	return s.vault.Delete(ctx, sessionToken)
}

func (s *Service) UpdateProfile(ctx context.Context, p Principal, displayName, key string) (store.ProductUser, error) {
	displayName = strings.TrimSpace(displayName)
	if !p.Authenticated || p.User == nil {
		return store.ProductUser{}, ErrSessionExpired
	}
	if utf8.RuneCountInString(displayName) < 1 || utf8.RuneCountInString(displayName) > 40 || hasControl(displayName) {
		return store.ProductUser{}, fmt.Errorf("display_name 无效")
	}
	payload, _ := json.Marshal([]string{p.User.ID, displayName})
	encoded, err := s.vault.DoIdempotent(ctx, "profile", key, payload, func() ([]byte, error) {
		u, err := s.store.UpdateProductUserDisplayName(ctx, p.User.ID, displayName)
		if err != nil {
			return nil, err
		}
		return json.Marshal(u)
	})
	if err != nil {
		return store.ProductUser{}, err
	}
	var u store.ProductUser
	if json.Unmarshal(encoded, &u) != nil {
		return store.ProductUser{}, ErrProtocol
	}
	return u, nil
}

func (s *Service) ChangePassword(ctx context.Context, p Principal, sessionToken, currentPassword, newPassword, key string) error {
	if !p.Authenticated || p.User == nil {
		return ErrSessionExpired
	}
	if len(newPassword) < 10 || len(newPassword) > 128 {
		return ErrWeakPassword
	}
	payload, _ := json.Marshal([]string{p.User.ID, currentPassword, newPassword})
	_, err := s.vault.DoIdempotent(ctx, "password", key, payload, func() ([]byte, error) {
		fresh, err := s.provider.PasswordGrant(ctx, p.User.Email, currentPassword)
		if err != nil {
			return nil, err
		}
		if _, err := s.provider.UpdatePassword(ctx, fresh.AccessToken, newPassword); err != nil {
			_ = s.provider.Logout(ctx, fresh.AccessToken, "local")
			return nil, err
		}
		if err := s.provider.Logout(ctx, fresh.AccessToken, "others"); err != nil {
			return nil, err
		}
		if err := s.vault.Put(ctx, sessionToken, recordFromTokens(p.User.ID, fresh, s.now())); err != nil {
			return nil, err
		}
		return []byte(`{"ok":true}`), nil
	})
	return err
}

func recordFromTokens(userID string, tokens Tokens, now time.Time) SessionRecord {
	expires := tokens.ExpiresIn
	if expires <= 0 {
		expires = 3600
	}
	return SessionRecord{UserID: userID, AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, ExpiresAt: now.Add(time.Duration(expires) * time.Second)}
}

func validateIdentityInput(email, password, displayName string) error {
	email = strings.TrimSpace(email)
	displayName = strings.TrimSpace(displayName)
	if len(email) > 254 || !strings.Contains(email, "@") {
		return ErrInvalidCredentials
	}
	if len(password) < 10 || len(password) > 128 {
		return ErrWeakPassword
	}
	if utf8.RuneCountInString(displayName) < 1 || utf8.RuneCountInString(displayName) > 40 || hasControl(displayName) {
		return fmt.Errorf("display_name 无效")
	}
	return nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
