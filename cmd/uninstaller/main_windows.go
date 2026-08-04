package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

const errorIcon = 0x00000010

func updateExecutable(uninstallerPath string) string {
	return filepath.Join(filepath.Dir(uninstallerPath), "Update.exe")
}

func showError(message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString("无法卸载 YOLO26 Model Training")
	body, _ := syscall.UTF16PtrFromString(message)
	messageBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), errorIcon)
}

func main() {
	executable, err := os.Executable()
	if err != nil {
		showError(fmt.Sprintf("无法定位卸载程序：%v", err))
		return
	}
	updateExe := updateExecutable(executable)
	if info, statErr := os.Stat(updateExe); statErr != nil || info.IsDir() {
		showError(fmt.Sprintf("找不到 Squirrel 卸载组件：\n%s", updateExe))
		return
	}

	command := exec.Command(updateExe, "--uninstall")
	command.Dir = filepath.Dir(updateExe)
	if err := command.Start(); err != nil {
		showError(fmt.Sprintf("无法启动卸载程序：%v", err))
		return
	}
	_ = command.Process.Release()
}
