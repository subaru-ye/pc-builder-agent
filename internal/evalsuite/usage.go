package evalsuite

// Usage 记录适配器调用次数与上游实际返回的用量；未返回 usage 的调用不能当作零 token。
// ModelCalls/EmbeddingCalls 是逻辑调用，不包含适配器内部 HTTP 重试，不能直接换算费用。
type Usage struct {
	ModelCalls     int64 `json:"model_calls"`
	EmbeddingCalls int64 `json:"embedding_calls"`
	UsageResponses int64 `json:"usage_responses"`
	InputTokens    int64 `json:"input_tokens"`
	OutputTokens   int64 `json:"output_tokens"`
	TotalTokens    int64 `json:"total_tokens"`
}

func (u Usage) Since(before Usage) Usage {
	return Usage{ModelCalls: u.ModelCalls - before.ModelCalls, EmbeddingCalls: u.EmbeddingCalls - before.EmbeddingCalls, UsageResponses: u.UsageResponses - before.UsageResponses, InputTokens: u.InputTokens - before.InputTokens, OutputTokens: u.OutputTokens - before.OutputTokens, TotalTokens: u.TotalTokens - before.TotalTokens}
}
