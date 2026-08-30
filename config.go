package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type AgentSettings struct {
	PrinterName        string   `json:"printerName"`
	CashierPrinterName string   `json:"cashierPrinterName"`
	KitchenPrinterName string   `json:"kitchenPrinterName"`
	StoragePrinterName string   `json:"storagePrinterName"`
	AllowedOrigins     []string `json:"allowedOrigins"`
}

type AgentSettingsStore struct {
	mu           sync.RWMutex
	settingsPath string
}

func NewAgentSettingsStore() *AgentSettingsStore {
	var baseDir string
	if runtime.GOOS == "windows" {
		baseDir = os.Getenv("LOCALAPPDATA")
		if baseDir == "" {
			baseDir = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
	} else {
		home, _ := os.UserHomeDir()
		baseDir = filepath.Join(home, "Library", "Application Support")
	}

	dataDir := filepath.Join(baseDir, "Blink", "PrintAgent")
	_ = os.MkdirAll(dataDir, 0755)

	return &AgentSettingsStore{
		settingsPath: filepath.Join(dataDir, "settings.json"),
	}
}

func (s *AgentSettingsStore) Load() *AgentSettings {
	s.mu.Lock()
	defer s.mu.Unlock()

	settings := &AgentSettings{AllowedOrigins: []string{}}
	data, err := os.ReadFile(s.settingsPath)
	if err != nil {
		s.saveNoLock(settings)
		return settings
	}

	if err := json.Unmarshal(data, settings); err != nil {
		return &AgentSettings{AllowedOrigins: []string{}}
	}

	changed := false
	if settings.CashierPrinterName == "" && settings.PrinterName != "" {
		settings.CashierPrinterName = strings.TrimSpace(settings.PrinterName)
		changed = true
	}
	if settings.PrinterName == "" && settings.CashierPrinterName != "" {
		settings.PrinterName = strings.TrimSpace(settings.CashierPrinterName)
		changed = true
	}

	if changed {
		s.saveNoLock(settings)
	}

	return settings
}

func (s *AgentSettingsStore) Save(settings *AgentSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveNoLock(settings)
}

func (s *AgentSettingsStore) saveNoLock(settings *AgentSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.settingsPath, data, 0644)
}