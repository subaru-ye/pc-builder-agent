package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrWebSessionNotFound        = errors.New("产品会话不存在")
	ErrRunNotFound               = errors.New("运行不存在")
	ErrSessionBusy               = errors.New("会话已有活动运行")
	ErrInvalidSessionPhase       = errors.New("会话阶段不允许当前操作")
	ErrIdempotencyConflict       = errors.New("幂等键已用于不同请求")
	ErrRequirementRevision       = errors.New("需求已更新，请刷新后重试")
	ErrRequirementNotReady       = errors.New("需求尚未达到确认条件")
	ErrRequirementReviewConflict = errors.New("需求核定预览已变化，请重新核定")
	ErrRetryTargetInvalid        = errors.New("重试目标运行无效")
	ErrWebMessageNotFound        = errors.New("产品消息不存在")
)

var (
	staleCancelProblem  = json.RawMessage(`{"type":"/problems/run_cancelled","title":"已停止本次生成","status":409,"code":"run_cancelled","detail":"本次生成已取消，此前的对话与数据保持不变，可重新发起请求。"}`)
	staleExpiredProblem = json.RawMessage(`{"type":"/problems/run_interrupted","title":"运行已中断","status":409,"code":"run_interrupted","detail":"运行超过时限已回收，此前的对话与数据保持不变，请使用新请求重试。"}`)
)

func (s *Store) LatestBuildVersion(ctx context.Context, sessionID string) (int, bool, error) {
	var v *int
	if err := s.pool.QueryRow(ctx, `SELECT max(version) FROM builds WHERE session_id = $1`, sessionID).Scan(&v); err != nil {
		return 0, false, fmt.Errorf("store: 查询最新配置版本失败: %w", err)
	}
	if v == nil {
		return 0, false, nil
	}
	return *v, true, nil
}
