package product

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ManageSession 仅管理会话元数据或显式删除，不触发 Screening/Builder。
func (s *Service) ManageSession(ctx context.Context, owner, id string, title *string, archived *bool, remove bool) error {
	if !remove && title == nil && archived == nil {
		return NewProblem("invalid_request", "没有需要修改的内容", 422, "请提供标题或归档状态。", "")
	}
	if title != nil {
		clean := strings.TrimSpace(*title)
		if clean == "" || utf8.RuneCountInString(clean) > 80 || strings.ContainsFunc(clean, unicode.IsControl) {
			return NewProblem("invalid_request", "对话标题无效", 422, "标题需要 1–80 个字符，且不能包含换行或控制字符。", "")
		}
		title = &clean
	}
	manager, ok := s.store.(interface {
		ManageWebSession(context.Context, string, string, *string, *bool, bool) error
	})
	if !ok {
		return fmt.Errorf("会话管理不可用")
	}
	return manager.ManageWebSession(ctx, owner, id, title, archived, remove)
}
