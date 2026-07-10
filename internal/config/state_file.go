package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateLockWait  = 5 * time.Second
	staleStateLock = 2 * time.Minute
)

func withStateFileLock(path string, action func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(stateLockWait)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = lock.Close()
			defer os.Remove(lockPath)
			return action()
		}
		if !os.IsExist(err) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > staleStateLock {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("state file is locked by another process: %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func writeStateJSONUnlocked(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func readStateJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
