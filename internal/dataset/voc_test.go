package dataset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertVOCXMLToYOLO(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	for _, dir := range []string{paths.XMLDir, paths.LabelsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.ClassesFile, []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	annotation := `<annotation>
  <filename>sample.jpg</filename>
  <size>
    <width>200</width>
    <height>100</height>
  </size>
  <object>
    <name>target</name>
    <bndbox>
      <xmin>50</xmin>
      <ymin>20</ymin>
      <xmax>150</xmax>
      <ymax>80</ymax>
    </bndbox>
  </object>
  <object>
    <name>ignored</name>
    <bndbox>
      <xmin>1</xmin>
      <ymin>1</ymin>
      <xmax>2</xmax>
      <ymax>2</ymax>
    </bndbox>
  </object>
</annotation>`
	if err := os.WriteFile(filepath.Join(paths.XMLDir, "sample.xml"), []byte(annotation), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ConvertAnnotationsToYOLO(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.XMLFiles != 1 || report.ConvertedFiles != 1 || report.Boxes != 1 || report.SkippedObjects != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(paths.LabelsDir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	want := "0 0.500000 0.500000 0.500000 0.600000"
	if got != want {
		t.Fatalf("label mismatch\ngot  %q\nwant %q", got, want)
	}
}
