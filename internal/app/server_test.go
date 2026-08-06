package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modeltraining-go-ts/internal/dataset"
	"modeltraining-go-ts/internal/training"
)

func TestProjectPreflightReportsTrainingBlockers(t *testing.T) {
	server := newTestServer(t)
	root := t.TempDir()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "train",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "yolo26n.pt",
		"device": "cpu",
		"imageSize": 640
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("expected report not ready: %+v", report)
	}
	if !hasCheck(report, "images", "error") {
		t.Fatalf("expected training images error: %+v", report.Checks)
	}
	if !hasCheck(report, "classes", "error") {
		t.Fatalf("expected training classes error: %+v", report.Checks)
	}
}

func TestProjectPreflightReportsPredictionBlockers(t *testing.T) {
	server := newTestServer(t)
	root := t.TempDir()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "predict",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "missing-model.pt",
		"imagePath": "missing-image.jpg",
		"device": "cpu",
		"imageSize": 640,
		"confidence": 0.25
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("expected report not ready: %+v", report)
	}
	if !hasCheck(report, "model", "error") {
		t.Fatalf("expected model error: %+v", report.Checks)
	}
	if !hasCheck(report, "predict-image", "error") {
		t.Fatalf("expected predict image error: %+v", report.Checks)
	}
}

func TestProjectPreflightBlocksMissingTrainingModel(t *testing.T) {
	server := newTestServer(t)
	root := t.TempDir()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "train",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "missing-model.pt",
		"device": "cpu",
		"imageSize": 640
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("expected report not ready: %+v", report)
	}
	if !hasCheck(report, "model", "error") {
		t.Fatalf("expected training model error: %+v", report.Checks)
	}
}

func TestProjectPreflightBlocksMissingYOLOCommand(t *testing.T) {
	server := newTestServer(t)
	server.training = training.Runner{YOLOCommand: "definitely-missing-yolo-command"}
	root := t.TempDir()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "train",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "yolo26n.pt",
		"device": "cpu",
		"imageSize": 640
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("expected report not ready: %+v", report)
	}
	if !hasCheck(report, "yolo-command", "error") {
		t.Fatalf("expected YOLO command error: %+v", report.Checks)
	}
	commandCheck := findCheck(report, "yolo-command")
	if commandCheck == nil || !strings.Contains(commandCheck.Hint, "重新解压或安装完整程序") {
		t.Fatalf("unexpected release hint: %+v", commandCheck)
	}
}

func TestProjectPreflightResolvesModelLikeRunner(t *testing.T) {
	baseDir := t.TempDir()
	modelDir := filepath.Join(baseDir, "third_party", "ultralytics-8.4.10")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundledModel := filepath.Join(modelDir, dataset.DefaultYOLO26WeightName)
	if err := os.WriteFile(bundledModel, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	server.training = training.Runner{
		YOLOCommand: "go",
		ModelDirs:   []string{modelDir},
	}
	server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
		return training.RuntimeProbeReport{OK: true, Command: "go", CheckedAt: time.Now()}
	}
	root := t.TempDir()
	imagePath := filepath.Join(root, "predict.jpg")
	if err := os.WriteFile(imagePath, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "predict",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "`+dataset.DefaultYOLO26WeightName+`",
		"imagePath": "`+strings.ReplaceAll(imagePath, `\`, `\\`)+`",
		"device": "cpu",
		"imageSize": 640,
		"confidence": 0.25
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	modelCheck := findCheck(report, "model")
	if modelCheck == nil || modelCheck.Level != "ok" {
		t.Fatalf("expected model ok check: %+v", report.Checks)
	}
	if modelCheck.Message != bundledModel {
		t.Fatalf("model check = %q, want bundled model %q", modelCheck.Message, bundledModel)
	}
	if !filepath.IsAbs(modelCheck.Message) {
		t.Fatalf("model check should be absolute, got %q", modelCheck.Message)
	}
}

func TestProjectPreflightBlocksBrokenYOLORuntime(t *testing.T) {
	server := newTestServer(t)
	server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
		return training.RuntimeProbeReport{
			OK:        false,
			Command:   server.training.CommandDisplay(),
			Error:     "torch import failed",
			CheckedAt: time.Now(),
		}
	}
	root := t.TempDir()
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ImagesDir, "sample.jpg"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dataset.SaveClasses(root, []string{"target"}); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(root, "model.pt")
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "train",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "`+strings.ReplaceAll(modelPath, `\`, `\\`)+`",
		"device": "cpu",
		"imageSize": 640
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("expected report not ready: %+v", report)
	}
	if !hasCheck(report, "yolo-runtime", "error") {
		t.Fatalf("expected YOLO runtime error: %+v", report.Checks)
	}
}

