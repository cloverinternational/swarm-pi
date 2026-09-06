package analytics

import (
	"os"
	"path/filepath"
	"strings"
)

type QueueStats struct {
	Dir   string `json:"dir"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

type DispatcherStats struct {
	Events    QueueStats `json:"events"`
	Artifacts QueueStats `json:"artifacts"`
}

func (s *Spool) Stats() (QueueStats, error) {
	if s == nil {
		return QueueStats{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return statJSONQueue(s.dir)
}

func (s *ArtifactSpool) Stats() (QueueStats, error) {
	if s == nil {
		return QueueStats{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return statJSONQueue(s.dir)
}

func statJSONQueue(dir string) (QueueStats, error) {
	stats := QueueStats{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return stats, nil
		}
		return stats, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return stats, err
		}
		stats.Files++
		stats.Bytes += info.Size()
	}
	if abs, err := filepath.Abs(stats.Dir); err == nil {
		stats.Dir = abs
	}
	return stats, nil
}
