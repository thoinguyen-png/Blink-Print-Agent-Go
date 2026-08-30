package main

import (
	"fmt"
	"os"

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

	go func() {
		if err := server.Start(); err != nil {
			fmt.Printf("Server Error: %v\n", err)
		}
	}()

	systray.Run(onReady, onExit)
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
				return
			}
		}
	}()
}

func onExit() {
	os.Exit(0)
}