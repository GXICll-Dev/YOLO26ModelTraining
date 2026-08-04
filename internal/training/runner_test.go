package training

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"modeltraining-go-ts/internal/dataset"
	"modeltraining-go-ts/internal/tasks"
)

func TestNewRunnerUsesModelTrainingPythonForYOLO(t *testing.T) {
	python := `D:\portable runtime\python.exe`
	t.Setenv("YOLO_CMD", "")
	t.Setenv("MT_PYTHON_CMD", `"`+python+`"`)
	t.Setenv("PYTHON_CMD", `D:\other\python.exe`)
	t.Setenv("ULTRALYTICS_DIR", t.TempDir())

	runner := NewRunner()
	if runner.PythonCommand != python {
		t.Fatalf("PythonCommand = %q, want %q", runner.PythonCommand, python)
	}
	if runner.YOLOCommand != python {
		t.Fatalf("YOLOCommand = %q, want %q", runner.YOLOCommand, python)
	}
	if !reflect.DeepEqual(runner.YOLOCommandArgs, []string{"-c", ultralyticsEntrypointScript}) {
		t.Fatalf("YOLOCommandArgs = %#v", runner.YOLOCommandArgs)
	}
	if runner.UltralyticsDir == "" {
		t.Fatal("expected configured Ultralytics directory")
	}
	if runner.IsolatedPython {
		t.Fatal("explicit ULTRALYTICS_DIR should keep source mode enabled")
	}
}

func TestNewRunnerWithConfiguredPythonIgnoresLegacyUltralyticsDirs(t *testing.T) {
	python := `D:\portable runtime\python.exe`
	legacySource := t.TempDir()
	t.Setenv("YOLO_CMD", "")
	t.Setenv("MT_PYTHON_CMD", python)
	t.Setenv("PYTHON_CMD", "")
	t.Setenv("ULTRALYTICS_DIR", "")
	t.Setenv("PYTHONPATH", legacySource)

	runner := NewRunner()
	if runner.UltralyticsDir != "" {
		t.Fatalf("UltralyticsDir = %q, want empty for packaged Python", runner.UltralyticsDir)
	}
	if !runner.IsolatedPython {
		t.Fatal("configured Python without ULTRALYTICS_DIR should be isolated")
	}
	if got := environmentValue(runner.commandEnv(), "PYTHONPATH"); got != "" {
		t.Fatalf("child PYTHONPATH = %q, want empty", got)
	}
	if runner.YOLOCommand != python {
		t.Fatalf("YOLOCommand = %q, want %q", runner.YOLOCommand, python)
	}
}

func TestNewRunnerIgnoresLegacyPythonCommandEnvironment(t *testing.T) {
	legacyPython := `D:\host-python\python.exe`
	t.Setenv("MT_PYTHON_CMD", "")
	t.Setenv("PYTHON_CMD", legacyPython)
	t.Setenv("YOLO_CMD", "")
	t.Setenv("ULTRALYTICS_DIR", "")
	t.Setenv("PATH", t.TempDir())

	runner := NewRunner()
	if runner.PythonCommand == legacyPython || runner.YOLOCommand == legacyPython {
		t.Fatalf("legacy PYTHON_CMD redirected runner: %+v", runner)
	}
}

func TestResolveUltralyticsDirSuppressesAutomaticSearchForConfiguredPython(t *testing.T) {
	legacySource := t.TempDir()
	if got := resolveUltralyticsDir("", true, legacySource); got != "" {
		t.Fatalf("resolved legacy source %q for configured Python", got)
	}
	if got := resolveUltralyticsDir(legacySource, true, t.TempDir()); got != legacySource {
		t.Fatalf("explicit source = %q, want %q", got, legacySource)
	}
}

