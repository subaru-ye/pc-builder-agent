// Package product 实现产品会话状态机、后台 run 与 Agent 适配。
package product

import (
	"encoding/json"
	"fmt"
)

// Problem 是 application/problem+json 与 run.failed 共用的安全错误投影。
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Instance  string `json:"instance,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
}

func NewProblem(code, title string, status int, detail, requestID string) Problem {
	return Problem{
		Type:      "/problems/" + code,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: requestID,
	}
}

func (p Problem) Error() string {
	if p.Detail == "" {
		return fmt.Sprintf("%s(%s)", p.Title, p.Code)
	}
	return fmt.Sprintf("%s(%s):%s", p.Title, p.Code, p.Detail)
}

func (p Problem) JSON() json.RawMessage {
	b, _ := json.Marshal(p)
	return b
}