func TestProjectPreflightBlocksCUDAWhenBundledTorchIsCPUOnly(t *testing.T) {
	server := newTestServer(t)
	server.training = training.Runner{YOLOCommand: "go", PythonCommand: "go"}
	server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
		return training.RuntimeProbeReport{
			OK:                 true,
			Command:            "go",
			PythonCommand:      "go",
			UltralyticsVersion: "8.test",
			TorchVersion:       "2.test+cpu",
			CUDAAvailable:      false,
			CheckedAt:          time.Now(),
		}
	}
	server.probeCache = nil
	root := t.TempDir()
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ImagesDir, "sample.jpg"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dataset.SaveClasses(root, []string{"target"}); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(root, "model.pt")
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "train",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "`+strings.ReplaceAll(modelPath, `\`, `\\`)+`",
		"device": "0",
		"imageSize": 640
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("expected report not ready: %+v", report)
	}
	if !hasCheck(report, "device", "error") {
		t.Fatalf("expected CUDA device error: %+v", report.Checks)
	}
}

func TestProjectPreflightBlocksExplicitCUDAWhenTensorSmokeFails(t *testing.T) {
	server := newTestServer(t)
	server.training = training.Runner{YOLOCommand: "go", PythonCommand: "go"}
	server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
		return training.RuntimeProbeReport{
			OK:                 true,
			Command:            "go",
			PythonCommand:      "go",
			UltralyticsVersion: "8.test",
			TorchVersion:       "2.test+cu126",
			TorchCUDA:          "12.6",
			CUDAReported:       true,
			CUDAAvailable:      false,
			CUDASmokeError:     "RuntimeError: CUDA error: no kernel image is available for execution on the device",
			CUDADeviceCount:    1,
			CUDADeviceName:     "Test RTX 50 GPU",
			CheckedAt:          time.Now(),
		}
	}
	server.probeCache = nil
	root, modelPath := createPreflightTrainingProject(t)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(fmt.Sprintf(
		`{"mode":"train","rootPath":%q,"modelPath":%q,"device":"cuda:0","imageSize":640}`,
		root, modelPath,
	)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("explicit CUDA should be blocked after smoke failure: %+v", report)
	}
	deviceCheck := findCheck(report, "device")
	if deviceCheck == nil || deviceCheck.Level != "error" || !strings.Contains(deviceCheck.Message, "no kernel image") {
		t.Fatalf("device check = %+v", deviceCheck)
	}
	runtimeCheck := findCheck(report, "yolo-runtime")
	if runtimeCheck == nil || !strings.Contains(runtimeCheck.Message, "张量测试失败") || !strings.Contains(runtimeCheck.Hint, "no kernel image") {
		t.Fatalf("runtime check = %+v", runtimeCheck)
	}
}

func TestProjectPreflightAutoFallsBackToCPUWhenTensorSmokeFails(t *testing.T) {
	server := newTestServer(t)
	server.training = training.Runner{YOLOCommand: "go", PythonCommand: "go"}
	server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
		return training.RuntimeProbeReport{
			OK:                 true,
			Command:            "go",
			PythonCommand:      "go",
			UltralyticsVersion: "8.test",
			TorchVersion:       "2.test+cu126",
			TorchCUDA:          "12.6",
			CUDAReported:       true,
			CUDAAvailable:      false,
			CUDASmokeError:     "no kernel image",
			CUDADeviceCount:    1,
			CUDADeviceName:     "Test RTX 50 GPU",
			CheckedAt:          time.Now(),
		}
	}
	server.probeCache = nil
	root, modelPath := createPreflightTrainingProject(t)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(fmt.Sprintf(
		`{"mode":"train","rootPath":%q,"modelPath":%q,"device":"auto","imageSize":640}`,
		root, modelPath,
	)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	deviceCheck := findCheck(report, "device")
	if !report.Ready || deviceCheck == nil || deviceCheck.Level != "ok" || !strings.Contains(deviceCheck.Message, "CPU") || !strings.Contains(deviceCheck.Hint, "no kernel image") {
		t.Fatalf("automatic fallback report = %+v", report)
	}
}

func TestWebcamScriptResolvesDeviceWithTensorSmoke(t *testing.T) {
	script := webcamPythonScript()
	for _, expected := range []string{"def mt_cuda_tensor_smoke", "torch.cuda.synchronize(device)", "device = mt_resolve_device(device)"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("webcam device resolver does not contain %q", expected)
		}
	}
}

