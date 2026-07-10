package diagnostics

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type PruneResult struct {
	SessionsPath  string
	Deleted       int
	ReleasedBytes int64
	Remaining     int
	RemainingSize int64
}

type sessionDir struct {
	path       string
	latestTime time.Time
	size       int64
}

func PruneSessions(basePath string, retentionDays int, maxTotalBytes int64) (PruneResult, error) {
	result := PruneResult{}
	if basePath == "" {
		return result, nil
	}
	result.SessionsPath = filepath.Join(filepath.Dir(basePath), "sessions")
	entries, err := os.ReadDir(result.SessionsPath)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	sessions := make([]sessionDir, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		session, err := inspectSessionDir(filepath.Join(result.SessionsPath, entry.Name()))
		if err != nil {
			return result, err
		}
		if retentionDays > 0 && session.latestTime.Before(cutoff) {
			if err := os.RemoveAll(session.path); err != nil {
				return result, err
			}
			result.Deleted++
			result.ReleasedBytes += session.size
			continue
		}
		sessions = append(sessions, session)
		result.RemainingSize += session.size
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].latestTime.Before(sessions[j].latestTime)
	})
	for maxTotalBytes > 0 && result.RemainingSize > maxTotalBytes && len(sessions) > 0 {
		oldest := sessions[0]
		sessions = sessions[1:]
		if err := os.RemoveAll(oldest.path); err != nil {
			return result, err
		}
		result.Deleted++
		result.ReleasedBytes += oldest.size
		result.RemainingSize -= oldest.size
	}
	result.Remaining = len(sessions)
	return result, nil
}

func inspectSessionDir(path string) (sessionDir, error) {
	info, err := os.Stat(path)
	if err != nil {
		return sessionDir{}, err
	}
	session := sessionDir{path: path, latestTime: info.ModTime()}
	err = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		session.size += info.Size()
		if info.ModTime().After(session.latestTime) {
			session.latestTime = info.ModTime()
		}
		return nil
	})
	return session, err
}
