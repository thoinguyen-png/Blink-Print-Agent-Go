package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"blink-print-agent/printer"
)

type Server struct {
	store    *AgentSettingsStore
	security *SecurityManager
	tlsMgr   *TLSManager
	driver   printer.Driver
	printMu  sync.Mutex
}

func NewServer(store *AgentSettingsStore, sec *SecurityManager, drv printer.Driver) *Server {
	return &Server{
		store:    store,
		security: sec,
		tlsMgr:   NewTLSManager(store),
		driver:   drv,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/security", s.handleSecurity)
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/printers", s.handlePrinters)
	mux.HandleFunc("/v1/settings/printer", s.handleSetPrinter)
	mux.HandleFunc("/v1/print/test", s.handleTestPrint)
	mux.HandleFunc("/v1/print/raw", s.handleRawPrint)

	handler := s.corsMiddleware(mux)

	rawListener, err := net.Listen("tcp", "127.0.0.1:18181")
	if err != nil {
		return fmt.Errorf("không thể mở cổng 18181: %w", err)
	}
	defer rawListener.Close()

	tlsConfig, err := s.tlsMgr.GetTLSConfig()
	if err != nil {
		// Fallback sang plain HTTP nếu TLS không khởi tạo được
		httpServ := &http.Server{Handler: handler}
		return httpServ.Serve(rawListener)
	}

	httpListener := newChanListener(rawListener.Addr())
	tlsListener := newChanListener(rawListener.Addr())

	// Khởi chạy HTTP Server (cho localhost / dev)
	go func() {
		httpServ := &http.Server{Handler: handler}
		_ = httpServ.Serve(httpListener)
	}()

	// Khởi chạy HTTPS Server (cho Web Cloud https://dev.blinkgo.tech)
	go func() {
		tlsServ := &http.Server{Handler: handler, TLSConfig: tlsConfig}
		_ = tlsServ.Serve(tls.NewListener(tlsListener, tlsConfig))
	}()

	// Dispatcher loop: Đọc byte đầu tiên để phân luồng HTTP vs HTTPS trên cùng cổng 18181
	for {
		conn, err := rawListener.Accept()
		if err != nil {
			return err
		}

		go func(c net.Conn) {
			buf := make([]byte, 1)
			n, err := c.Read(buf)
			if err != nil || n == 0 {
				_ = c.Close()
				return
			}

			// Gói lại connection để không làm mất byte đầu tiên
			wrapped := &prefixedConn{
				Conn: c,
				r:    io.MultiReader(bytes.NewReader(buf[:n]), c),
			}

			if buf[0] == 0x16 { // Byte bắt đầu của gói tin TLS Handshake (HTTPS)
				select {
				case tlsListener.ch <- wrapped:
				default:
					_ = c.Close()
				}
			} else { // Plain HTTP (GET, POST, OPTIONS...)
				select {
				case httpListener.ch <- wrapped:
				default:
					_ = c.Close()
				}
			}
		}(conn)
	}
}

type prefixedConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

type chanListener struct {
	addr      net.Addr
	ch        chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{
		addr: addr,
		ch:   make(chan net.Conn, 128),
		done: make(chan struct{}),
	}
}

func (cl *chanListener) Accept() (net.Conn, error) {
	select {
	case c, ok := <-cl.ch:
		if !ok {
			return nil, errors.New("listener closed")
		}
		return c, nil
	case <-cl.done:
		return nil, errors.New("listener closed")
	}
}

func (cl *chanListener) Close() error {
	cl.closeOnce.Do(func() {
		close(cl.done)
	})
	return nil
}

func (cl *chanListener) Addr() net.Addr {
	return cl.addr
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if !s.security.IsOriginAllowed(origin) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": false, "error_code": "origin_not_allowed", "message": "Domain không thuộc danh sách cấp phép của Blink.",
			})
			return
		}

		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Blink-Print-Token, Access-Control-Request-Private-Network")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.Header().Set("Cache-Control", "no-store")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) resolvePrinter(settings *AgentSettings, role string) string {
	norm := printer.NormalizeRole(role)
	cashier := settings.CashierPrinterName
	if cashier == "" {
		cashier, _ = s.driver.GetDefaultPrinter()
	}

	if norm == "kitchen" && settings.KitchenPrinterName != "" {
		return settings.KitchenPrinterName
	}
	if norm == "storage" && settings.StoragePrinterName != "" {
		return settings.StoragePrinterName
	}
	return cashier
}

func (s *Server) getPrinterHealth(printerName string) printer.HealthInfo {
	if printer.IsNetworkTarget(printerName) {
		return printer.CheckNetworkHealth(printerName)
	}
	return s.driver.GetHealth(printerName)
}

