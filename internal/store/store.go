// Package store keeps run history in an append-only JSON Lines file.
package store

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/celaldrg/flakehound/internal/model"
)

// DefaultPath is where history lives relative to the working directory.
const DefaultPath = ".flakehound/history.jsonl"

// MakeID derives a stable id so ingesting the same report twice is a no-op.
func MakeID(label, source string, content []byte) string {
	h := sha1.New()
	h.Write([]byte(label))
	h.Write([]byte{0})
	h.Write([]byte(source))
	h.Write([]byte{0})
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Load reads every run from path. A missing file is an empty history.
// Corrupt lines are skipped and counted so one bad write never loses the rest.
func Load(path string) (runs []model.Run, skipped int, err error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r model.Run
		if json.Unmarshal(line, &r) != nil || r.ID == "" {
			skipped++
			continue
		}
		runs = append(runs, r)
	}
	if err := sc.Err(); err != nil {
		return runs, skipped, err
	}
	model.SortRuns(runs)
	return runs, skipped, nil
}

// Append adds runs, skipping ids already present. It returns how many were added.
func Append(path string, runs ...model.Run) (int, error) {
	existing, _, err := Load(path)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, r := range existing {
		seen[r.ID] = true
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n := 0
	var werr error
	for _, r := range runs {
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		b, err := json.Marshal(r)
		if err != nil {
			werr = err
			break
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			werr = err
			break
		}
		n++
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return n, fmt.Errorf("writing %s: %w", path, werr)
	}
	return n, nil
}
