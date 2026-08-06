package dataset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateLabelsAcceptsDetectionAndSegmentationAndRejectsOutOfRange(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.LabelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveClasses(root, []string{"target"}); err != nil {
		t.Fatal(err)
	}

	labels := map[string]string{
		"detect":  "0 0.5 0.5 0.2 0.2\n",
		"segment": "0 0.1 0.1 0.5 0.1 0.5 0.5\n",
		"invalid": "0 0.1 0.1 1.2 0.1 0.5 0.5\n",
	}
	for name, content := range labels {
		if err := os.WriteFile(filepath.Join(paths.ImagesDir, name+".jpg"), []byte("image"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(paths.LabelsDir, name+".txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := ValidateLabels(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.DetectionAnnotations != 1 || report.SegmentationAnnotations != 2 {
		t.Fatalf("annotation counts = detect %d segment %d", report.DetectionAnnotations, report.SegmentationAnnotations)
	}
	if report.ValidLabels != 2 {
		t.Fatalf("valid labels = %d, want 2", report.ValidLabels)
	}
	if _, ok := report.InvalidLabels["invalid.jpg"]; !ok {
		t.Fatalf("invalid label was not reported: %+v", report.InvalidLabels)
	}
}
