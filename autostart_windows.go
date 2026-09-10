//go:build windows
// +build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const appRegistryName = "BlinkPrintAgent"

// ensureAutoStart tự động cấu hình Windows Registry để Agent tự khởi động ngầm cùng Windows
func ensureAutoStart() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("không xác định được đường dẫn file exe: %w", err)
	}

	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("không lấy được đường dẫn tuyệt đối: %w", err)
	}

	// Đảm bảo đường dẫn được đặt trong dấu ngoặc kép phòng trường hợp có khoảng trắng
	expectedValue := fmt.Sprintf(`"%s"`, exePath)

	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("không mở được Registry Run key: %w", err)
	}
	defer key.Close()

	existing, _, err := key.GetStringValue(appRegistryName)
	if err == nil && strings.EqualFold(strings.Trim(existing, `"`), exePath) {
		return nil // Đã được cấu hình đúng rồi
	}

	if err := key.SetStringValue(appRegistryName, expectedValue); err != nil {
		return fmt.Errorf("không ghi được Registry Run key: %w", err)
	}

	return nil
}
