package evalsuite

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SuiteManifest freezes the ordered case list. Hashes cover canonical JSON:
// whitespace/key ordering are ignored, but values, arrays and expectations are not.
type SuiteManifest struct {
	SchemaVersion int         `json:"schema_version"`
	Version       string      `json:"version"`
	CreatedAt     string      `json:"created_at"`
	Cases         []SuiteCase `json:"cases"`
}

type SuiteCase struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// SuiteSnapshot is the self-contained exam actually used by a run.
type SuiteSnapshot struct {
	Manifest SuiteManifest     `json:"manifest"`
	Cases    []json.RawMessage `json:"cases"`
}

func strictJSON(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

func JSONHash(data []byte) (string, error) {
	var value any
	if err := strictJSON(data, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(canonical)), nil
}

func (s SuiteSnapshot) Hash() (string, error) {
	data, err := json.Marshal(s.Manifest)
	if err != nil {
		return "", err
	}
	return JSONHash(data)
}

// DecodeCases checks identity and content before handing any inputs to the model.
func (s SuiteSnapshot) DecodeCases() ([]Case, error) {
	if s.Manifest.SchemaVersion != 1 || s.Manifest.Version == "" || s.Manifest.CreatedAt == "" || len(s.Cases) == 0 || len(s.Cases) != len(s.Manifest.Cases) {
		return nil, fmt.Errorf("evalsuite: invalid or incomplete suite snapshot")
	}
	ids, files := map[string]bool{}, map[string]bool{}
	var cases []Case
	for i, entry := range s.Manifest.Cases {
		if entry.ID == "" || entry.File != entry.ID+".json" || filepath.Base(entry.File) != entry.File || ids[entry.ID] || files[entry.File] {
			return nil, fmt.Errorf("evalsuite: invalid or duplicate case entry %q", entry.ID)
		}
		ids[entry.ID], files[entry.File] = true, true
		hash, err := JSONHash(s.Cases[i])
		if err != nil || hash != entry.SHA256 {
			return nil, fmt.Errorf("evalsuite: case %s content hash mismatch", entry.ID)
		}
		c, err := decodeCase(s.Cases[i])
		if err != nil {
			return nil, fmt.Errorf("evalsuite: case %s: %w", entry.ID, err)
		}
		if c.ID != entry.ID {
			return nil, fmt.Errorf("evalsuite: case ID mismatch: %s", entry.ID)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// LoadSuite reads each selected fixture once; execution uses these captured bytes.
// Extra cases in the workspace do not silently become members of an old version.
func LoadSuite(manifestPath, casesDir string) (SuiteSnapshot, []Case, error) {
	var s SuiteSnapshot
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return s, nil, err
	}
	if err := strictJSON(data, &s.Manifest); err != nil {
		return s, nil, err
	}
	for _, entry := range s.Manifest.Cases {
		if entry.File != entry.ID+".json" || filepath.Base(entry.File) != entry.File || entry.ID == "" {
			return s, nil, fmt.Errorf("evalsuite: unsafe fixture filename %q", entry.File)
		}
		raw, err := os.ReadFile(filepath.Join(casesDir, entry.File))
		if err != nil {
			return s, nil, err
		}
		s.Cases = append(s.Cases, json.RawMessage(raw))
	}
	cases, err := s.DecodeCases()
	return s, cases, err
}

func (s SuiteSnapshot) Write(path string) error {
	if _, err := s.DecodeCases(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func ReadSuiteSnapshot(path, expectedHash string) (SuiteSnapshot, []Case, error) {
	var s SuiteSnapshot
	data, err := os.ReadFile(path)
	if err != nil {
		return s, nil, err
	}
	if err := strictJSON(data, &s); err != nil {
		return s, nil, err
	}
	hash, err := s.Hash()
	if err != nil || hash != expectedHash {
		return s, nil, fmt.Errorf("evalsuite: suite hash mismatch")
	}
	cases, err := s.DecodeCases()
	return s, cases, err
}

// FreezeSuite captures all current fixtures. Existing versions are never overwritten.
func FreezeSuite(casesDir, path, version, date string) error {
	if version == "" {
		return fmt.Errorf("suite version is required")
	}
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		return err
	}
	s := SuiteSnapshot{Manifest: SuiteManifest{SchemaVersion: 1, Version: version, CreatedAt: date}}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(casesDir, entry.Name()))
		if err != nil {
			return err
		}
		c, err := decodeCase(data)
		if err != nil {
			return err
		}
		hash, err := JSONHash(data)
		if err != nil {
			return err
		}
		s.Manifest.Cases = append(s.Manifest.Cases, SuiteCase{ID: c.ID, File: entry.Name(), SHA256: hash})
		s.Cases = append(s.Cases, data)
	}
	if _, err := s.DecodeCases(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.Manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	closeErr := f.Close()
	if err == nil {
		return closeErr
	}
	return err
}
