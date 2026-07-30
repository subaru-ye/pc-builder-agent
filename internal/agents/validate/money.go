package validate

import (
	"fmt"
	"strconv"
	"strings"
)

// parseCents 把受控 NUMERIC(10,2) 文本价(如 "1299.00"、"1299"、"0.50")解析为「分」。
// 只接受非负、至多两位小数;超两位小数或非数字一律报错(不静默截断,遵失败要响原则)。
func parseCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("空价格")
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > 2 {
		return 0, fmt.Errorf("小数位超过 2 位: %q", s)
	}
	ip, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil || ip < 0 {
		return 0, fmt.Errorf("整数部分非法: %q", s)
	}
	var fp int64
	if fracPart != "" {
		padded := fracPart + strings.Repeat("0", 2-len(fracPart))
		fp, err = strconv.ParseInt(padded, 10, 64)
		if err != nil || fp < 0 {
			return 0, fmt.Errorf("小数部分非法: %q", s)
		}
	}
	return ip*100 + fp, nil
}

// formatCents 把「分」格式化回两位小数的元文本(如 129900 → "1299.00")。
func formatCents(c int64) string {
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}
