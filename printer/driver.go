package printer

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
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

func IsNetworkTarget(target string) bool {
	target = strings.TrimSpace(target)
	ipStr := target
	if strings.Contains(target, ":") {
		host, _, err := net.SplitHostPort(target)
		if err == nil {
			ipStr = host
		}
	}
	return net.ParseIP(ipStr) != nil
}

func CheckNetworkHealth(target string) HealthInfo {
	addr := strings.TrimSpace(target)
	if !strings.Contains(addr, ":") {
		addr = addr + ":9100"
	}

	conn, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
	if err != nil {
		return HealthInfo{
			Name:       target,
			Configured: true,
			Installed:  false,
			Ready:      false,
			State:      "offline",
			Message:    fmt.Sprintf("Không thể kết nối tới máy in mạng (%s)", addr),
		}
	}
	_ = conn.Close()

	return HealthInfo{
		Name:       target,
		Configured: true,
		Installed:  true,
		Ready:      true,
		State:      "ready",
		Message:    "Máy in mạng sẵn sàng (Port 9100).",
	}
}

func SendRawNetwork(target string, data []byte) error {
	addr := strings.TrimSpace(target)
	if !strings.Contains(addr, ":") {
		addr = addr + ":9100"
	}

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("lỗi kết nối máy in mạng %s: %w", addr, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write(data)
	return err
}

func DiscoverNetworkPrinters() []string {
	var found []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return found
	}

	var localSubnets []string
	for _, a := range addrs {
		if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			ip := ipNet.IP.To4()
			localSubnets = append(localSubnets, fmt.Sprintf("%d.%d.%d", ip[0], ip[1], ip[2]))
		}
	}

	for _, subnet := range localSubnets {
		for i := 1; i <= 254; i++ {
			wg.Add(1)
			targetIP := fmt.Sprintf("%s.%d", subnet, i)
			go func(ip string) {
				defer wg.Done()
				conn, err := net.DialTimeout("tcp", ip+":9100", 300*time.Millisecond)
				if err == nil {
					_ = conn.Close()
					mu.Lock()
					found = append(found, ip)
					mu.Unlock()
				}
			}(targetIP)
		}
	}

	wg.Wait()
	return found
}