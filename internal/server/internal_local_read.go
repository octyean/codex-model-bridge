package server

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	localReadMaxBytes = 20000
	localReadMaxLines = 240
)

func resolveWorkspacePath(path string, workspace string) string {
	path = strings.TrimSpace(strings.TrimPrefix(path, "file://"))
	if path == "" || filepath.IsAbs(path) || strings.TrimSpace(workspace) == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workspace, path))
}

func localFileReadOutput(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "LOCAL_FILE_READ_FAILED\npath: " + path + "\nerror: " + err.Error()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "LOCAL_FILE_READ_FAILED\npath: " + path + "\nerror: " + err.Error()
	}
	if info.IsDir() {
		return "LOCAL_FILE_READ_FAILED\npath: " + path + "\nerror: target is a directory"
	}
	data, err := io.ReadAll(io.LimitReader(file, localReadMaxBytes+1))
	if err != nil {
		return "LOCAL_FILE_READ_FAILED\npath: " + path + "\nerror: " + err.Error()
	}
	truncated := len(data) > localReadMaxBytes
	if truncated {
		data = data[:localReadMaxBytes]
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > localReadMaxLines {
		lines = lines[:localReadMaxLines]
		truncated = true
	}
	out := []string{"LOCAL_FILE_READ_RESULT", "path: " + path, "content:"}
	out = append(out, lines...)
	if truncated {
		out = append(out, "truncated: true")
	}
	return strings.Join(out, "\n")
}
