// Package embedding 百炼 OpenAI 兼容端点 /embeddings 的最小客户端。
// 供 cmd/embedparts(全量生成)与 cmd/host(查询向量化)共用;
// 模型型号只写代码常量,不进文档(技术选型 ADR-004)。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultModel 端点 embedding 模型(2026-07-30 实测输出 1024 维)。
const DefaultModel = "qwen3.7-text-embedding"

// batchSize 单请求文本数上限(百炼 embedding 批量限制,保守取 10)。
const batchSize = 10

// Client OpenAI 兼容 /embeddings 客户端。
type Client struct {
	baseURL string
	apiKey  string
	model   string
	dims    int // 期望维度,响应不符即报错(不静默截断/填充)
	hc      *http.Client
}

// NewClient 构造客户端;dims 为期望向量维度(与 store.EmbeddingDims 对齐)。
func NewClient(baseURL, apiKey, model string, dims int) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		dims:    dims,
		hc:      &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed 批量向量化;返回顺序与入参一致。任一批失败即整体报错,不静默跳过。
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		vecs, err := c.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embedding: 批次 [%d:%d) 失败: %w", start, end, err)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// EmbedOne 单条向量化(查询场景)。
func (c *Client) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("编码请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求端点失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("端点返回 %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}

	var er embedResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, fmt.Errorf("解码响应失败: %w", err)
	}
	if len(er.Data) != len(texts) {
		return nil, fmt.Errorf("返回 %d 条向量,期望 %d", len(er.Data), len(texts))
	}

	// 按 index 归位(响应顺序不做假设),并校验维度。
	out := make([][]float32, len(texts))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("响应 index %d 越界(批大小 %d)", d.Index, len(texts))
		}
		if len(d.Embedding) != c.dims {
			return nil, fmt.Errorf("向量维度 %d,期望 %d(模型与库列不匹配须新迁移)", len(d.Embedding), c.dims)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("响应缺少 index %d 的向量", i)
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
