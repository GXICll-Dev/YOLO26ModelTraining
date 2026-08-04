package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultTrainingConfigUsesPackagedModel(t *testing.T) {
	modelPath := filepath.Join(t.TempDir(), DefaultYOLO26WeightName)
	t.Setenv("YOLO_MODEL_PATH", modelPath)

	cfg := DefaultTrainingConfig()
	if cfg.Model != modelPath {
		t.Fatalf("model = %q, want %q", cfg.Model, modelPath)
	}
}

func TestDefaultTrainingConfigUsesAutomaticDevice(t *testing.T) {
	cfg := DefaultTrainingConfig()
	if cfg.Version != CurrentTrainingConfigVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentTrainingConfigVersion)
	}
	if cfg.Device != "auto" {
		t.Fatalf("device = %q, want auto", cfg.Device)
	}
}

func TestDefaultYOLO26ModelCandidatesDoNotContainLegacyMachinePath(t *testing.T) {
	t.Setenv("YOLO_MODEL_DIR", "")
	t.Setenv("ULTRALYTICS_DIR", "")
	t.Chdir(t.TempDir())

	legacy := strings.ToLower(filepath.Clean(`D:\ultralytics-8.4.10\` + DefaultYOLO26WeightName))
	for _, candidate := range defaultYOLO26ModelCandidates() {
		if strings.ToLower(filepath.Clean(candidate)) == legacy {
			t.Fatalf("legacy machine path remains an implicit model candidate: %s", candidate)
		}
	}
}

func TestLoadTrainingConfigMigratesLegacyCPUDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "training_config.json")
	if err := os.WriteFile(path, []byte(`{"epochs":50,"device":"cpu"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadTrainingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != CurrentTrainingConfigVersion || cfg.Device != "auto" {
		t.Fatalf("migrated config = %+v", cfg)
	}
}

func TestLoadTrainingConfigPreservesVersionedExplicitCPU(t *testing.T) {
	path := filepath.Join(t.TempDir(), "training_config.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"epochs":50,"device":"cpu"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadTrainingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Device != "cpu" {
		t.Fatalf("device = %q, want cpu", cfg.Device)
	}
}

func TestLoadTrainingConfigMigratesEmptyModelToPackagedModel(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, DefaultYOLO26WeightName)
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_MODEL_PATH", modelPath)
	configPath := filepath.Join(dir, "training_config.json")
	if err := os.WriteFile(configPath, []byte(`{"epochs": 50, "model": ""}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadTrainingConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != modelPath {
		t.Fatalf("model = %q, want %q", cfg.Model, modelPath)
	}
}

func TestLoadTrainingConfigDropsLegacyRuntimeSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "training_config.json")
	if err := os.WriteFile(path, []byte(`{
		"version": 2,
		"epochs": 50,
		"device": "auto",
		"pythonRuntime": "conda",
		"condaEnv": "host-vision"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadTrainingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PythonRuntime != "" || cfg.CondaEnv != "" {
		t.Fatalf("legacy runtime selection survived load: %+v", cfg)
	}
}

func TestSaveTrainingConfigOmitsLegacyRuntimeSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "training_config.json")
	cfg := DefaultTrainingConfig()
	cfg.PythonRuntime = "conda"
	cfg.CondaEnv = "host-vision"
	if err := SaveTrainingConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "pythonRuntime") || strings.Contains(text, "condaEnv") || strings.Contains(text, "host-vision") {
		t.Fatalf("saved config contains legacy runtime selection: %s", text)
	}
}

func TestImportImagesSkipsExistingTarget(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(paths.ImagesDir, "sample.jpg")
	if err := os.WriteFile(destPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "sample.jpg")
	if err := os.WriteFile(sourcePath, []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}

	imported, skipped, err := ImportImages(root, []string{sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	if imported != 0 {
		t.Fatalf("imported = %d, want 0", imported)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want one item", skipped)
	}
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("existing image was overwritten: %q", string(data))
	}
}

func TestPrepareDatasetSplitMirrorsSmallDataset(t *testing.T) {
	root := t.TempDir()
	createTrainingImages(t, root, 3)

	split, err := PrepareDatasetSplit(root)
	if err != nil {
		t.Fatal(err)
	}
	if !split.Mirrored {
		t.Fatalf("mirrored = false, want true")
	}
	if split.TrainImages != 3 || split.ValImages != 3 || split.TotalImages != 3 {
		t.Fatalf("split = %+v, want 3 train / 3 val / 3 total", split)
	}
	trainLines := readLines(t, split.TrainListFile)
	valLines := readLines(t, split.ValListFile)
	if strings.Join(trainLines, "\n") != strings.Join(valLines, "\n") {
		t.Fatalf("small dataset should mirror train and val lists:\ntrain=%v\nval=%v", trainLines, valLines)
	}
	for _, line := range trainLines {
		if strings.Contains(line, `\`) {
			t.Fatalf("list path contains Windows separator: %q", line)
		}
	}
}

func TestPrepareDatasetSplitCreatesSeparateValidationSet(t *testing.T) {
	root := t.TempDir()
	createTrainingImages(t, root, 10)

	split, err := PrepareDatasetSplit(root)
	if err != nil {
		t.Fatal(err)
	}
	if split.Mirrored {
		t.Fatalf("mirrored = true, want false")
	}
	if split.TrainImages != 8 || split.ValImages != 2 || split.TotalImages != 10 {
		t.Fatalf("split = %+v, want 8 train / 2 val / 10 total", split)
	}
	seen := map[string]bool{}
	for _, line := range readLines(t, split.TrainListFile) {
		seen[line] = true
	}
	for _, line := range readLines(t, split.ValListFile) {
		if seen[line] {
			t.Fatalf("validation image also appears in training list: %s", line)
		}
	}
}

func TestWriteDatasetYAMLUsesSplitListFiles(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	createTrainingImages(t, root, 5)

	if err := WriteDatasetYAML(root, []string{"target"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.YamlFile)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	if !strings.Contains(yaml, "train: "+yamlPath(paths.TrainListFile)) {
		t.Fatalf("dataset yaml does not reference train list:\n%s", yaml)
	}
	if !strings.Contains(yaml, "val: "+yamlPath(paths.ValListFile)) {
		t.Fatalf("dataset yaml does not reference val list:\n%s", yaml)
	}
	if strings.Contains(yaml, "val: images/train") {
		t.Fatalf("dataset yaml still reuses images/train as val:\n%s", yaml)
	}
	if len(readLines(t, paths.TrainListFile)) == 0 || len(readLines(t, paths.ValListFile)) == 0 {
		t.Fatalf("split list files were not written")
	}
}

func TestTrainingResultsUsesSavedManifestSnapshot(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	runDir := filepath.Join(paths.RunsDir, "train")
	weightsDir := filepath.Join(runDir, "weights")
	if err := os.MkdirAll(weightsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(weightsDir, "best.pt")
	if err := os.WriteFile(modelPath, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ImagesDir, "current.jpg"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveClasses(root, []string{"current"}); err != nil {
		t.Fatal(err)
	}

	manifest := TrainingManifest{
		Version:            1,
		RunDir:             runDir,
		ModelPath:          modelPath,
		ResolvedModel:      "yolo26n.pt",
		Config:             DefaultTrainingConfig(),
		Classes:            []string{"old-a", "old-b"},
		ImageCount:         42,
		ClassCount:         2,
		LabelCount:         40,
		TrainingImages:     34,
		ValidationImages:   8,
		ValidationMirrored: false,
		Command:            "yolo detect train",
		CommandArgs:        []string{"detect", "train"},
		YOLOCommand:        "yolo",
		StartedAt:          time.Now().Add(-time.Minute),
		FinishedAt:         time.Now(),
		Status:             "succeeded",
	}
	if err := SaveTrainingManifest(manifest); err != nil {
		t.Fatal(err)
	}

	results, err := TrainingResults(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	result := results[0]
	if result.Manifest == nil {
		t.Fatal("manifest was not loaded")
	}
	if result.ImageCount != 42 {
		t.Fatalf("image count = %d, want manifest count 42", result.ImageCount)
	}
	if result.ClassCount != 2 {
		t.Fatalf("class count = %d, want manifest count 2", result.ClassCount)
	}
	if result.Manifest.LabelCount != 40 {
		t.Fatalf("label count = %d, want 40", result.Manifest.LabelCount)
	}
	if result.Manifest.TrainingImages != 34 || result.Manifest.ValidationImages != 8 || result.Manifest.ValidationMirrored {
		t.Fatalf("manifest split = %+v, want 34 train / 8 val / not mirrored", result.Manifest)
	}
}

func TestLatestResultPrefersNewestUsableModel(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	oldRun := filepath.Join(paths.RunsDir, "train-old")
	oldWeights := filepath.Join(oldRun, "weights")
	if err := os.MkdirAll(oldWeights, 0o755); err != nil {
		t.Fatal(err)
	}
	oldModel := filepath.Join(oldWeights, "best.pt")
	if err := os.WriteFile(oldModel, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	newRun := filepath.Join(paths.RunsDir, "train-new-failed")
	if err := os.MkdirAll(newRun, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveTrainingManifest(TrainingManifest{
		Version:    1,
		RunDir:     newRun,
		Config:     DefaultTrainingConfig(),
		Classes:    []string{"target"},
		ImageCount: 3,
		ClassCount: 1,
		Status:     "failed",
		Error:      "training failed before weights were saved",
	}); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(oldRun, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newRun, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	results, err := TrainingResults(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].RunDir != newRun {
		t.Fatalf("newest run should remain first in history, got %s", results[0].RunDir)
	}
	latest, err := LatestResult(root)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ModelPath != oldModel {
		t.Fatalf("latest usable model = %#v, want %s", latest, oldModel)
	}
	state, err := State(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.LatestRun == nil || state.LatestRun.ModelPath != oldModel {
		t.Fatalf("state latest run = %#v, want usable model %s", state.LatestRun, oldModel)
	}
}

func TestLatestResultIgnoresMissingManifestModelPath(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	oldRun := filepath.Join(paths.RunsDir, "train-old")
	oldWeights := filepath.Join(oldRun, "weights")
	if err := os.MkdirAll(oldWeights, 0o755); err != nil {
		t.Fatal(err)
	}
	oldModel := filepath.Join(oldWeights, "best.pt")
	if err := os.WriteFile(oldModel, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}

	newRun := filepath.Join(paths.RunsDir, "train-new")
	if err := os.MkdirAll(newRun, 0o755); err != nil {
		t.Fatal(err)
	}
	staleModel := filepath.Join(newRun, "weights", "best.pt")
	if err := SaveTrainingManifest(TrainingManifest{
		Version:    1,
		RunDir:     newRun,
		ModelPath:  staleModel,
		Config:     DefaultTrainingConfig(),
		Classes:    []string{"target"},
		ImageCount: 8,
		ClassCount: 1,
		Status:     "succeeded",
		FinishedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(oldRun, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newRun, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	results, err := TrainingResults(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].RunDir != newRun {
		t.Fatalf("newest run should remain first in history, got %s", results[0].RunDir)
	}
	if results[0].ModelPath != "" {
		t.Fatalf("stale manifest model path surfaced as usable: %s", results[0].ModelPath)
	}

	latest, err := LatestResult(root)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ModelPath != oldModel {
		t.Fatalf("latest usable model = %#v, want %s", latest, oldModel)
	}
}

func TestPredictionResultsFiltersStaleFiles(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	outputDir := filepath.Join(paths.PredictDir, "predict-stale")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	freshArtifact := filepath.Join(outputDir, "fresh.jpg")
	if err := os.WriteFile(freshArtifact, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleImage := filepath.Join(outputDir, "missing.jpg")
	staleSource := filepath.Join(paths.PredictDir, "deleted-source.jpg")
	result := PredictionResult{
		RunID:       "predict-stale",
		ModelPath:   filepath.Join(root, "model.pt"),
		SourceImage: staleSource,
		OutputDir:   filepath.Join(root, "old-output"),
		OutputImage: staleImage,
		Artifacts:   []string{staleImage},
		RanAt:       time.Now(),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "prediction-result.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := PredictionResults(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.OutputDir != outputDir {
		t.Fatalf("output dir = %s, want scanned dir %s", got.OutputDir, outputDir)
	}
	if got.SourceImage != "" {
		t.Fatalf("stale source image should be cleared, got %s", got.SourceImage)
	}
	if got.OutputImage != freshArtifact {
		t.Fatalf("output image = %s, want fallback artifact %s", got.OutputImage, freshArtifact)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0] != freshArtifact {
		t.Fatalf("artifacts = %#v, want only %s", got.Artifacts, freshArtifact)
	}
}

func TestPredictionResultsDropsStaleDetections(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	outputDir := filepath.Join(paths.PredictDir, "predict-detections")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleLabel := filepath.Join(outputDir, "labels", "deleted.txt")
	result := PredictionResult{
		RunID:     "predict-detections",
		OutputDir: outputDir,
		Detections: []PredictionDetection{
			{
				ClassID:    0,
				ClassName:  "old-target",
				Confidence: 0.9,
				LabelPath:  staleLabel,
			},
		},
		DetectionCount: 1,
		RanAt:          time.Now(),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "prediction-result.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := PredictionResults(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if len(results[0].Detections) != 0 {
		t.Fatalf("detections = %#v, want stale detection removed", results[0].Detections)
	}
	if results[0].DetectionCount != 0 {
		t.Fatalf("detection count = %d, want 0", results[0].DetectionCount)
	}
}

func TestPredictionResultsRebuildsDetectionsAfterStaleSnapshot(t *testing.T) {
	root := t.TempDir()
	paths := Paths(root)
	outputDir := filepath.Join(paths.PredictDir, "predict-rebuild")
	labelDir := filepath.Join(outputDir, "labels")
	if err := os.MkdirAll(labelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveClasses(root, []string{"fresh-target"}); err != nil {
		t.Fatal(err)
	}
	freshLabel := filepath.Join(labelDir, "fresh.txt")
	if err := os.WriteFile(freshLabel, []byte("0 0.5 0.5 0.2 0.2 0.88\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleLabel := filepath.Join(labelDir, "deleted.txt")
	result := PredictionResult{
		RunID:     "predict-rebuild",
		OutputDir: outputDir,
		Detections: []PredictionDetection{
			{
				ClassID:    0,
				ClassName:  "old-target",
				Confidence: 0.3,
				LabelPath:  staleLabel,
			},
		},
		DetectionCount: 1,
		RanAt:          time.Now(),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "prediction-result.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := PredictionResults(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].DetectionCount != 1 || len(results[0].Detections) != 1 {
		t.Fatalf("detections = %#v, count = %d; want one rebuilt detection", results[0].Detections, results[0].DetectionCount)
	}
	got := results[0].Detections[0]
	if got.LabelPath != freshLabel || got.ClassName != "fresh-target" || got.Confidence != 0.88 {
		t.Fatalf("rebuilt detection = %+v, want fresh label %s", got, freshLabel)
	}
}

func createTrainingImages(t *testing.T, root string, count int) {
	t.Helper()
	paths := Paths(root)
	if err := os.MkdirAll(paths.ImagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		name := filepath.Join(paths.ImagesDir, fmt.Sprintf("image-%02d.jpg", i))
		if err := os.WriteFile(name, []byte("image"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}
