package evaldesk

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxArtifactBytes = 256 << 20

var errNotFound = errors.New("运行不存在或产物已移除")

// Store never opens environment files, databases, model clients or raw log files.
type Store struct {
	root       string
	mu         sync.Mutex
	cacheReqV2 map[string]*savedReqV2Run
}

func NewStore(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	return &Store{root: absolute, cacheReqV2: map[string]*savedReqV2Run{}}, nil
}

func below(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// Even artifact symlinks inside an otherwise safe directory are rejected.
func (s *Store) safeFile(dir, name string) (string, error) {
	if !below(s.root, dir) || filepath.Base(name) != name {
		return "", errNotFound
	}
	relative, err := filepath.Rel(s.root, filepath.Join(dir, name))
	if err != nil {
		return "", errNotFound
	}
	current := s.root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("不读取符号链接产物")
		}
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || !below(s.root, resolved) {
		return "", errors.New("不读取越出项目目录的链接产物")
	}
	st, err := os.Stat(current)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > maxArtifactBytes {
		return "", errors.New("产物类型或大小不受支持")
	}
	return current, nil
}

func (s *Store) read(dir, name string) ([]byte, error) {
	path, err := s.safeFile(dir, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
