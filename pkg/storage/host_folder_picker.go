package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrHostDirectoryPickerUnsupported = errors.New("本机目录选择器仅支持 macOS")

const hostDirectoryPickerScript = `POSIX path of (choose folder with prompt "选择 Mac 本地目录")`

// PickHostDirectory opens the native macOS directory chooser. The caller is
// responsible for authorizing the request and validating the returned path.
// Keeping the command as a direct exec (rather than a shell) prevents a
// selected path from being interpreted as shell syntax.
func PickHostDirectory(ctx context.Context) (path string, cancelled bool, err error) {
	if runtime.GOOS != "darwin" {
		return "", false, ErrHostDirectoryPickerUnsupported
	}

	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", hostDirectoryPickerScript).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		if isHostDirectoryPickerCancellation(string(output)) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("打开 macOS 目录选择器失败")
	}

	path = strings.TrimSpace(string(output))
	if path == "" {
		return "", false, fmt.Errorf("macOS 目录选择器未返回路径")
	}
	return path, false, nil
}

// ValidateHostDirectory canonicalizes and validates a path returned by the
// native picker before it is sent to the browser or persisted in config.
func ValidateHostDirectory(rawPath string) (string, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(rawPath))
	if cleanPath == "." || !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("所选路径必须是本机绝对路径")
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return "", fmt.Errorf("所选本机路径不可用")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("所选路径不是目录")
	}
	return cleanPath, nil
}

func isHostDirectoryPickerCancellation(output string) bool {
	value := strings.ToLower(output)
	return strings.Contains(value, "user canceled") || strings.Contains(value, "user cancelled") || strings.Contains(value, "(-128)")
}
