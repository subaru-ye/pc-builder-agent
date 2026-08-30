package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var required = []struct {
	key   string
	value func() (string, error)
}{
	{"AUTH_ENABLED", func() (string, error) { return "true", nil }},
	{"SUPABASE_AUTH_URL", func() (string, error) { return "http://127.0.0.1:9999", nil }},
	{"SUPABASE_JWT_SECRET", func() (string, error) { return randomSecret(48) }},
	{"AUTH_SESSION_SECRET", func() (string, error) { return randomSecret(32) }},
	{"AUTH_SESSION_TTL", func() (string, error) { return "720h", nil }},
}

func main() {
	path := ".env"
	if len(os.Args) == 2 {
		path = os.Args[1]
	} else if len(os.Args) > 2 {
		fatal(errors.New("usage: authsetup [env-file]"))
	}
	if err := updateEnv(path); err != nil {
		fatal(err)
	}
	fmt.Printf("认证配置已安全写入 %s（已有非空值保持不变）\n", path)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "authsetup:", err)
	os.Exit(1)
}

func randomSecret(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func updateEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read env: %w", err)
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	values := parseEnv(content)
	var appended []string
	for _, item := range required {
		if strings.TrimSpace(values[item.key]) != "" {
			continue
		}
		value, err := item.value()
		if err != nil {
			return err
		}
		appended = append(appended, item.key+"="+value)
	}
	if len(appended) == 0 {
		return nil
	}
	if content != "" && !strings.HasSuffix(content, "\n\n") {
		content += "\n"
	}
	content += "# 本地 Supabase Auth；由 cmd/authsetup 生成，请勿提交。\n"
	content += strings.Join(appended, "\n") + "\n"

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".authsetup-*")
	if err != nil {
		return fmt.Errorf("create temporary env: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary env: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary env: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary env: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary env: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace env: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func parseEnv(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}