func TestAutoAnnotateBlocksExplicitCUDAWhenTensorSmokeFails(t *testing.T) {
	server := newTestServer(t)
	server.training = training.Runner{YOLOCommand: "go", PythonCommand: "go"}
	server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
		return training.RuntimeProbeReport{
			OK:                 true,
			Command:            "go",
			PythonCommand:      "go",
			UltralyticsVersion: "8.test",
			TorchVersion:       "2.test+cu126",
			TorchCUDA:          "12.6",
			CUDAReported:       true,
			CUDAAvailable:      false,
			CUDASmokeError:     "no kernel image",
			CheckedAt:          time.Now(),
		}
	}
	server.probeCache = nil
	root, modelPath := createPreflightTrainingProject(t)
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/auto-annotate", strings.NewReader(fmt.Sprintf(
		`{"projectRoot":%q,"modelPath":%q,"device":"cuda:0","imageSize":640,"confidence":0.25,"iou":0.7}`, root, modelPath,
	)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no kernel image") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProjectPreflightAutomaticDeviceAllowsGPUAndCPUFallback(t *testing.T) {
	for _, cudaAvailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("cuda-%v", cudaAvailable), func(t *testing.T) {
			server := newTestServer(t)
			server.training = training.Runner{YOLOCommand: "go", PythonCommand: "go"}
			server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
				return training.RuntimeProbeReport{
					OK:                 true,
					Command:            "go",
					PythonCommand:      "go",
					UltralyticsVersion: "8.test",
					TorchVersion:       "2.test+cu126",
					TorchCUDA:          "12.6",
					CUDAAvailable:      cudaAvailable,
					CUDADeviceName:     "Test NVIDIA GPU",
					CheckedAt:          time.Now(),
				}
			}
			root := t.TempDir()
			paths := dataset.Paths(root)
			if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.ImagesDir, "sample.jpg"), []byte("image"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := dataset.SaveClasses(root, []string{"target"}); err != nil {
				t.Fatal(err)
			}
			modelPath := filepath.Join(root, "model.pt")
			if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"mode":"train","rootPath":%q,"modelPath":%q,"device":"auto","imageSize":640}`, root, modelPath)
			req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			server.Routes().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var report dataset.PreflightReport
			if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if !report.Ready || !hasCheck(report, "device", "ok") {
				t.Fatalf("automatic device report = %+v", report)
			}
		})
	}
}

func TestDeviceRequestsCUDA(t *testing.T) {
	tests := map[string]bool{
		"cpu":    false,
		"mps":    false,
		"auto":   false,
		"":       false,
		"0":      true,
		"0,1":    true,
		"[0, 1]": true,
		"cuda":   true,
		"cuda:0": true,
	}
	for device, want := range tests {
		if got := deviceRequestsCUDA(device); got != want {
			t.Errorf("deviceRequestsCUDA(%q) = %v, want %v", device, got, want)
		}
	}
}

func TestProjectPreflightWarnsMirroredValidationSplit(t *testing.T) {
	server := newTestServer(t)
	server.training = training.Runner{YOLOCommand: "go"}
	root := t.TempDir()
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		if err := os.WriteFile(filepath.Join(paths.ImagesDir, name), []byte("image"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := dataset.SaveClasses(root, []string{"target"}); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(root, "model.pt")
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", strings.NewReader(`{
		"mode": "train",
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"modelPath": "`+strings.ReplaceAll(modelPath, `\`, `\\`)+`",
		"device": "cpu",
		"imageSize": 640
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !hasCheck(report, "validation-split", "warning") {
		t.Fatalf("expected mirrored validation warning: %+v", report.Checks)
	}
}

func TestProjectPreflightBlocksMixedAndOutOfRangeSegmentationLabels(t *testing.T) {
	server := newTestServer(t)
	root, _ := createPreflightTrainingProject(t)
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.LabelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(root, "yolo26l-seg.pt")
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	label := "0 0.5 0.5 0.2 0.2\n0 0.1 0.1 1.2 0.1 0.5 0.5\n"
	if err := os.WriteFile(filepath.Join(paths.LabelsDir, "sample.txt"), []byte(label), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"mode": "train", "rootPath": root, "modelPath": modelPath, "device": "cpu", "imageSize": 640,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/preflight", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	var report dataset.PreflightReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Ready || !hasCheck(report, "label-content", "error") || !hasCheck(report, "label-task", "error") {
		t.Fatalf("expected label blockers: %+v", report.Checks)
	}
}

