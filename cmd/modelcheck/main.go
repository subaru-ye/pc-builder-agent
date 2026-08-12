// modelcheck 显式验证某个模型角色的最小能力。它只在人工执行时调用上游，
// 服务启动和常规测试不会运行本命令，也不会输出请求正文、响应正文或凭据。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

func main() {
	roleFlag := flag.String("role", "", "要验证的角色: screening、builder 或 embedding")
	timeout := flag.Duration("timeout", 60*time.Second, "单次验证超时")
	flag.Parse()

	dotenv.Load(".env")
	role := modelprovider.Role(*roleFlag)
	if role != modelprovider.RoleScreening && role != modelprovider.RoleBuilder && role != modelprovider.RoleEmbedding {
		log.Fatal("-role 必须是 screening、builder 或 embedding")
	}
	cfg, err := modelprovider.Load(role)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	started := time.Now()
	if role == modelprovider.RoleEmbedding {
		err = checkEmbedding(ctx, cfg)
	} else {
		err = checkChat(ctx, cfg, role == modelprovider.RoleBuilder)
	}
	if err != nil {
		log.Fatalf("modelcheck failed provider=%s role=%s model=%s duration=%s: %v",
			cfg.Provider, cfg.Role, cfg.Model, time.Since(started).Round(time.Millisecond), err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "modelcheck ok provider=%s role=%s model=%s duration=%s\n",
		cfg.Provider, cfg.Role, cfg.Model, time.Since(started).Round(time.Millisecond)); err != nil {
		log.Fatal(err)
	}
}

func checkEmbedding(ctx context.Context, cfg modelprovider.Config) error {
	client, err := modelprovider.NewEmbedding(cfg)
	if err != nil {
		return err
	}
	vector, err := client.EmbedOne(ctx, "模型连通性检查")
	if err != nil {
		return err
	}
	if len(vector) != cfg.Dimensions {
		return fmt.Errorf("embedding dimension=%d, want %d", len(vector), cfg.Dimensions)
	}
	return nil
}

func checkChat(ctx context.Context, cfg modelprovider.Config, requireTool bool) error {
	chat, err := modelprovider.NewChat(ctx, cfg, "modelcheck")
	if err != nil {
		return err
	}
	req := &model.LLMRequest{
		Model:    cfg.Model,
		Contents: []*genai.Content{genai.NewContentFromText("只回复 OK。", genai.RoleUser)},
	}
	if requireTool {
		req.Contents = []*genai.Content{genai.NewContentFromText("必须调用 health_check 工具，不要输出普通文本。", genai.RoleUser)}
		req.Config = &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:                 "health_check",
				Description:          "用于验证模型 function calling 能力",
				ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			}}}},
			ToolConfig: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAny,
			}},
		}
	}

	gotResponse, gotTool := false, false
	for resp, callErr := range chat.GenerateContent(ctx, req, false) {
		if callErr != nil {
			return callErr
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		gotResponse = true
		for _, part := range resp.Content.Parts {
			if part.FunctionCall != nil && part.FunctionCall.Name == "health_check" {
				gotTool = true
			}
		}
	}
	if !gotResponse {
		return fmt.Errorf("provider returned no content")
	}
	if requireTool && !gotTool {
		return fmt.Errorf("provider returned no health_check function call")
	}
	return nil
}
