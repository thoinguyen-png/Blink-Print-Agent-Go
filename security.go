package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// URL cấu hình danh sách domain tập trung trên Cloud
const RemoteOriginsURL = "https://api.blinkgo.tech/v1/agent/allowed-origins.json"

type SecurityManager struct {
	mu           sync.RWMutex
	store        *AgentSettingsStore
	allowedRoots []string
}

// CleanHostname loại bỏ scheme (http/https), path, port để lấy chính xác hostname
func CleanHostname(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			s = strings.ToLower(u.Hostname())
		}
	}
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		return strings.ToLower(host)
	}
	return s
}

func NewSecurityManager(store *AgentSettingsStore) *SecurityManager {
	sm := &SecurityManager{
		store:        store,
		allowedRoots: []string{"blinkgo.tech", "santori.vn", "max360.tech"}, // Domain mặc định cơ sở
	}

	// Nạp domain từ cache cục bộ đã lưu trước đó và chuẩn hóa
	cachedSettings := store.Load()
	if len(cachedSettings.AllowedOrigins) > 0 {
		for _, o := range cachedSettings.AllowedOrigins {
			if c := CleanHostname(o); c != "" {
				sm.addRootNoLock(c)
			}
		}
	}

	// Bắt đầu tiến trình tự động đồng bộ từ Cloud ngầm
	go sm.startSyncWorker()

	return sm
}

func (sm *SecurityManager) addRootNoLock(root string) {
	for _, existing := range sm.allowedRoots {
		if existing == root {
			return
		}
	}
	sm.allowedRoots = append(sm.allowedRoots, root)
}

// Kiểm tra xem Origin gửi lên có hợp lệ hay không (hỗ trợ cả root domain và subdomain)
func (sm *SecurityManager) IsOriginAllowed(origin string) bool {
	if strings.TrimSpace(origin) == "" {
		return true
	}
	norm := NormalizeOrigin(origin)
	if norm == "" {
		return false
	}

	u, err := url.Parse(norm)
	if err != nil {
		return false
	}

	hostname := strings.ToLower(u.Hostname())

	// 1. Luôn cho phép chạy local / dev
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" {
		return true
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// 2. Cho phép chính xác domain hoặc bất kỳ subdomain nào của root domain
	for _, root := range sm.allowedRoots {
		rootNorm := CleanHostname(root)
		if rootNorm == "" {
			continue
		}
		if hostname == rootNorm || strings.HasSuffix(hostname, "."+rootNorm) {
			return true
		}
	}

	return false
}

// Đồng bộ danh sách domain từ xa định kỳ mỗi 2 tiếng
func (sm *SecurityManager) startSyncWorker() {
	sm.fetchRemoteOrigins()
	ticker := time.NewTicker(2 * time.Hour)
	for range ticker.C {
		sm.fetchRemoteOrigins()
	}
}

func (sm *SecurityManager) fetchRemoteOrigins() {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(RemoteOriginsURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	defer resp.Body.Close()

	var remoteDomains []string
	if err := json.NewDecoder(resp.Body).Decode(&remoteDomains); err != nil || len(remoteDomains) == 0 {
		return
	}

	sm.mu.Lock()
	sm.allowedRoots = []string{"blinkgo.tech", "santori.vn", "max360.tech"}
	var cleanedDomains []string
	for _, d := range remoteDomains {
		if c := CleanHostname(d); c != "" {
			sm.addRootNoLock(c)
			cleanedDomains = append(cleanedDomains, c)
		}
	}
	sm.mu.Unlock()

	// Cập nhật lưu vào file cấu hình máy tính để dùng offline
	if len(cleanedDomains) > 0 {
		settings := sm.store.Load()
		settings.AllowedOrigins = cleanedDomains
		_ = sm.store.Save(settings)
	}
}

func NormalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}