package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentcfg "jcmanager/internal/agent"
	jcmanagerpb "jcmanager/proto"
)

const defaultManagedFileMode = 0o644

func executeCommand(ctx context.Context, cfg *agentcfg.RuntimeConfig, state agentState, command *jcmanagerpb.Command) *jcmanagerpb.CommandResult {
	result := &jcmanagerpb.CommandResult{
		NodeId:         state.NodeID,
		CommandId:      command.GetCommandId(),
		Type:           command.GetType(),
		ReportedAtUnix: time.Now().Unix(),
		Status:         jcmanagerpb.ResultStatus_RESULT_STATUS_SKIPPED,
		Message:        "unsupported command type",
	}

	if fileWrite := command.GetFileWrite(); fileWrite != nil {
		return executeFileWriteCommand(ctx, cfg, result, fileWrite)
	}
	if fileRead := command.GetFileRead(); fileRead != nil {
		return executeFileReadCommand(cfg, result, fileRead)
	}

	return result
}

func executeFileReadCommand(cfg *agentcfg.RuntimeConfig, result *jcmanagerpb.CommandResult, command *jcmanagerpb.FileReadCommand) *jcmanagerpb.CommandResult {
	response, message, err := applyFileReadCommand(cfg, command)
	result.ReportedAtUnix = time.Now().Unix()
	result.Message = message
	if response != nil {
		result.Payload = &jcmanagerpb.CommandResult_FileRead{
			FileRead: response,
		}
	}

	if err == nil {
		result.Status = jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS
		return result
	}

	result.Status = jcmanagerpb.ResultStatus_RESULT_STATUS_FAILED
	if result.Message == "" {
		result.Message = err.Error()
	}
	return result
}

func executeFileWriteCommand(ctx context.Context, cfg *agentcfg.RuntimeConfig, result *jcmanagerpb.CommandResult, command *jcmanagerpb.FileWriteCommand) *jcmanagerpb.CommandResult {
	resp, changed, stdout, stderr, message, err := applyFileWriteCommand(ctx, cfg, command)
	result.ReportedAtUnix = time.Now().Unix()
	result.Changed = changed
	result.Stdout = stdout
	result.Stderr = stderr
	result.Message = message
	if resp != nil {
		result.BackupPath = resp.GetBackupPath()
		result.Payload = &jcmanagerpb.CommandResult_FileWrite{
			FileWrite: resp,
		}
	}

	if err == nil {
		result.Status = jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS
		return result
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Status = jcmanagerpb.ResultStatus_RESULT_STATUS_TIMEOUT
		if result.Message == "" {
			result.Message = "file write command timed out"
		}
		return result
	}

	result.Status = jcmanagerpb.ResultStatus_RESULT_STATUS_FAILED
	if result.Message == "" {
		result.Message = err.Error()
	}
	return result
}

func applyFileWriteCommand(ctx context.Context, cfg *agentcfg.RuntimeConfig, command *jcmanagerpb.FileWriteCommand) (*jcmanagerpb.FileWriteResponse, bool, string, string, string, error) {
	targetPath := filepath.Clean(strings.TrimSpace(command.GetPath()))
	if targetPath == "" {
		return nil, false, "", "", "file write command missing path", fmt.Errorf("file write command missing path")
	}
	if !filepath.IsAbs(targetPath) {
		return nil, false, "", "", "path must be absolute", fmt.Errorf("path must be absolute")
	}

	allowedPaths := loadServiceSnapshot(cfg).allowedPath
	if err := validateManagedPath(targetPath, allowedPaths); err != nil {
		return nil, false, "", "", err.Error(), err
	}

	existingContent, fileMode, exists, err := readManagedFile(targetPath)
	if err != nil {
		return nil, false, "", "", err.Error(), err
	}
	if exists && bytes.Equal(existingContent, command.GetContent()) {
		return &jcmanagerpb.FileWriteResponse{
			Path: targetPath,
		}, false, "", "", "file already up to date", nil
	}

	response := &jcmanagerpb.FileWriteResponse{
		Path: targetPath,
	}
	if exists && command.GetCreateBackup() {
		backupPath, err := createManagedFileBackup(targetPath, existingContent)
		if err != nil {
			return response, false, "", "", err.Error(), err
		}
		response.BackupPath = backupPath
	}

	if err := writeManagedFile(targetPath, command.GetContent(), fileMode); err != nil {
		return response, false, "", "", err.Error(), err
	}

	stdout := ""
	stderr := ""
	if command.GetRestartAfterWrite() {
		serviceName := strings.TrimSpace(command.GetServiceName())
		if serviceName == "" {
			return response, true, "", "", "service_name is required when restart_after_write is true", fmt.Errorf("service_name is required when restart_after_write is true")
		}
		stdout, stderr, err = restartManagedService(ctx, serviceName)
		response.Restarted = err == nil
		if err != nil {
			restartMessage := firstNonEmptyString(strings.TrimSpace(stderr), strings.TrimSpace(stdout), err.Error())
			if rollbackErr := rollbackManagedFile(targetPath, existingContent, fileMode, exists); rollbackErr != nil {
				return response, true, stdout, stderr, fmt.Sprintf("%s; rollback failed: %v", restartMessage, rollbackErr), fmt.Errorf("restart service %q: %w; rollback failed: %v", serviceName, err, rollbackErr)
			}
			return response, false, stdout, stderr, fmt.Sprintf("%s; configuration rolled back", restartMessage), fmt.Errorf("restart service %q: %w", serviceName, err)
		}
		return response, true, stdout, stderr, "file updated and service restarted", nil
	}

	return response, true, stdout, stderr, "file updated", nil
}

