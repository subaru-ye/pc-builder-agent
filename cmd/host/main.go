// P0 hello-world agent:验证 ADK-Go + 百炼 OpenAI 兼容端点 + 内置 launcher 全链路。
// 运行方式(从仓库根):
//
//	go run ./cmd/host                # console 模式,快速验证模型连通
//	go run ./cmd/host web api webui  # Web UI(三个子命令缺一不可),http://localhost:8080
package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/full"
	"google.golang.org/adk/v2/model/openaimodel"
)

// 低价档 chat 模型,型号以本常量为准,不写进文档(技术选型 ADR-004)。
const modelName = "qwen-flash"

// 百炼 OpenAI 兼容端点(公共默认);工作空间专属 Host 用 DASHSCOPE_BASE_URL 覆盖。
// ADK 的 openaimodel 走 Responses API,百炼已支持。
const defaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

func main() {
	ctx := context.Background()

	loadDotEnv(".env")

	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		log.Fatal("DASHSCOPE_API_KEY 未设置:复制 .env.example 为 .env 并填入百炼 key")
	}
	baseURL := os.Getenv("DASHSCOPE_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	m, err := openaimodel.NewModel(ctx, modelName, &openaimodel.ClientConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})
	if err != nil {
		log.Fatalf("创建模型失败: %v", err)
	}

	a, err := llmagent.New(llmagent.Config{
		Name:        "hello_agent",
		Model:       m,
		Description: "装机配置单 Agent 项目的 P0 hello 助手。",
		Instruction: "你是装机配置单 Agent 项目的 hello 助手,用简洁的中文回答。",
	})
	if err != nil {
		log.Fatalf("创建 agent 失败: %v", err)
	}

	l := full.NewLauncher()
	config := &launcher.Config{AgentLoader: agent.NewSingleLoader(a)}
	if err := l.Execute(ctx, config, os.Args[1:]); err != nil {
		log.Fatalf("运行失败: %v\n\n%s", err, l.CommandLineSyntax())
	}
}

// loadDotEnv 从仓库根的 .env 读取 KEY=VALUE 并注入环境(Windows 无 source .env 等价物)。
// 已存在的环境变量优先;文件不存在时静默跳过。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}
