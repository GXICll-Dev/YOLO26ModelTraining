package main

import (
	"path/filepath"
	"testing"
)

func TestUpdateExecutable(t *testing.T) {
	uninstaller := filepath.Join("C:\\Users\\Tester", "AppData", "Local", "YOLO26ModelTraining", "Uninstall.exe")
	want := filepath.Join("C:\\Users\\Tester", "AppData", "Local", "YOLO26ModelTraining", "Update.exe")
	if got := updateExecutable(uninstaller); got != want {
		t.Fatalf("updateExecutable() = %q, want %q", got, want)
	}
}
