package product

import (
	"context"
	"regexp"
	"strconv"

	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

var legacyVersion = regexp.MustCompile(`已落库[:：]版本 v([1-9][0-9]*)\(`)
var legacyBuildRef = regexp.MustCompile(`配置单\(build_ref: ([^\s)]+)\)`)

// 旧消息只依据保存的 run 证据，或可同时匹配的版本号与 build_ref 关联，绝不猜测最新版本。
func (s *Service) presentMessages(ctx context.Context, sessionID string, messages []store.WebMessage) {
	reader, ok := s.store.(presenter.Reader)
	if !ok {
		return
	}
	view := presenter.New(reader)
	cache := make(map[int]string)
	for i := range messages {
		m := &messages[i]
		if m.Role != "assistant" || m.DisplayContent != "" {
			continue
		}
		version := m.BuildVersion
		if version == 0 {
			v, ref := legacyVersion.FindStringSubmatch(m.Content), legacyBuildRef.FindStringSubmatch(m.Content)
			if len(v) != 2 || len(ref) != 2 {
				continue
			}
			version, _ = strconv.Atoi(v[1])
			build, err := reader.BuildByVersion(ctx, sessionID, version)
			if err != nil {
				continue
			}
			row, err := presenter.DecodeBuild(build, nil)
			if err != nil || row.Draft.BuildRef != ref[1] {
				continue
			}
		}
		if text, exists := cache[version]; exists {
			m.DisplayContent = text
			continue
		}
		text, err := view.ConversationSummary(ctx, sessionID, version)
		if err != nil {
			continue
		}
		m.DisplayContent = text
		cache[version] = text
	}
}

func (s *Service) buildMessage(ctx context.Context, sessionID string, version int) string {
	if reader, ok := s.store.(presenter.Reader); ok {
		if text, err := presenter.New(reader).ConversationSummary(ctx, sessionID, version); err == nil {
			return text
		}
	}
	return "方案已保存，但暂时无法整理摘要。请在配置详情中核对价格、配件和待确认事项。"
}