func TestDefaultUltralyticsCandidatesDoNotContainLegacyMachinePath(t *testing.T) {
	t.Chdir(t.TempDir())
	legacy := strings.ToLower(filepath.Clean(`D:\ultralytics-8.4.10`))
	for _, candidate := range defaultUltralyticsDirCandidates() {
		if strings.ToLower(filepath.Clean(candidate)) == legacy {
			t.Fatalf("legacy machine path remains an implicit source candidate: %s", candidate)
		}
	}
}

func TestResolveYOLOCommandPrefersConfiguredCommand(t *testing.T) {
	cmd, args := resolveYOLOCommand("D:\\tools\\yolo.exe", "D:\\ultralytics-8.4.10", "D:\\runtime\\python.exe", fakeLookPath())
	if cmd != "D:\\tools\\yolo.exe" {
		t.Fatalf("cmd = %q", cmd)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolveYOLOCommandUsesSystemYolo(t *testing.T) {
	cmd, args := resolveYOLOCommand("", "D:\\ultralytics-8.4.10", "", fakeLookPath("yolo"))
	if cmd != "yolo" {
		t.Fatalf("cmd = %q", cmd)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolveYOLOCommandFallsBackToBundledSourceThroughPython(t *testing.T) {
	cmd, args := resolveYOLOCommand("", "D:\\ultralytics-8.4.10", "", fakeLookPath("python"))
	if cmd != "python" {
		t.Fatalf("cmd = %q", cmd)
	}
	if !reflect.DeepEqual(args, []string{"-c", ultralyticsEntrypointScript}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolveYOLOCommandPrefersConfiguredPythonOverSystemYolo(t *testing.T) {
	python := `D:\runtime with spaces\python.exe`
	cmd, args := resolveYOLOCommand("", "D:\\ultralytics-8.4.10", `"`+python+`"`, fakeLookPath("yolo"))
	if cmd != python {
		t.Fatalf("cmd = %q, want %q", cmd, python)
	}
	if !reflect.DeepEqual(args, []string{"-c", ultralyticsEntrypointScript}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolvePythonCommandUsesModelTrainingEnvironment(t *testing.T) {
	cmd := resolvePythonCommand(`"D:\\bundled python\\python.exe"`, fakeLookPath("python"))
	if cmd != `D:\\bundled python\\python.exe` {
		t.Fatalf("cmd = %q", cmd)
	}
}

func TestResolvePythonCommandFallsBackToPath(t *testing.T) {
	cmd := resolvePythonCommand("", fakeLookPath("python3"))
	if cmd != "python3" {
		t.Fatalf("cmd = %q", cmd)
	}
}

func TestParseProgressOnlyAcceptsConfiguredEpochTotal(t *testing.T) {
	if got := parseProgress("       1/50         0G", 50); got != 2 {
		t.Fatalf("epoch progress = %d, want 2", got)
	}
	for _, line := range []string{
		"Scanning images 3/3",
		"model summary 606/708",
		"51/50",
	} {
		if got := parseProgress(line, 50); got != -1 {
			t.Fatalf("parseProgress(%q) = %d, want -1", line, got)
		}
	}
}

func TestScanPipePreservesRawPythonLogsAndAddsSummary(t *testing.T) {
	task := tasks.NewManager().Create("train", t.TempDir())
	tail := newRecentLineBuffer(10)
	input := strings.NewReader("WARNING original warning\r       1/2         0G\rTraceback (most recent call last):\nRuntimeError: CUDA failed\n")

	var wg sync.WaitGroup
	wg.Add(1)
	go scanPipe(&wg, input, task, 2, "stderr", tail)
	wg.Wait()

	snapshot := task.Snapshot()
	messages := make([]string, 0, len(snapshot.Logs))
	for _, entry := range snapshot.Logs {
		messages = append(messages, entry.Message)
	}
	joined := strings.Join(messages, "\n")
	for _, expected := range []string{
		"[Python stderr] WARNING original warning",
		"[YOLO 摘要] YOLO 给出提醒",
		"[Python stderr]        1/2         0G",
		"[YOLO 摘要] 训练进度：第 1 / 2 轮",
		"[Python stderr] Traceback (most recent call last):",
		"[Python stderr] RuntimeError: CUDA failed",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("logs do not contain %q:\n%s", expected, joined)
		}
	}
	if snapshot.Progress != 50 {
		t.Fatalf("progress = %d, want 50", snapshot.Progress)
	}
	if !strings.Contains(tail.String(), "RuntimeError: CUDA failed") {
		t.Fatalf("stderr tail = %q", tail.String())
	}
}

func TestDetailedProcessErrorIncludesBoundedStderrTail(t *testing.T) {
	stderrTail := newRecentLineBuffer(2)
	stderrTail.Add("first")
	stderrTail.Add("second")
	stderrTail.Add("third")

	err := detailedProcessError("YOLO 训练失败", errors.New("exit status 1"), stderrTail, nil)
	message := err.Error()
	if strings.Contains(message, "first") || !strings.Contains(message, "second\nthird") {
		t.Fatalf("unexpected detailed error: %q", message)
	}
}

func TestAutomaticDeviceOmitsUltralyticsDeviceArgument(t *testing.T) {
	cfg := dataset.DefaultTrainingConfig()
	for _, device := range []string{"", "auto", " AUTO "} {
		cfg.Device = device
		trainArgs := buildYOLOArgs("data.yaml", "runs", "model.pt", cfg)
		if containsArgumentPrefix(trainArgs, "device=") {
			t.Fatalf("training args for %q contain device=: %#v", device, trainArgs)
		}
		predictArgs := buildYOLOPredictArgs("model.pt", "image.jpg", "predict", "run", dataset.PredictionConfig{Device: device, ImageSize: 640, Confidence: 0.25})
		if containsArgumentPrefix(predictArgs, "device=") {
			t.Fatalf("prediction args for %q contain device=: %#v", device, predictArgs)
		}
	}
}

func TestExplicitDeviceIsPreserved(t *testing.T) {
	cfg := dataset.DefaultTrainingConfig()
	for _, device := range []string{"cpu", "cuda:0"} {
		cfg.Device = device
		args := buildYOLOArgs("data.yaml", "runs", "model.pt", cfg)
		if !containsExactArgument(args, "device="+device) {
			t.Fatalf("training args for %q = %#v", device, args)
		}
	}
}

func TestRuntimeProbePayloadIgnoresUltralyticsStartupOutput(t *testing.T) {
	output := "Creating new Ultralytics Settings file\n" + runtimeProbePrefix + `{"ultralyticsVersion":"8.4.10","torchVersion":"2.7.0","cudaAvailable":false}` + "\n"
	payload, err := runtimeProbePayload(output)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ultralyticsVersion":"8.4.10","torchVersion":"2.7.0","cudaAvailable":false}`
	if payload != want {
		t.Fatalf("payload = %q, want %q", payload, want)
	}
}

func TestRuntimeProbeRequiresSuccessfulCUDATensorSmoke(t *testing.T) {
	for _, expected := range []string{
		"sample = torch.arange(16",
		"result = (sample * sample + 1.0).sum()",
		"torch.cuda.synchronize(device)",
		`"cudaReportedAvailable": cuda_reported`,
		`"cudaAvailable": cuda_available`,
		`"cudaSmokeError": cuda_smoke_error`,
	} {
		if !strings.Contains(runtimeProbeScript, expected) {
			t.Fatalf("runtime probe does not contain %q", expected)
		}
	}
	if !strings.Contains(runtimeProbeScript, "cuda_available = bool(cuda_reported and device_count and cuda_smoke_ok)") {
		t.Fatal("runtime probe still treats torch.cuda.is_available() as sufficient")
	}
}

func TestUltralyticsEntrypointResolvesAutoDeviceInChildProcess(t *testing.T) {
	for _, expected := range []string{
		"resolved_device = mt_resolve_device(requested_device)",
		`resolved_argument = "device=" + resolved_device`,
		"sys.argv.append(resolved_argument)",
		"sys.argv[device_arg_index] = resolved_argument",
	} {
		if !strings.Contains(ultralyticsEntrypointScript, expected) {
			t.Fatalf("Ultralytics entrypoint does not contain %q", expected)
		}
	}
}

func TestLocalizedDeviceResolutionLog(t *testing.T) {
	tests := map[string]string{
		"MT_DEVICE_RESOLVED 0":             "CUDA 0",
		"MT_DEVICE_RESOLVED cpu no kernel": "CPU",
		"MT_DEVICE_VALIDATED cuda:0":       "cuda:0",
	}
	for line, expected := range tests {
		if got := localizedDeviceResolutionLog(line); !strings.Contains(got, expected) {
			t.Errorf("localizedDeviceResolutionLog(%q) = %q, want substring %q", line, got, expected)
		}
	}
}

func TestLegacyRuntimeSelectionCannotRedirectTrainingCommand(t *testing.T) {
	runner := Runner{
		YOLOCommand:     `D:\\runtime\\python.exe`,
		YOLOCommandArgs: []string{"-c", ultralyticsEntrypointScript},
		PythonCommand:   `D:\\runtime\\python.exe`,
	}
	cfg := dataset.DefaultTrainingConfig()
	cfg.PythonRuntime = "conda"
	cfg.CondaEnv = "host-vision"
	cfg.ClearLegacyRuntimeSelection()

	got := runner.commandArgs(buildYOLOArgs("data.yaml", "runs", "model.pt", cfg))
	if cfg.PythonRuntime != "" || cfg.CondaEnv != "" {
		t.Fatalf("legacy runtime selection was not cleared: %+v", cfg)
	}
	if !reflect.DeepEqual(got[:4], []string{"-c", ultralyticsEntrypointScript, "detect", "train"}) {
		t.Fatalf("command args = %#v", got)
	}
	for _, arg := range got {
		if strings.Contains(strings.ToLower(arg), "conda") || arg == "host-vision" {
			t.Fatalf("host runtime leaked into command args: %#v", got)
		}
	}
}

func TestLegacyRuntimeSelectionCannotRedirectPredictionCommand(t *testing.T) {
	runner := Runner{
		YOLOCommand:     `D:\\runtime\\python.exe`,
		YOLOCommandArgs: []string{"-c", ultralyticsEntrypointScript},
		PythonCommand:   `D:\\runtime\\python.exe`,
	}
	cfg := dataset.PredictionConfig{
		PythonRuntime: "conda",
		CondaEnv:      "host-vision",
		ImageSize:     640,
		Confidence:    0.25,
	}
	cfg.ClearLegacyRuntimeSelection()

	got := runner.commandArgs(buildYOLOPredictArgs("model.pt", "image.jpg", "predict", "run", cfg))
	if cfg.PythonRuntime != "" || cfg.CondaEnv != "" {
		t.Fatalf("legacy runtime selection was not cleared: %+v", cfg)
	}
	if !reflect.DeepEqual(got[:4], []string{"-c", ultralyticsEntrypointScript, "detect", "predict"}) {
		t.Fatalf("command args = %#v", got)
	}
	for _, arg := range got {
		if strings.Contains(strings.ToLower(arg), "conda") || arg == "host-vision" {
			t.Fatalf("host runtime leaked into command args: %#v", got)
		}
	}
}

func fakeLookPath(found ...string) func(string) (string, error) {
	allowed := map[string]bool{}
	for _, name := range found {
		allowed[name] = true
	}
	return func(name string) (string, error) {
		if allowed[name] {
			return name, nil
		}
		return "", errors.New("not found")
	}
}

func environmentValue(env []string, key string) string {
	prefix := strings.ToUpper(key) + "="
	for _, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			return entry[len(prefix):]
		}
	}
	return ""
}

func containsArgumentPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func containsExactArgument(args []string, expected string) bool {
	for _, arg := range args {
		if arg == expected {
			return true
		}
	}
	return false
}
