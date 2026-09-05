//go:build darwin

package printer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type DarwinDriver struct{}

func NewDriver() Driver {
	return &DarwinDriver{}
}

// GetInstalledPrinters lấy danh sách máy in CUPS hiện có và tự động kích hoạt máy in USB vừa cắm vào (Zero-Driver)
func (d *DarwinDriver) GetInstalledPrinters() ([]string, error) {
	// 1. Tự động quét và kích hoạt máy in USB mới cắm mà chưa tạo queue (Plug & Play không cần cài driver)
	d.autoSetupUSBPrinters()

	// 2. Lấy danh sách máy in từ CUPS
	cmd := exec.Command("/usr/bin/lpstat", "-a")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
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

// autoSetupUSBPrinters tự động phát hiện thiết bị USB cắm vào và tạo RAW queue tự động
func (d *DarwinDriver) autoSetupUSBPrinters() {
	cmd := exec.Command("/usr/sbin/lpinfo", "-v")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return
	}

	// Lấy danh sách máy in đã có
	existingCmd := exec.Command("/usr/bin/lpstat", "-a")
	existingCmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	existingOut, _ := existingCmd.Output()
	existingStr := string(existingOut)

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Tìm các cổng USB trực tiếp: "direct usb://..."
		if strings.HasPrefix(line, "direct usb://") || strings.HasPrefix(line, "usb://") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			uri := parts[1]

			// Tạo tên máy in thân thiện từ URI (VD: usb://Xprinter/XP-80C -> Xprinter_XP_80C)
			printerName := extractCleanNameFromURI(uri)
			if printerName == "" {
				printerName = "Blink_USB_Printer"
			}

			// Nếu máy in này chưa có trong CUPS, tự động tạo RAW queue
			if !strings.Contains(existingStr, printerName) {
				adminCmd := exec.Command("/usr/sbin/lpadmin", "-p", printerName, "-v", uri, "-E", "-m", "raw")
				_ = adminCmd.Run()
			}
		}
	}
}

var cleanNameRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func extractCleanNameFromURI(uri string) string {
	// uri ví dụ: usb://Xprinter/XP-80C?serial=... hoặc usb://POS-80...
	trimmed := strings.TrimPrefix(uri, "direct ")
	trimmed = strings.TrimPrefix(trimmed, "usb://")
	if idx := strings.Index(trimmed, "?"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	parts := strings.Split(trimmed, "/")
	name := strings.Join(parts, "_")
	name = cleanNameRegex.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	return name
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