// feedbackexport 导出明确反馈 ID 对应的本机待复核案例，不生成标准答案或修改题库。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func main() {
	id := flag.String("id", "", "明确的反馈 UUID")
	out := flag.String("out", "var/data/feedback-candidates", "本机待复核目录")
	flag.Parse()
	if _, err := uuid.Parse(*id); err != nil {
		fmt.Fprintln(os.Stderr, "必须提供有效的 -id")
		os.Exit(2)
	}
	dotenv.Load(".env")
	ctx := context.Background()
	s, err := store.New(ctx, os.Getenv("PG_DSN"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法连接反馈数据库")
		os.Exit(2)
	}
	defer s.Close()
	raw, err := s.FeedbackCandidate(ctx, *id)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法读取指定反馈")
		os.Exit(2)
	}
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		fmt.Fprintln(os.Stderr, "反馈证据格式错误")
		os.Exit(2)
	}
	pretty, _ := json.MarshalIndent(value, "", "  ")
	path, err := writeCandidate(*out, *id, pretty)
	if err != nil {
		fmt.Fprintln(os.Stderr, "候选文件写入失败；已有文件不会被覆盖")
		os.Exit(2)
	}
	fmt.Printf("待复核候选已保存：%s（未脱敏、无标准答案、未加入评估集）\n", path)
}

func writeCandidate(out, id string, pretty []byte) (string, error) {
	if err := os.MkdirAll(out, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(out, id+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = f.Write(append(pretty, '\n'))
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
}
