// Package upstream 提供模型与 embedding 上游错误的统一、安全分类。
// 错误正文可能包含供应商返回的请求片段，面向日志和产品层只暴露 Kind/HTTP 状态/机器码。
package upstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// Kind 是可安全用于分支判断和脱敏指标的上游错误类别。
type Kind string

const (
	KindQuota          Kind = "quota"
	KindAuthentication Kind = "authentication"
	KindRateLimit      Kind = "rate_limit"
	KindTimeout        Kind = "timeout"
	KindUnavailable    Kind = "unavailable"
	KindInvalidRequest Kind = "invalid_request"
	KindProtocol       Kind = "protocol"
	KindUnknown        Kind = "unknown"
)

// Error 保留原始 cause 供 errors.Is/As 使用，但 Error() 不拼接供应商正文。
type Error struct {
	Provider   string
	Role       string
	Kind       Kind
	HTTPStatus int
	Code       string
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "model upstream error"
	}
	return fmt.Sprintf("model upstream: provider=%s role=%s kind=%s status=%d code=%s",
		e.Provider, e.Role, e.Kind, e.HTTPStatus, SafeCode(e.Code))
}

func (e *Error) Unwrap() error { return e.cause }

// New 构造已分类错误；cause 只用于程序判断，不进入 Error() 文本。
func New(provider, role string, kind Kind, status int, code string, cause error) *Error {
	return &Error{Provider: provider, Role: role, Kind: kind, HTTPStatus: status, Code: SafeCode(code), cause: cause}
}

// Classify 根据 HTTP 状态、供应商机器码和网络错误得出稳定类别。
func Classify(status int, code string, err error) Kind {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return KindProtocol
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTimeout
	}
	normalized := strings.ToLower(strings.TrimSpace(code))
	if containsAny(normalized, "insufficient_quota", "quota_exhausted", "free_tier_only", "allocationquota") {
		return KindQuota
	}
	switch status {
	case 401:
		return KindAuthentication
	case 429:
		return KindRateLimit
	case 400, 404, 409, 422:
		return KindInvalidRequest
	case 408, 504:
		return KindTimeout
	case 500, 502, 503:
		return KindUnavailable
	}
	if status == 403 {
		if containsAny(normalized, "quota", "allocation") {
			return KindQuota
		}
		return KindAuthentication
	}
	if err != nil {
		return KindUnavailable
	}
	return KindUnknown
}

// SafeCode 只允许短机器码进入日志；其他内容统一脱敏。
func SafeCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if len(code) > 96 {
		return "redacted"
	}
	for _, r := range code {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return "redacted"
	}
	return code
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
