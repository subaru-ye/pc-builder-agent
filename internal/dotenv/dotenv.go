// Package dotenv 提供仓库根 .env 的最小加载器(Windows 无 source .env 等价物)。
// 已存在的环境变量优先;文件不存在时静默跳过。
package dotenv

import (
	"bufio"
	"os"
	"strings"
)

// Load 从 path 读取 KEY=VALUE 并注入环境;忽略空行与 # 注释行。
func Load(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

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
			_ = os.Setenv(k, v)
		}
	}
}
