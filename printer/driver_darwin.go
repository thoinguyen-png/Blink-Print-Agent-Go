//go:build darwin

package printer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type DarwinDriver struct{}

func NewDriver() Driver {
	return &DarwinDriver{}
}

func (d *DarwinDriver) GetInstalledPrinters() ([]string, error) {
	// Dùng đường dẫn tuyệt đối để app chạy ngầm không bị thiếu PATH
	cmd := exec.Command("/usr/bin/lpstat", "-a")
	out, err := cmd.Output()
	if err != nil {
		// Fallback thử với -p nếu -a lỗi
		cmd = exec.Command("/usr/bin/lpstat", "-p")
		out, err = cmd.Output()
		if err != nil {
			return []string{}, nil
		}
	}

	var printers []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 0 {
			if parts[0] == "printer" && len(parts) >= 2 {
				printers = append(printers, parts[1])
			} else {
				// Output của 'lpstat -a' từ đầu tiên luôn là tên máy in
				printers = append(printers, parts[0])
			}
		}
	}
	return printers, nil
}

func (d *DarwinDriver) GetDefaultPrinter() (string, error) {
	cmd := exec.Command("/usr/bin/lpstat", "-d")
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	parts := strings.Split(string(out), ":")
	if len(parts) >= 2 {
		return strings.TrimSpace(parts[1]), nil
	}
	return "", nil
}

func (d *DarwinDriver) GetHealth(printerName string) HealthInfo {
	if printerName == "" {
		return HealthInfo{
			State:   "not_configured",
			Message: "Chưa chọn máy in cho Blink Print Agent.",
		}
	}

	installedList, _ := d.GetInstalledPrinters()
	installed := false
	actualPrinterName := printerName

	for _, p := range installedList {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(printerName)) {
			installed = true
			actualPrinterName = p
			break
		}
	}

	if !installed {
		return HealthInfo{
			Name:      printerName,
			State:     "not_installed",
			Message:   fmt.Sprintf("Máy in %s không tồn tại trong CUPS/macOS.", printerName),
			Installed: false,
		}
	}

	cmd := exec.Command("/usr/bin/lpstat", "-p", actualPrinterName)
	out, err := cmd.Output()
	statusText := strings.ToLower(string(out))

	if err != nil || strings.Contains(statusText, "disabled") || strings.Contains(statusText, "paused") {
		return HealthInfo{
			Name:      actualPrinterName,
			Installed: true,
			State:     "paused",
			Message:   fmt.Sprintf("Hàng đợi máy in %s đang tạm dừng.", actualPrinterName),
		}
	}

	return HealthInfo{
		Name:       actualPrinterName,
		Installed:  true,
		Configured: true,
		Ready:      true,
		State:      "ready",
		Message:    "Máy in sẵn sàng.",
	}
}

func (d *DarwinDriver) SendRaw(printerName string, data []byte, docName string) error {
	tmpFile, err := os.CreateTemp("", "blink_raw_*.bin")
	if err != nil {
		return fmt.Errorf("không thể tạo file in tạm: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("lỗi ghi dữ liệu in: %w", err)
	}
	_ = tmpFile.Close()

	cmd := exec.Command("/usr/bin/lpr", "-P", printerName, "-o", "raw", "-T", docName, tmpFile.Name())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("CUPS print error: %s (%w)", stderr.String(), err)
	}
	return nil
}