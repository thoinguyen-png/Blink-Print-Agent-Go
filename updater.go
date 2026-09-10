package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// AppVersion phiên bản hiện tại của Blink Print Agent
const AppVersion = "0.9.0"

// RemoteVersionURL URL cấu hình phiên bản tập trung trên Cloud
const RemoteVersionURL = "https://api.blinkgo.tech/v1/agent/version.json"

// UpdateInfo cấu trúc thông tin phiên bản từ Cloud
type UpdateInfo struct {
	Version     string `json:"version"`      // Phiên bản mới, ví dụ: "0.9.1"
	DownloadURL string `json:"download_url"` // Đường dẫn tải file binary mới (.exe)
	SHA256      string `json:"sha256"`       // Mã SHA256 để kiểm tra toàn vẹn (tùy chọn)
	Notes       string `json:"notes"`        // Nội dung cập nhật
	Mandatory   bool   `json:"mandatory"`    // Bắt buộc cập nhật hay không
}

// StartAutoUpdater khởi chạy tiến trình kiểm tra cập nhật định kỳ ngầm
func StartAutoUpdater(store *AgentSettingsStore) {
	// Dọn dẹp file .old từ lần cập nhật trước nếu có
	cleanupOldBinary()

	go func() {
		// Đợi 10 giây sau khi khởi động để ưu tiên mở server in ấn trước
		time.Sleep(10 * time.Second)
		checkAndApplyUpdate(store)

		// Định kỳ kiểm tra mỗi 4 tiếng
		ticker := time.NewTicker(4 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			checkAndApplyUpdate(store)
		}
	}()
}

// cleanupOldBinary dọn dẹp file sao lưu cũ sau khi update thành công
func cleanupOldBinary() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	oldPath := exePath + ".old"
	if _, err := os.Stat(oldPath); err == nil {
		_ = os.Remove(oldPath)
	}
}

// checkAndApplyUpdate kiểm tra phiên bản từ Cloud và tự động cập nhật ngầm
func checkAndApplyUpdate(store *AgentSettingsStore) {
	info, err := fetchUpdateInfo(store)
	if err != nil || info == nil {
		return
	}

	if !isNewerVersion(info.Version, AppVersion) {
		return // Đang ở phiên bản mới nhất
	}

	if strings.TrimSpace(info.DownloadURL) == "" {
		return
	}

	fmt.Printf("\n📦 [Auto-Update] Phát hiện phiên bản mới v%s (hiện tại v%s). Đang tải cập nhật ngầm...\n", info.Version, AppVersion)
	if err := applyUpdate(info); err != nil {
		fmt.Printf("⚠️ [Auto-Update] Lỗi khi cập nhật phiên bản mới: %v\n", err)
	}
}

// fetchUpdateInfo gửi yêu cầu lấy thông tin phiên bản mới nhất từ Cloud
func fetchUpdateInfo(store *AgentSettingsStore) (*UpdateInfo, error) {
	checkURL := RemoteVersionURL

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(checkURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server trả về mã trạng thái: %d", resp.StatusCode)
	}

	var info UpdateInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}

// isNewerVersion so sánh 2 chuỗi phiên bản dạng semver (ví dụ "0.9.1" > "0.9.0")
func isNewerVersion(remote, current string) bool {
	rParts := parseVersion(remote)
	cParts := parseVersion(current)

	for i := 0; i < len(rParts) && i < len(cParts); i++ {
		if rParts[i] > cParts[i] {
			return true
		}
		if rParts[i] < cParts[i] {
			return false
		}
	}
	return len(rParts) > len(cParts)
}

func parseVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	res := make([]int, 0, len(parts))
	for _, p := range parts {
		numStr := ""
		for _, ch := range p {
			if ch >= '0' && ch <= '9' {
				numStr += string(ch)
			} else {
				break
			}
		}
		num, _ := strconv.Atoi(numStr)
		res = append(res, num)
	}
	return res
}

// applyUpdate tải file mới, thay thế an toàn vào vị trí file cũ và khởi động lại
func applyUpdate(info *UpdateInfo) error {
	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("không xác định được đường dẫn file đang chạy: %w", err)
	}
	currentExe, err = filepath.Abs(currentExe)
	if err != nil {
		return fmt.Errorf("không lấy được đường dẫn tuyệt đối: %w", err)
	}

	// 1. Tải file mới vào file tạm
	tempFile, err := os.CreateTemp("", "blink-agent-update-*.exe")
	if err != nil {
		return fmt.Errorf("không thể tạo file tạm: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()

	client := http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(info.DownloadURL)
	if err != nil {
		return fmt.Errorf("lỗi kết nối tải file mới: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tải file thất bại, server báo HTTP %d", resp.StatusCode)
	}

	hasher := sha256.New()
	writer := io.MultiWriter(tempFile, hasher)
	if _, err := io.Copy(writer, resp.Body); err != nil {
		return fmt.Errorf("lỗi khi lưu file cập nhật: %w", err)
	}
	_ = tempFile.Close()

	// 2. Kiểm tra mã băm SHA256 nếu server cung cấp
	if strings.TrimSpace(info.SHA256) != "" {
		calcHash := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(calcHash, strings.TrimSpace(info.SHA256)) {
			return fmt.Errorf("mã băm SHA256 không khớp, file tải về có thể bị lỗi")
		}
	}

	// 3. Cơ chế Windows Self-Replacement (Đổi tên file đang chạy thành .old và thế chỗ bằng file mới)
	oldExe := currentExe + ".old"
	_ = os.Remove(oldExe) // Xóa file .old còn sót nếu có

	if err := os.Rename(currentExe, oldExe); err != nil {
		return fmt.Errorf("không thể đổi tên file exe hiện tại: %w", err)
	}

	// Sao chép file vừa tải về vào đúng vị trí exe chính
	if err := copyBinary(tempPath, currentExe); err != nil {
		// Rollback phục hồi lại file cũ nếu lỗi
		_ = os.Rename(oldExe, currentExe)
		return fmt.Errorf("không thể sao chép file mới vào hệ thống: %w", err)
	}

	if runtime.GOOS != "windows" {
		_ = os.Chmod(currentExe, 0755)
	}

	fmt.Printf("🎉 [Auto-Update] Cập nhật thành công lên v%s! Đang khởi động lại Agent...\n", info.Version)

	// 4. Khởi động tiến trình mới
	cmd := exec.Command(currentExe)
	cmd.Dir = filepath.Dir(currentExe)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("không thể khởi động phiên bản mới: %w", err)
	}

	// 5. Thoát tiến trình hiện tại để nhường quyền cho phiên bản mới
	os.Exit(0)
	return nil
}

func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
