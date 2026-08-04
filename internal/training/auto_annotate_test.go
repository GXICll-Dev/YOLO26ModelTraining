package training

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modeltraining-go-ts/internal/dataset"
)

func TestResolveAutoAnnotateModelUsesPackagedDefault(t *testing.T) {
	modelPath := filepath.Join(t.TempDir(), dataset.DefaultYOLO26WeightName)
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_MODEL_PATH", modelPath)

	got, err := (Runner{}).resolveAutoAnnotateModel(t.TempDir(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != modelPath {
		t.Fatalf("model = %q, want %q", got, modelPath)
	}
}

func TestAutoAnnotateReportErrorFailsWhenEveryImageErrors(t *testing.T) {
	report := dataset.AutoAnnotateReport{
		ImageCount:     2,
		AnnotatedFiles: 0,
		Errors:         []string{"a.jpg: CUDA unavailable", "b.jpg: CUDA unavailable"},
	}
	err := autoAnnotateReportError(report)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "CUDA unavailable") {
		t.Fatalf("error = %q", err)
	}
}

func TestAutoAnnotateReportErrorAllowsPartialSuccess(t *testing.T) {
	report := dataset.AutoAnnotateReport{
		ImageCount:     2,
		AnnotatedFiles: 1,
		Errors:         []string{"b.jpg: unreadable"},
	}
	if err := autoAnnotateReportError(report); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAutoAnnotateScriptResolvesDeviceWithTensorSmoke(t *testing.T) {
	script := autoAnnotatePythonScript()
	for _, expected := range []string{
		"def mt_cuda_tensor_smoke",
		"torch.cuda.synchronize(device)",
		"device = mt_resolve_device(device)",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("auto-annotation device resolver does not contain %q", expected)
		}
	}
}

func TestAutoAnnotateScriptDoesNotOverwritePredictionIOU(t *testing.T) {
	script := autoAnnotatePythonScript()
	for _, expected := range []string{
		"prediction_iou = float(prediction_iou)",
		"iou=prediction_iou",
		"overlap_iou = inter /",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("script does not contain %q", expected)
		}
	}
	if strings.Contains(script, "\n                iou = inter /") {
		t.Fatal("deduplication still overwrites the prediction IOU threshold")
	}
}
