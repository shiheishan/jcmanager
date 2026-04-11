package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcfg "jcmanager/internal/agent"
	jcmanagerpb "jcmanager/proto"
)

func TestApplyFileWriteCommandCreatesBackupAndRestartsService(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "config.yml")
	if err := os.WriteFile(targetPath, []byte("old-config"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	logPath := filepath.Join(tempDir, "systemctl.log")
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "systemctl")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + shellQuote(logPath) + "\n" +
		"echo restarted\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake systemctl: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	cfg := &agentcfg.RuntimeConfig{
		AllowedPaths: []string{tempDir},
	}

	response, changed, stdout, stderr, message, err := applyFileWriteCommand(context.Background(), cfg, &jcmanagerpb.FileWriteCommand{
		Path:              targetPath,
		Content:           []byte("new-config"),
		CreateBackup:      true,
		RestartAfterWrite: true,
		ServiceName:       "xrayr",
	})
	if err != nil {
		t.Fatalf("apply file write: %v", err)
	}
	if !changed || !response.GetRestarted() {
		t.Fatalf("expected changed file and restart response, got changed=%v resp=%#v", changed, response)
	}
	if stdout != "restarted" || stderr != "" {
		t.Fatalf("unexpected command output stdout=%q stderr=%q", stdout, stderr)
	}
	if message != "file updated and service restarted" {
		t.Fatalf("unexpected message %q", message)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	if string(content) != "new-config" {
		t.Fatalf("unexpected updated content %q", string(content))
	}

	if !strings.Contains(response.GetBackupPath(), string(filepath.Separator)+".backup"+string(filepath.Separator)) {
		t.Fatalf("expected backup path in .backup dir, got %q", response.GetBackupPath())
	}
	backupContent, err := os.ReadFile(response.GetBackupPath())
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if string(backupContent) != "old-config" {
		t.Fatalf("unexpected backup content %q", string(backupContent))
	}

	logContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read systemctl log: %v", err)
	}
	if !strings.Contains(string(logContent), "restart xrayr") {
		t.Fatalf("unexpected systemctl invocation %q", string(logContent))
	}
}

func TestApplyFileWriteCommandRejectsDisallowedPath(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &agentcfg.RuntimeConfig{
		AllowedPaths: []string{filepath.Join(tempDir, "allowed")},
	}

	_, _, _, _, _, err := applyFileWriteCommand(context.Background(), cfg, &jcmanagerpb.FileWriteCommand{
		Path:              filepath.Join(tempDir, "blocked", "config.yml"),
		Content:           []byte("blocked"),
		CreateBackup:      true,
		RestartAfterWrite: false,
	})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected not allowed error, got %v", err)
	}
}

func TestApplyFileWriteCommandRollsBackOnRestartFailure(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "config.yml")
	if err := os.WriteFile(targetPath, []byte("stable-config"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	logPath := filepath.Join(tempDir, "systemctl.log")
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "systemctl")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + shellQuote(logPath) + "\n" +
		"echo invalid config >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake systemctl: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	cfg := &agentcfg.RuntimeConfig{
		AllowedPaths: []string{tempDir},
	}

	response, changed, stdout, stderr, message, err := applyFileWriteCommand(context.Background(), cfg, &jcmanagerpb.FileWriteCommand{
		Path:              targetPath,
		Content:           []byte("broken-config"),
		CreateBackup:      true,
		RestartAfterWrite: true,
		ServiceName:       "xrayr",
	})
	if err == nil {
		t.Fatalf("expected restart failure")
	}
	if changed {
		t.Fatalf("expected changed=false after rollback, got true")
	}
	if response.GetRestarted() {
		t.Fatalf("expected restarted=false on failure")
	}
	if stdout != "" {
		t.Fatalf("unexpected stdout %q", stdout)
	}
	if stderr != "invalid config" {
		t.Fatalf("unexpected stderr %q", stderr)
	}
	if !strings.Contains(message, "configuration rolled back") {
		t.Fatalf("expected rollback message, got %q", message)
	}

	content, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatalf("read rolled back config: %v", readErr)
	}
	if string(content) != "stable-config" {
		t.Fatalf("expected original content after rollback, got %q", string(content))
	}
}

func TestApplyFileReadCommandReturnsContent(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "config.yml")
	if err := os.WriteFile(targetPath, []byte("log:\n  level: info\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := &agentcfg.RuntimeConfig{
		AllowedPaths: []string{tempDir},
	}

	response, message, err := applyFileReadCommand(cfg, &jcmanagerpb.FileReadCommand{
		Path: targetPath,
	})
	if err != nil {
		t.Fatalf("apply file read: %v", err)
	}
	if message != "file read" {
		t.Fatalf("unexpected message %q", message)
	}
	if response.GetPath() != targetPath {
		t.Fatalf("unexpected path %#v", response)
	}
	if string(response.GetContent()) != "log:\n  level: info\n" {
		t.Fatalf("unexpected content %q", string(response.GetContent()))
	}
	if response.GetSizeBytes() == 0 || response.GetModTimeUnix() == 0 {
		t.Fatalf("expected size and mod time, got %#v", response)
	}
}

func TestApplyFileReadCommandRejectsDisallowedPath(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &agentcfg.RuntimeConfig{
		AllowedPaths: []string{filepath.Join(tempDir, "allowed")},
	}

	_, _, err := applyFileReadCommand(cfg, &jcmanagerpb.FileReadCommand{
		Path: filepath.Join(tempDir, "blocked", "config.yml"),
	})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected not allowed error, got %v", err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
