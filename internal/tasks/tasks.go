package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

type LogEntry struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

type Task struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	ProjectRoot string     `json:"projectRoot"`
	Status      Status     `json:"status"`
	Progress    int        `json:"progress"`
	Logs        []LogEntry `json:"logs"`
	Result      any        `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	PID         int        `json:"pid,omitempty"`

	cancel  context.CancelFunc
	manager *Manager
	mu      *sync.Mutex
	// lastPersistedAt throttles high-frequency progress and console output.
	// Terminal state changes always bypass the throttle.
	lastPersistedAt time.Time
}

type Manager struct {
	mu       sync.RWMutex
	saveMu   sync.Mutex
	tasks    map[string]*Task
	storeDir string
}

const taskPersistInterval = 500 * time.Millisecond

func NewManager() *Manager {
	return &Manager{tasks: map[string]*Task{}}
}

func NewPersistentManager(storeDir string) *Manager {
	manager := &Manager{tasks: map[string]*Task{}, storeDir: storeDir}
	_ = os.MkdirAll(storeDir, 0o755)
	manager.load()
	return manager
}

func (m *Manager) Create(taskType, projectRoot string) *Task {
	task := &Task{
		ID:          newID(),
		Type:        taskType,
		ProjectRoot: projectRoot,
		Status:      StatusPending,
		CreatedAt:   time.Now(),
		Logs:        []LogEntry{},
		manager:     m,
		mu:          &sync.Mutex{},
	}
	m.mu.Lock()
	m.tasks[task.ID] = task
	m.mu.Unlock()
	m.save(task)
	return task
}

func (m *Manager) Get(id string) (*Task, bool) {
	m.mu.RLock()
	task, ok := m.tasks[id]
	m.mu.RUnlock()
	return task, ok
}

func (m *Manager) List() []*Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		out = append(out, task)
	}
	return out
}

func (t *Task) Snapshot() Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := *t
	cp.Logs = append([]LogEntry(nil), t.Logs...)
	if cp.Logs == nil {
		cp.Logs = []LogEntry{}
	}
	cp.cancel = nil
	cp.manager = nil
	cp.mu = &sync.Mutex{}
	return cp
}

func (t *Task) Start(ctx context.Context, cancel context.CancelFunc) {
	now := time.Now()
	t.mu.Lock()
	t.Status = StatusRunning
	t.StartedAt = &now
	t.cancel = cancel
	t.mu.Unlock()
	t.persist(true)
	_ = ctx
}

func (t *Task) SetPID(pid int) {
	t.mu.Lock()
	t.PID = pid
	t.mu.Unlock()
	t.persist(true)
}

func (t *Task) Log(message string) {
	t.mu.Lock()
	t.Logs = append(t.Logs, LogEntry{At: time.Now(), Message: message})
	if len(t.Logs) > 2000 {
		t.Logs = t.Logs[len(t.Logs)-2000:]
	}
	t.mu.Unlock()
	t.persist(false)
}

func (t *Task) SetProgress(progress int) {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	t.mu.Lock()
	t.Progress = progress
	t.mu.Unlock()
	t.persist(false)
}

func (t *Task) Succeed(result any) {
	now := time.Now()
	t.mu.Lock()
	t.Status = StatusSucceeded
	t.Progress = 100
	t.Result = result
	t.FinishedAt = &now
	t.cancel = nil
	t.mu.Unlock()
	t.persist(true)
}

func (t *Task) Fail(err error) {
	now := time.Now()
	t.mu.Lock()
	t.Status = StatusFailed
	if err != nil {
		t.Error = err.Error()
		message := "[任务失败] " + err.Error()
		if len(t.Logs) == 0 || t.Logs[len(t.Logs)-1].Message != message {
			t.Logs = append(t.Logs, LogEntry{At: now, Message: message})
			if len(t.Logs) > 2000 {
				t.Logs = t.Logs[len(t.Logs)-2000:]
			}
		}
	}
	t.FinishedAt = &now
	t.cancel = nil
	t.mu.Unlock()
	t.persist(true)
}

func (t *Task) Cancel() bool {
	t.mu.Lock()
	cancel := t.cancel
	t.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (t *Task) MarkCanceled() {
	now := time.Now()
	t.mu.Lock()
	t.Status = StatusCanceled
	t.FinishedAt = &now
	t.cancel = nil
	t.mu.Unlock()
	t.persist(true)
}

func (t *Task) persist(force bool) {
	t.mu.Lock()
	manager := t.manager
	now := time.Now()
	if manager == nil || (!force && !t.lastPersistedAt.IsZero() && now.Sub(t.lastPersistedAt) < taskPersistInterval) {
		t.mu.Unlock()
		return
	}
	t.lastPersistedAt = now
	t.mu.Unlock()
	manager.save(t)
}

func (m *Manager) save(task *Task) {
	if m.storeDir == "" {
		return
	}
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	snapshot := task.Snapshot()
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(m.storeDir, 0o755); err != nil {
		return
	}
	target := filepath.Join(m.storeDir, snapshot.ID+".json")
	temp, err := os.CreateTemp(m.storeDir, snapshot.ID+"-*.tmp")
	if err != nil {
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return
	}
	if err := temp.Close(); err != nil {
		return
	}
	_ = os.Chmod(tempPath, 0o644)
	if err := os.Rename(tempPath, target); err != nil {
		// Some Windows filesystems or antivirus products can temporarily reject
		// replacement renames. The per-manager save lock still makes this fallback
		// safe from concurrent writers.
		_ = os.WriteFile(target, data, 0o644)
	}
}

func (m *Manager) load() {
	if m.storeDir == "" {
		return
	}
	entries, err := os.ReadDir(m.storeDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.storeDir, entry.Name()))
		if err != nil {
			continue
		}
		var task Task
		if err := json.Unmarshal(data, &task); err != nil || task.ID == "" {
			continue
		}
		task.cancel = nil
		task.manager = m
		task.mu = &sync.Mutex{}
		if task.Status == StatusRunning || task.Status == StatusPending {
			task.Status = StatusCanceled
			now := time.Now()
			task.FinishedAt = &now
			task.Logs = append(task.Logs, LogEntry{At: now, Message: "程序重启，未完成任务已标记为停止。"})
		}
		m.tasks[task.ID] = &task
		m.save(&task)
	}
}

func (t *Task) Done() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Status == StatusSucceeded || t.Status == StatusFailed || t.Status == StatusCanceled
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}