func applyFileReadCommand(cfg *agentcfg.RuntimeConfig, command *jcmanagerpb.FileReadCommand) (*jcmanagerpb.FileReadResponse, string, error) {
	targetPath := filepath.Clean(strings.TrimSpace(command.GetPath()))
	if targetPath == "" {
		return nil, "file read command missing path", fmt.Errorf("file read command missing path")
	}
	if !filepath.IsAbs(targetPath) {
		return nil, "path must be absolute", fmt.Errorf("path must be absolute")
	}

	allowedPaths := loadServiceSnapshot(cfg).allowedPath
	if err := validateManagedPath(targetPath, allowedPaths); err != nil {
		return nil, err.Error(), err
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, err.Error(), fmt.Errorf("stat %q: %w", targetPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Sprintf("%q is a directory", targetPath), fmt.Errorf("%q is a directory", targetPath)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, err.Error(), fmt.Errorf("read %q: %w", targetPath, err)
	}

	return &jcmanagerpb.FileReadResponse{
		Path:        targetPath,
		Content:     content,
		SizeBytes:   uint64(len(content)),
		ModTimeUnix: info.ModTime().Unix(),
	}, "file read", nil
}

func validateManagedPath(targetPath string, allowedPaths []string) error {
	for _, allowedPath := range allowedPaths {
		allowedPath = filepath.Clean(strings.TrimSpace(allowedPath))
		if allowedPath == "" {
			continue
		}
		if targetPath == allowedPath {
			return nil
		}
		info, err := os.Stat(allowedPath)
		if err != nil || !info.IsDir() {
			continue
		}
		relativePath, err := filepath.Rel(allowedPath, targetPath)
		if err != nil {
			continue
		}
		if relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("path %q is not allowed for this agent", targetPath)
}

func readManagedFile(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, defaultManagedFileMode, false, nil
		}
		return nil, 0, false, fmt.Errorf("stat %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, 0, false, fmt.Errorf("%q is a directory", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read %q: %w", path, err)
	}
	return content, info.Mode().Perm(), true, nil
}

func createManagedFileBackup(path string, content []byte) (string, error) {
	backupDir := filepath.Join(filepath.Dir(path), ".backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir %q: %w", backupDir, err)
	}

	backupPath := filepath.Join(
		backupDir,
		fmt.Sprintf("%s.%s.bak", filepath.Base(path), time.Now().UTC().Format("20060102T150405.000000000Z")),
	)
	if err := os.WriteFile(backupPath, content, 0o600); err != nil {
		return "", fmt.Errorf("write backup %q: %w", backupPath, err)
	}
	return backupPath, nil
}

func writeManagedFile(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tempFile, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", dir, err)
	}

	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(content); err != nil {
		tempFile.Close()
		return fmt.Errorf("write temp file %q: %w", tempPath, err)
	}
	if err := tempFile.Chmod(mode); err != nil {
		tempFile.Close()
		return fmt.Errorf("chmod temp file %q: %w", tempPath, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temp file %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace %q: %w", path, err)
	}
	cleanup = false
	return nil
}

func rollbackManagedFile(path string, content []byte, mode os.FileMode, existed bool) error {
	if existed {
		return writeManagedFile(path, content, mode)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %q during rollback: %w", path, err)
	}
	return nil
}

func restartManagedService(ctx context.Context, serviceName string) (string, string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "", "", fmt.Errorf("systemctl not found: %w", err)
	}
	return runManagedCommand(ctx, "systemctl", "restart", serviceName)
}

func runManagedCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
