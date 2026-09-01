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
	// Ép hệ thống dùng ngôn ngữ chuẩn C/English để output đồng nhất tuyệt đối
	cmd := exec.Command("/usr/bin/lpstat", "-a")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		// Fallback với cờ -e nếu -a bị lỗi
		cmd = exec.Command("/usr/bin/lpstat", "-e")
		cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
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
		// 'lpstat -a' luôn trả về tên máy in ở từ đầu tiên
		parts := strings.Fields(line)
		if len(parts) > 0 {
			if parts[0] == "printer" && len(parts) >= 2 {
				printers = append(printers, parts[1])
			} else {
				printers = append(printers, parts[0])
			}
		}
	}
	return printers, nil
}

func (d *DarwinDriver) GetDefaultPrinter() (string, error) {
	cmd := exec.Command("/usr/bin/lpstat", "-d")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
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
	if strings.TrimSpace(printerName) == "" {
		return HealthInfo{
			State:   "not_configured",
			Message: "Chưa chọn máy in cho Blink Print Agent.",
		}
	}

	installedList, _ := d.GetInstalledPrinters()
	installed := false
	actualPrinterName := strings.TrimSpace(printerName)

	for _, p := range installedList {
		if strings.EqualFold(strings.TrimSpace(p), actualPrinterName) {
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
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
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
	cmd := exec.Command("/usr/bin/lpr", "-P", printerName, "-o", "raw", "-T", docName)
	cmd.Stdin = bytes.NewReader(data) // Gửi trực tiếp bytes qua stdin
	
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("CUPS print error: %s (%w)", stderr.String(), err)
	}
	return nil
}