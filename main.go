package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"blink-print-agent/printer"
	"github.com/getlantern/systray"
)

var (
	store    *AgentSettingsStore
	security *SecurityManager
	drv      printer.Driver
	server   *Server
)

func main() {
	store = NewAgentSettingsStore()
	security = NewSecurityManager(store)
	drv = printer.NewDriver()
	server = NewServer(store, security, drv)

	fmt.Println("==================================================")
	fmt.Println("🚀 BLINK PRINT AGENT v0.9.0 ĐANG HOẠT ĐỘNG")
	fmt.Println("📍 HTTP Localhost : http://127.0.0.1:18181")
	fmt.Println("🔒 HTTPS Cloud    : https://local.blinkgo.tech:18181")
	fmt.Println("==================================================")
	fmt.Println("Agent đang chạy ngầm sẵn sàng nhận lệnh in...")

	// Khởi chạy HTTP & HTTPS Server song song
	go func() {
		if err := server.Start(); err != nil {
			fmt.Printf("❌ Lỗi Server: %v\n", err)
		}
	}()

	// Lắng nghe tín hiệu dừng chương trình (Ctrl+C hoặc đóng cửa sổ)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nĐang đóng Blink Print Agent...")
		os.Exit(0)
	}()

	// Chạy systray ngầm (nếu môi trường hỗ trợ tray icon)
	go func() {
		systray.Run(onReady, onExit)
	}()

	// Giữ tiến trình luôn chạy liên tục
	select {}
}

func onReady() {
	systray.SetTitle("Blink Print Agent")
	systray.SetTooltip("Blink Print Agent (Online)")

	mStatus := systray.AddMenuItem("Blink Agent: Online", "Trạng thái Agent")
	mStatus.Disable()

	systray.AddSeparator()

	mTest := systray.AddMenuItem("In thử kiểm tra máy in", "Gửi lệnh ESC/POS in test")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Thoát", "Đóng ứng dụng")

	go func() {
		for {
			select {
			case <-mTest.ClickedCh:
				settings := store.Load()
				cashier := server.resolvePrinter(settings, "cashier")
				_ = drv.SendRaw(cashier, printer.BuildCutTestTicket(), "Blink Print - Cut Test")
			case <-mQuit.ClickedCh:
				systray.Quit()
				os.Exit(0)
				return
			}
		}
	}()
}

func onExit() {
	// Không gọi os.Exit trực tiếp để tránh tắt server ngoài ý muốn
}