func TestExportDiagnosticsWritesReport(t *testing.T) {
	t.Setenv("MT_STATE_DIR", t.TempDir())
	server := newTestServer(t)
	root := t.TempDir()
	req := httptest.NewRequest(http.MethodPost, "/api/diagnostics/export", strings.NewReader(`{
		"rootPath": "`+strings.ReplaceAll(root, `\`, `\\`)+`",
		"taskIds": []
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Path == "" {
		t.Fatal("diagnostics path is empty")
	}
	var report map[string]any
	data, err := os.ReadFile(payload.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"runtime", "resources", "config", "trainPreflight"} {
		if _, ok := report[key]; !ok {
			t.Fatalf("missing %q in diagnostics report", key)
		}
	}
	resources, ok := report["resources"].(map[string]any)
	if !ok {
		t.Fatalf("resources has unexpected type: %T", report["resources"])
	}
	if _, ok := resources["yoloRuntime"]; !ok {
		t.Fatalf("missing yoloRuntime in diagnostics resources: %+v", resources)
	}
	if _, ok := resources["pythonCommand"]; !ok {
		t.Fatalf("missing pythonCommand in diagnostics resources: %+v", resources)
	}
}

func TestUploadImagesSkipsExistingTarget(t *testing.T) {
	server := newTestServer(t)
	root := t.TempDir()
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(paths.ImagesDir, "sample.jpg")
	if err := os.WriteFile(destPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("rootPath", root); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("images", "sample.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/images/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Imported int      `json:"imported"`
		Skipped  []string `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Imported != 0 {
		t.Fatalf("imported = %d, want 0", payload.Imported)
	}
	if len(payload.Skipped) != 1 {
		t.Fatalf("skipped = %#v, want one item", payload.Skipped)
	}
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("existing image was overwritten: %q", string(data))
	}
}

func TestPredictUploadCleansSavedImageWhenPreflightFails(t *testing.T) {
	server := newTestServer(t)
	server.training = training.Runner{YOLOCommand: "go"}
	root := t.TempDir()
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.PredictDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("rootPath", root); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("modelPath", filepath.Join(root, "missing-model.pt")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("device", "cpu"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("imageSize", "640"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("confidence", "0.25"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("image", "sample.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("image")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/predict-image", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(paths.PredictDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".jpg") {
			t.Fatalf("orphan predict upload was not removed: %s", entry.Name())
		}
	}
}

func TestPutConfigAcceptsButDropsLegacyRuntimeSelection(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("MT_STATE_DIR", stateDir)
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{
		"version": 2,
		"epochs": 1,
		"device": "auto",
		"pythonRuntime": "conda",
		"condaEnv": "host-vision"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfg dataset.TrainingConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.PythonRuntime != "" || cfg.CondaEnv != "" {
		t.Fatalf("response retained legacy runtime selection: %+v", cfg)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "training_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "pythonRuntime") || strings.Contains(string(data), "condaEnv") || strings.Contains(string(data), "host-vision") {
		t.Fatalf("persisted config contains legacy runtime selection: %s", data)
	}
}

func TestCondaEnvsEndpointNeverEnumeratesHostEnvironments(t *testing.T) {
	t.Setenv("PATH", "")
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/conda/envs", nil)
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envs []any
	if err := json.Unmarshal(rec.Body.Bytes(), &envs); err != nil {
		t.Fatal(err)
	}
	if len(envs) != 0 {
		t.Fatalf("host environments leaked through API: %#v", envs)
	}
}

func TestEnsureLabelMeConfigDisablesAIDefault(t *testing.T) {
	t.Setenv("MT_STATE_DIR", t.TempDir())
	server := newTestServer(t)
	path, err := server.ensureLabelMeConfig()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ai:\n  default: null\n" {
		t.Fatalf("LabelMe config = %q", data)
	}
}

func hasCheck(report dataset.PreflightReport, key, level string) bool {
	for _, check := range report.Checks {
		if check.Key == key && check.Level == level {
			return true
		}
	}
	return false
}

func createPreflightTrainingProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ImagesDir, "sample.jpg"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dataset.SaveClasses(root, []string{"target"}); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(root, "model.pt")
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, modelPath
}

func findCheck(report dataset.PreflightReport, key string) *dataset.PreflightCheck {
	for i := range report.Checks {
		if report.Checks[i].Key == key {
			return &report.Checks[i]
		}
	}
	return nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(".")
	if err != nil {
		t.Fatal(err)
	}
	server.probeYOLO = func(context.Context) training.RuntimeProbeReport {
		return training.RuntimeProbeReport{
			OK:                 true,
			Command:            "yolo",
			PythonCommand:      "python",
			UltralyticsVersion: "8.test",
			TorchVersion:       "2.test",
			CheckedAt:          time.Now(),
		}
	}
	server.probeCache = nil
	return server
}
