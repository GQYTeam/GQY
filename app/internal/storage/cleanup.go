package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// sessionPolicy 是清理时从 manifest 读取的策略字段。
type sessionPolicy struct {
	SessionID string `json:"session_id"`
	CreatedAt string `json:"created_at"`
	Cleanup   struct {
		SessionTmpTTLDays int `json:"session_tmp_ttl_days"`
		LogsTTLDays       int `json:"logs_ttl_days"`
	} `json:"cleanup_policy"`
}

// CleanExpired 删除超过 logs TTL 的会话目录；返回删除数量（ST7、S6）。
func CleanExpired(home string, now time.Time) (int, error) {
	sessionsDir := filepath.Join(home, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(sessionsDir, entry.Name())
		manifestPath := filepath.Join(dir, "manifest.json")
		data, err := os.ReadFile(manifestPath) // #nosec G304 -- 路径来自会话目录
		if err != nil {
			continue
		}
		var policy sessionPolicy
		if err := json.Unmarshal(data, &policy); err != nil {
			continue
		}
		ttlDays := policy.Cleanup.LogsTTLDays
		if ttlDays <= 0 {
			ttlDays = 30
		}
		created, err := time.Parse(time.RFC3339, policy.CreatedAt)
		if err != nil {
			continue
		}
		if now.Sub(created) > time.Duration(ttlDays)*24*time.Hour {
			if err := EnsureInside(sessionsDir, dir); err != nil {
				continue
			}
			if err := os.RemoveAll(dir); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// CleanOneshotCache 删除超过 TTL 的一次性插件缓存目录（15 NX6、ST7）。
// 只清理 $GQY_HOME/tmp/oneshot/.cache/ 下的条目，按目录修改时间判定过期。
func CleanOneshotCache(home string, now time.Time, ttl time.Duration) (int, error) {
	cacheDir := filepath.Join(home, "tmp", "oneshot", ".cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(cacheDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) <= ttl {
			continue
		}
		if err := EnsureInside(cacheDir, dir); err != nil {
			continue
		}
		if err := os.RemoveAll(dir); err == nil {
			removed++
		}
	}
	return removed, nil
}
