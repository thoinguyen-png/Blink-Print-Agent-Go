//go:build windows

package printer

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	winspool               = syscall.NewLazyDLL("winspool.drv")
	procOpenPrinterW       = winspool.NewProc("OpenPrinterW")
	procClosePrinter       = winspool.NewProc("ClosePrinter")
	procStartDocPrinterW   = winspool.NewProc("StartDocPrinterW")
	procEndDocPrinter     = winspool.NewProc("EndDocPrinter")
	procStartPagePrinter   = winspool.NewProc("StartPagePrinter")
	procEndPagePrinter     = winspool.NewProc("EndPagePrinter")
	procWritePrinter       = winspool.NewProc("WritePrinter")
	procEnumPrintersW      = winspool.NewProc("EnumPrintersW")
	procGetDefaultPrinterW = winspool.NewProc("GetDefaultPrinterW")
)

type DOC_INFO_1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

type PRINTER_INFO_4 struct {
	PrinterName *uint16
	ServerName  *uint16
	Attributes  uint32
}

type WindowsDriver struct{}

func NewDriver() Driver {
	return &WindowsDriver{}
}

func (d *WindowsDriver) GetInstalledPrinters() ([]string, error) {
	var flags uint32 = 2 | 4 // PRINTER_ENUM_LOCAL | PRINTER_ENUM_CONNECTIONS
	var needed, returned uint32

	procEnumPrintersW.Call(
		uintptr(flags),
		0,
		4,
		0,
		0,
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if needed == 0 {
		return []string{}, nil
	}

	buf := make([]byte, needed)
	r1, _, _ := procEnumPrintersW.Call(
		uintptr(flags),
		0,
		4,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(needed),
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if r1 == 0 {
		return []string{}, fmt.Errorf("EnumPrintersW failed")
	}

	var printers []string
	size := unsafe.Sizeof(PRINTER_INFO_4{})
	for i := uintptr(0); i < uintptr(returned); i++ {
		info := (*PRINTER_INFO_4)(unsafe.Pointer(&buf[i*size]))
		if info.PrinterName != nil {
			printers = append(printers, syscall.UTF16ToString((*[1 << 16]uint16)(unsafe.Pointer(info.PrinterName))[:]))
		}
	}
	return printers, nil
}

func (d *WindowsDriver) GetDefaultPrinter() (string, error) {
	var needed uint32
	procGetDefaultPrinterW.Call(0, uintptr(unsafe.Pointer(&needed)))
	if needed == 0 {
		return "", nil
	}
	buf := make([]uint16, needed)
	r1, _, _ := procGetDefaultPrinterW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&needed)))
	if r1 == 0 {
		return "", nil
	}
	return syscall.UTF16ToString(buf), nil
}

func (d *WindowsDriver) GetHealth(printerName string) HealthInfo {
	if printerName == "" {
		return HealthInfo{State: "not_configured", Message: "Chưa chọn máy in."}
	}
	installedPrinters, _ := d.GetInstalledPrinters()
	installed := false
	for _, p := range installedPrinters {
		if p == printerName {
			installed = true
			break
		}
	}
	if !installed {
		return HealthInfo{Name: printerName, State: "not_installed", Message: "Máy in không tồn tại trong Windows."}
	}
	return HealthInfo{Name: printerName, Configured: true, Installed: true, Ready: true, State: "ready", Message: "Máy in sẵn sàng."}
}

func (d *WindowsDriver) SendRaw(printerName string, data []byte, docName string) error {
	pName, _ := syscall.UTF16PtrFromString(printerName)
	var handle syscall.Handle
	r1, _, err := procOpenPrinterW.Call(uintptr(unsafe.Pointer(pName)), uintptr(unsafe.Pointer(&handle)), 0)
	if r1 == 0 {
		return fmt.Errorf("OpenPrinter error: %w", err)
	}
	defer procClosePrinter.Call(uintptr(handle))

	dName, _ := syscall.UTF16PtrFromString(docName)
	dType, _ := syscall.UTF16PtrFromString("RAW")
	docInfo := DOC_INFO_1{DocName: dName, OutputFile: nil, Datatype: dType}

	jobId, _, err := procStartDocPrinterW.Call(uintptr(handle), 1, uintptr(unsafe.Pointer(&docInfo)))
	if jobId == 0 {
		return fmt.Errorf("StartDocPrinter error: %w", err)
	}
	defer procEndDocPrinter.Call(uintptr(handle))

	procStartPagePrinter.Call(uintptr(handle))
	defer procEndPagePrinter.Call(uintptr(handle))

	var written uint32
	r1, _, err = procWritePrinter.Call(uintptr(handle), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&written)))
	if r1 == 0 || written != uint32(len(data)) {
		return fmt.Errorf("WritePrinter failed: %w", err)
	}
	return nil
}