func (s *Server) sendRawData(printerName string, data []byte, docName string) error {
	if printer.IsNetworkTarget(printerName) {
		return printer.SendRawNetwork(printerName, data)
	}
	return s.driver.SendRaw(printerName, data, docName)
}

func (s *Server) getAllAvailablePrinters() []string {
	installedList, _ := s.driver.GetInstalledPrinters()
	netPrinters := printer.DiscoverNetworkPrinters()

	all := make([]string, 0, len(installedList)+len(netPrinters))
	all = append(all, installedList...)
	for _, ip := range netPrinters {
		all = append(all, ip)
	}
	return all
}

func (s *Server) handleSecurity(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":        true,
		"version":        "0.9.0",
		"token_enforced": false,
		"origin":         NormalizeOrigin(origin),
		"origin_allowed": s.security.IsOriginAllowed(origin),
		"message":        "Dynamic Cloud Origin Security active.",
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	settings := s.store.Load()
	role := printer.NormalizeRole(r.URL.Query().Get("printer_role"))
	targetPrinter := s.resolvePrinter(settings, role)
	health := s.getPrinterHealth(targetPrinter)
	availableList := s.getAllAvailablePrinters()
	defPrinter, _ := s.driver.GetDefaultPrinter()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":            true,
		"name":               "Blink Print Agent",
		"version":            "0.9.0",
		"status":             "online",
		"port":               18181,
		"printer_role":       role,
		"printer":            health.Name,
		"defaultPrinter":     defPrinter,
		"available_printers": availableList,
		"printers":           availableList,
		"printer_ready":      health.Ready,
		"printer_state":      health.State,
		"printer_message":    health.Message,
		"printer_installed":  health.Installed,
		"printer_configured": health.Configured,
		"printer_jobs":       health.Jobs,
		"printer_assignments": map[string]string{
			"cashier": s.resolvePrinter(settings, "cashier"),
			"kitchen": s.resolvePrinter(settings, "kitchen"),
			"storage": s.resolvePrinter(settings, "storage"),
		},
	})
}

func (s *Server) handlePrinters(w http.ResponseWriter, r *http.Request) {
	settings := s.store.Load()
	role := printer.NormalizeRole(r.URL.Query().Get("printer_role"))
	target := s.resolvePrinter(settings, role)
	health := s.getPrinterHealth(target)
	availableList := s.getAllAvailablePrinters()
	def, _ := s.driver.GetDefaultPrinter()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":         true,
		"printer_role":    role,
		"printer":         target,
		"defaultPrinter":  def,
		"printers":        availableList,
		"printer_ready":   health.Ready,
		"printer_state":   health.State,
		"printer_message": health.Message,
	})
}

func (s *Server) handleSetPrinter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PrinterName string `json:"printer_name"`
		PrinterRole string `json:"printer_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	role := printer.NormalizeRole(body.PrinterRole)
	settings := s.store.Load()
	switch role {
	case "kitchen":
		settings.KitchenPrinterName = body.PrinterName
	case "storage":
		settings.StoragePrinterName = body.PrinterName
	default:
		settings.CashierPrinterName = body.PrinterName
		settings.PrinterName = body.PrinterName
	}
	_ = s.store.Save(settings)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "printer_role": role, "selected": body.PrinterName})
}

func (s *Server) handleTestPrint(w http.ResponseWriter, r *http.Request) {
	s.printMu.Lock()
	defer s.printMu.Unlock()

	settings := s.store.Load()
	role := printer.NormalizeRole(r.URL.Query().Get("printer_role"))
	targetPrinter := s.resolvePrinter(settings, role)
	health := s.getPrinterHealth(targetPrinter)

	if !health.Ready {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error_code": "printer_" + health.State, "message": health.Message})
		return
	}

	err := s.sendRawData(targetPrinter, printer.BuildCutTestTicket(), "Blink Print Agent - TEST")
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "printer_role": role, "printer": targetPrinter, "message": "Đã in test thành công."})
}

func (s *Server) handleRawPrint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DocumentName string `json:"document_name"`
		RawBase64    string `json:"raw_base64"`
		PrinterRole  string `json:"printer_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RawBase64 == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	rawBytes, err := base64.StdEncoding.DecodeString(body.RawBase64)
	if err != nil || len(rawBytes) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	s.printMu.Lock()
	defer s.printMu.Unlock()

	settings := s.store.Load()
	role := printer.NormalizeRole(body.PrinterRole)
	targetPrinter := s.resolvePrinter(settings, role)

	docName := body.DocumentName
	if docName == "" {
		docName = "Blink Print"
	}

	if err := s.sendRawData(targetPrinter, rawBytes, docName); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "printer_role": role, "printer": targetPrinter, "bytes": len(rawBytes)})
}