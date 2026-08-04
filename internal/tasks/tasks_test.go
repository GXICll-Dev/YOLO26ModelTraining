package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFailAddsDetailedErrorToTaskLog(t *testing.T) {
	task := NewManager().Create("train", t.TempDir())
	task.Fail(errors.New("exit status 1\nPython stderr tail:\nRuntimeError: CUDA failed"))

	snapshot := task.Snapshot()
	if snapshot.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", snapshot.Status, StatusFailed)
	}
	if len(snapshot.Logs) == 0 || !strings.Contains(snapshot.Logs[len(snapshot.Logs)-1].Message, "RuntimeError: CUDA failed") {
		t.Fatalf("failure detail was not appended to logs: %#v", snapshot.Logs)
	}
}

func TestPersistentManagerSerializesConcurrentDetailedLogs(t *testing.T) {
	storeDir := t.TempDir()
	manager := NewPersistentManager(storeDir)
	task := manager.Create("train", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	task.Start(ctx, cancel)

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < 50; index++ {
				task.Log("[Python stderr] detailed output")
			}
		}()
	}
	wg.Wait()
	task.Fail(errors.New("exit status 1"))

	data, err := os.ReadFile(filepath.Join(storeDir, task.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted Task
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("persisted task JSON is invalid: %v\n%s", err, data)
	}
	if persisted.Status != StatusFailed || persisted.Error != "exit status 1" {
		t.Fatalf("persisted task = %#v", persisted)
	}
}
