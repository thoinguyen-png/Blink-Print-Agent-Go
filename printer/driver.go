package printer

import (
	"strings"
)

type HealthInfo struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Installed   bool   `json:"installed"`
	Ready       bool   `json:"ready"`
	State       string `json:"state"`
	Message     string `json:"message"`
	Jobs        uint32 `json:"jobs"`
	ProblemJobs uint32 `json:"problem_jobs"`
	StalledJobs uint32 `json:"stalled_jobs"`
}

type Driver interface {
	GetInstalledPrinters() ([]string, error)
	GetDefaultPrinter() (string, error)
	GetHealth(printerName string) HealthInfo
	SendRaw(printerName string, data []byte, docName string) error
}

func BuildCutTestTicket() []byte {
	var ticket []byte
	ticket = append(ticket, 0x1B, 0x40) // ESC @
	ticket = append(ticket, []byte("BLINK PRINT AGENT\nRAW ESC/POS TEST\nXP-80 CUT TEST\n------------------------------\nCUT OK = STEP 2 PASS\n\n\n")...)
	ticket = append(ticket, 0x1B, 0x64, 0x04)       // ESC d 4
	ticket = append(ticket, 0x1D, 0x56, 0x42, 0x00) // GS V B 0 (Cut)
	return ticket
}

func NormalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "kitchen":
		return "kitchen"
	case "storage":
		return "storage"
	default:
		return "cashier"
	}
}