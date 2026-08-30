package main

import (
	"encoding/json"
	"fmt"
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

func NewSecurityManager(store *AgentSettingsStore) *SecurityManager {
	sm := &SecurityManager{
		store:        store,
		allowedRoots: []string{"blinkgo.tech", "santori.vn"}, // Domain mặc định cơ sở
	}

	// Nạp domain từ cache cục bộ đã lưu trước đó
	cachedSettings := store.Load()
	if len(cachedSettings.AllowedOrigins) > 0 {
		sm.allowedRoots = cachedSettings.AllowedOrigins
	}

	// Bắt đầu tiến trình tự động đồng bộ từ Cloud ngầm
	go sm.startSyncWorker()

	return sm
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
		rootNorm := strings.ToLower(strings.TrimSpace(root))
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
	sm.allowedRoots = remoteDomains
	sm.mu.Unlock()

	// Cập nhật lưu vào file cấu hình máy tính để dùng offline
	settings := sm.store.Load()
	settings.AllowedOrigins = remoteDomains
	_ = sm.store.Save(settings)
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