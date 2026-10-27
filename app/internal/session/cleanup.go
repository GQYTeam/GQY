package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CleanExpired 删除 updated_at 早于 now-ttl 的会话目录。dryRun 只返回将删除的 id。
func (s *Store) CleanExpired(now time.Time, ttl time.Duration, dryRun bool) ([]string, error) {
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cutoff := now.Add(-ttl)
	var removed []string
	for _, e := range entries {
		if !e.IsDir() || !IDRe.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(s.Dir(), e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			continue
		}
		var m struct {
			UpdatedAt string `json:"updated_at"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		updated, err := time.Parse(time.RFC3339, m.UpdatedAt)
		if err != nil {
			continue
		}
		if !updated.Before(cutoff) {
			continue
		}
		if !dryRun {
			if err := os.RemoveAll(dir); err != nil {
				return removed, err
			}
		}
		removed = append(removed, e.Name())
	}
	sort.Strings(removed)
	return removed, nil
}
