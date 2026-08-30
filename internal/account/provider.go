package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("认证凭据无效")
	ErrEmailExists        = errors.New("邮箱已注册")
	ErrWeakPassword       = errors.New("密码不符合要求")
	ErrSessionExpired     = errors.New("登录会话已失效")
	ErrUnavailable        = errors.New("认证服务不可用")
	ErrProtocol           = errors.New("认证服务响应无效")
)

type ProviderUser struct {
	ID           string         `json:"id"`
	Email        string         `json:"email"`
	UserMetadata map[string]any `json:"user_metadata"`
}

type Tokens struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int          `json:"expires_in"`
	User         ProviderUser `json:"user"`
}

type Provider interface {
	Health(context.Context) error
	Register(context.Context, string, string, string) (Tokens, error)
	PasswordGrant(context.Context, string, string) (Tokens, error)
	Refresh(context.Context, string) (Tokens, error)
	User(context.Context, string) (ProviderUser, error)
	UpdatePassword(context.Context, string, string) (ProviderUser, error)
	Logout(context.Context, string, string) error
}

type GoTrueClient struct {
	baseURL string
	client  *http.Client
}

func NewGoTrueClient(baseURL string, timeout time.Duration) (*GoTrueClient, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("auth: SUPABASE_AUTH_URL 无效")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &GoTrueClient{baseURL: u.String(), client: &http.Client{Timeout: timeout}}, nil
}

func (c *GoTrueClient) Health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	return nil
}

func (c *GoTrueClient) Register(ctx context.Context, email, password, displayName string) (Tokens, error) {
	body := map[string]any{"email": email, "password": password, "data": map[string]string{"display_name": displayName}}
	var out Tokens
	err := c.request(ctx, http.MethodPost, "/signup", "", body, &out)
	return out, err
}

func (c *GoTrueClient) PasswordGrant(ctx context.Context, email, password string) (Tokens, error) {
	var out Tokens
	err := c.request(ctx, http.MethodPost, "/token?grant_type=password", "", map[string]string{"email": email, "password": password}, &out)
	return out, err
}

func (c *GoTrueClient) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	var out Tokens
	err := c.request(ctx, http.MethodPost, "/token?grant_type=refresh_token", "", map[string]string{"refresh_token": refreshToken}, &out)
	return out, err
}

func (c *GoTrueClient) User(ctx context.Context, accessToken string) (ProviderUser, error) {
	var out ProviderUser
	err := c.request(ctx, http.MethodGet, "/user", accessToken, nil, &out)
	return out, err
}

func (c *GoTrueClient) UpdatePassword(ctx context.Context, accessToken, password string) (ProviderUser, error) {
	var out ProviderUser
	err := c.request(ctx, http.MethodPut, "/user", accessToken, map[string]string{"password": password}, &out)
	return out, err
}

func (c *GoTrueClient) Logout(ctx context.Context, accessToken, scope string) error {
	if scope != "local" && scope != "others" && scope != "global" {
		return fmt.Errorf("auth: logout scope 无效")
	}
	return c.request(ctx, http.MethodPost, "/logout?scope="+scope, accessToken, nil, nil)
}

func (c *GoTrueClient) request(ctx context.Context, method, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return ErrProtocol
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return ErrProtocol
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, 64<<10)
	data, err := io.ReadAll(limited)
	if err != nil {
		return ErrProtocol
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyProviderError(resp.StatusCode, data)
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return ErrProtocol
	}
	return nil
}

func classifyProviderError(status int, data []byte) error {
	var payload struct {
		Code      string `json:"code"`
		ErrorCode string `json:"error_code"`
		Message   string `json:"msg"`
	}
	_ = json.Unmarshal(data, &payload)
	code := strings.ToLower(payload.Code + " " + payload.ErrorCode + " " + payload.Message)
	switch {
	case status == http.StatusUnauthorized || strings.Contains(code, "invalid login") || strings.Contains(code, "invalid_credentials"):
		return ErrInvalidCredentials
	case strings.Contains(code, "already") || strings.Contains(code, "exists"):
		return ErrEmailExists
	case strings.Contains(code, "password") && (strings.Contains(code, "weak") || strings.Contains(code, "length")):
		return ErrWeakPassword
	case status == http.StatusTooManyRequests || status >= 500:
		return ErrUnavailable
	case status == http.StatusForbidden:
		return ErrSessionExpired
	default:
		return ErrProtocol
	}
}
