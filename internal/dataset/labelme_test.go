package dataset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertLabelMeJSONToYOLO(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	for _, dir := range []string{paths.LabelMeDir, paths.LabelsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.ClassesFile, []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	annotation := `{
  "imagePath": "sample.jpg",
  "imageWidth": 100,
  "imageHeight": 50,
  "shapes": [
    {"label": "target", "points": [[10, 5], [30, 25]], "shape_type": "rectangle"},
    {"label": "ignored", "points": [[1, 1], [2, 2]], "shape_type": "rectangle"}
  ]
}`
	if err := os.WriteFile(filepath.Join(paths.LabelMeDir, "sample.json"), []byte(annotation), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ConvertAnnotationsToYOLO(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.JSONFiles != 1 || report.ConvertedFiles != 1 || report.Boxes != 1 || report.SkippedObjects != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(paths.LabelsDir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	want := "0 0.200000 0.300000 0.200000 0.400000"
	if got != want {
		t.Fatalf("label mismatch\ngot  %q\nwant %q", got, want)
	}
}
