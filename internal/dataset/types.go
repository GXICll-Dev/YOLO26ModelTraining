package dataset

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultYOLO26WeightName = "yolo26n.pt"
const CurrentTrainingConfigVersion = 2

type TrainingConfig struct {
	Version     int    `json:"version,omitempty"`
	ProjectRoot string `json:"projectRoot,omitempty"`
	// Deprecated: retained only so older clients can submit their saved
	// configuration. The server clears both fields and always uses the Python
	// runtime selected when the backend starts (MT_PYTHON_CMD in releases).
	PythonRuntime string  `json:"pythonRuntime,omitempty"`
	CondaEnv      string  `json:"condaEnv,omitempty"`
	Epochs        int     `json:"epochs"`
	BatchSize     int     `json:"batchSize"`
	ImageSize     int     `json:"imageSize"`
	Device        string  `json:"device"`
	LearningRate  float64 `json:"learningRate"`
	Momentum      float64 `json:"momentum"`
	WeightDecay   float64 `json:"weightDecay"`
	Patience      int     `json:"patience"`
	Workers       int     `json:"workers"`
	FlipLR        float64 `json:"fliplr"`
	FlipUD        float64 `json:"flipud"`
	Mosaic        float64 `json:"mosaic"`
	Mixup         float64 `json:"mixup"`
	CopyPaste     float64 `json:"copyPaste"`
	Augment       bool    `json:"augment"`
	Model         string  `json:"model"`
}

// ClearLegacyRuntimeSelection prevents persisted or request-provided settings
// from redirecting training into a machine-wide Python or Conda environment.
func (c *TrainingConfig) ClearLegacyRuntimeSelection() {
	if c == nil {
		return
	}
	c.PythonRuntime = ""
	c.CondaEnv = ""
}

func DefaultTrainingConfig() TrainingConfig {
	return TrainingConfig{
		Version:      CurrentTrainingConfigVersion,
		Epochs:       50,
		BatchSize:    4,
		ImageSize:    640,
		Device:       "auto",
		LearningRate: 0.01,
		Momentum:     0.937,
		WeightDecay:  0.0005,
		Patience:     20,
		Workers:      2,
		FlipLR:       0.5,
		FlipUD:       0,
		Mosaic:       1,
		Mixup:        0,
		CopyPaste:    0,
		Augment:      true,
		Model:        DefaultYOLO26Model(),
	}
}

func DefaultYOLO26Model() string {
	if model := strings.TrimSpace(os.Getenv("YOLO_MODEL_PATH")); model != "" {
		return model
	}
	for _, candidate := range defaultYOLO26ModelCandidates() {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Size() > 0 {
			return candidate
		}
	}
	return DefaultYOLO26WeightName
}

func defaultYOLO26ModelCandidates() []string {
	var candidates []string
	for _, value := range []string{os.Getenv("YOLO_MODEL_DIR"), os.Getenv("ULTRALYTICS_DIR")} {
		for _, dir := range filepath.SplitList(value) {
			dir = strings.TrimSpace(dir)
			if dir != "" {
				candidates = append(candidates, filepath.Join(dir, DefaultYOLO26WeightName))
			}
		}
	}
	for _, root := range defaultToolRoots() {
		candidates = append(candidates,
			filepath.Join(root, "third_party", "ultralytics-8.4.10", DefaultYOLO26WeightName),
			filepath.Join(root, "ultralytics-8.4.10", DefaultYOLO26WeightName),
		)
	}
	return candidates
}

func defaultToolRoots() []string {
	var roots []string
	if workingDir, err := os.Getwd(); err == nil {
		roots = append(roots, workingDir)
	}
	if executable, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(executable), filepath.Dir(filepath.Dir(executable)))
	}
	return roots
}

type ProjectPaths struct {
	ImagesDir     string `json:"imagesDir"`
	ValImagesDir  string `json:"valImagesDir"`
	XMLDir        string `json:"xmlDir"`
	LabelMeDir    string `json:"labelmeDir"`
	LabelsDir     string `json:"labelsDir"`
	ValLabelsDir  string `json:"valLabelsDir"`
	RunsDir       string `json:"runsDir"`
	PredictDir    string `json:"predictDir"`
	ClassesFile   string `json:"classesFile"`
	YamlFile      string `json:"yamlFile"`
	TrainListFile string `json:"trainListFile"`
	ValListFile   string `json:"valListFile"`
}

type ImageInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type ProjectState struct {
	RootPath       string             `json:"rootPath"`
	Paths          ProjectPaths       `json:"paths"`
	Classes        []string           `json:"classes"`
	SampleCount    int                `json:"sampleCount"`
	LabelCount     int                `json:"labelCount"`
	TrainImages    []ImageInfo        `json:"trainImages"`
	LatestRun      *TrainingResult    `json:"latestRun,omitempty"`
	TrainingRuns   []TrainingResult   `json:"trainingRuns"`
	PredictionRuns []PredictionResult `json:"predictionRuns"`
}

type TrainingResult struct {
	RunDir     string             `json:"runDir"`
	ModelPath  string             `json:"modelPath"`
	Metrics    map[string]float64 `json:"metrics"`
	Artifacts  []string           `json:"artifacts"`
	ModifiedAt time.Time          `json:"modifiedAt"`
	ImageCount int                `json:"imageCount"`
	ClassCount int                `json:"classCount"`
	Manifest   *TrainingManifest  `json:"manifest,omitempty"`
}

type TrainingManifest struct {
	Version            int            `json:"version"`
	RunDir             string         `json:"runDir"`
	ModelPath          string         `json:"modelPath"`
	ResolvedModel      string         `json:"resolvedModel"`
	Config             TrainingConfig `json:"config"`
	Classes            []string       `json:"classes"`
	ImageCount         int            `json:"imageCount"`
	ClassCount         int            `json:"classCount"`
	LabelCount         int            `json:"labelCount"`
	TrainingImages     int            `json:"trainingImages"`
	ValidationImages   int            `json:"validationImages"`
	ValidationMirrored bool           `json:"validationMirrored"`
	Command            string         `json:"command"`
	CommandArgs        []string       `json:"commandArgs"`
	YOLOCommand        string         `json:"yoloCommand"`
	YOLOCommandArgs    []string       `json:"yoloCommandArgs"`
	PythonCommand      string         `json:"pythonCommand,omitempty"`
	UltralyticsDir     string         `json:"ultralyticsDir,omitempty"`
	StartedAt          time.Time      `json:"startedAt"`
	FinishedAt         time.Time      `json:"finishedAt"`
	Status             string         `json:"status"`
	Error              string         `json:"error,omitempty"`
}

type DatasetSplit struct {
	TrainListFile string `json:"trainListFile"`
	ValListFile   string `json:"valListFile"`
	TotalImages   int    `json:"totalImages"`
	TrainImages   int    `json:"trainImages"`
	ValImages     int    `json:"valImages"`
	Mirrored      bool   `json:"mirrored"`
}

type PredictionConfig struct {
	ProjectRoot string `json:"projectRoot,omitempty"`
	// Deprecated: accepted for backward-compatible request decoding only.
	// Prediction always uses the backend's startup-selected Python runtime.
	PythonRuntime string  `json:"pythonRuntime,omitempty"`
	CondaEnv      string  `json:"condaEnv,omitempty"`
	ImagePath     string  `json:"imagePath"`
	ModelPath     string  `json:"modelPath"`
	Device        string  `json:"device"`
	ImageSize     int     `json:"imageSize"`
	Confidence    float64 `json:"confidence"`
}

// ClearLegacyRuntimeSelection prevents a prediction request from selecting a
// host Python or Conda environment.
func (c *PredictionConfig) ClearLegacyRuntimeSelection() {
	if c == nil {
		return
	}
	c.PythonRuntime = ""
	c.CondaEnv = ""
}

type AutoAnnotateConfig struct {
	ProjectRoot string  `json:"projectRoot,omitempty"`
	ImageDir    string  `json:"imageDir,omitempty"`
	OutputDir   string  `json:"outputDir,omitempty"`
	ModelPath   string  `json:"modelPath"`
	Device      string  `json:"device"`
	ImageSize   int     `json:"imageSize"`
	Confidence  float64 `json:"confidence"`
	IOU         float64 `json:"iou"`
	Overwrite   bool    `json:"overwrite"`
}

type AutoAnnotateReport struct {
	ImageCount     int      `json:"imageCount"`
	AnnotatedFiles int      `json:"annotatedFiles"`
	SkippedFiles   int      `json:"skippedFiles"`
	ShapeCount     int      `json:"shapeCount"`
	OutputDir      string   `json:"outputDir"`
	Errors         []string `json:"errors"`
}

type PredictionResult struct {
	RunID          string                `json:"runId"`
	ModelPath      string                `json:"modelPath"`
	SourceImage    string                `json:"sourceImage"`
	OutputDir      string                `json:"outputDir"`
	OutputImage    string                `json:"outputImage"`
	Artifacts      []string              `json:"artifacts"`
	Detections     []PredictionDetection `json:"detections"`
	DetectionCount int                   `json:"detectionCount"`
	RanAt          time.Time             `json:"ranAt"`
	Device         string                `json:"device,omitempty"`
	ImageSize      int                   `json:"imageSize,omitempty"`
	Confidence     float64               `json:"confidence,omitempty"`
}

type PredictionDetection struct {
	ClassID    int     `json:"classId"`
	ClassName  string  `json:"className"`
	Confidence float64 `json:"confidence"`
	XCenter    float64 `json:"xCenter"`
	YCenter    float64 `json:"yCenter"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	LabelPath  string  `json:"labelPath"`
}

type PreflightCheck struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type PreflightReport struct {
	Mode     string           `json:"mode"`
	Ready    bool             `json:"ready"`
	Errors   int              `json:"errors"`
	Warnings int              `json:"warnings"`
	Checks   []PreflightCheck `json:"checks"`
}

type ConvertReport struct {
	XMLFiles       int      `json:"xmlFiles"`
	JSONFiles      int      `json:"jsonFiles"`
	ConvertedFiles int      `json:"convertedFiles"`
	Boxes          int      `json:"boxes"`
	SkippedObjects int      `json:"skippedObjects"`
	Errors         []string `json:"errors"`
}

type ValidationReport struct {
	ImageCount              int                 `json:"imageCount"`
	LabelCount              int                 `json:"labelCount"`
	ValidLabels             int                 `json:"validLabels"`
	TotalBoxes              int                 `json:"totalBoxes"`
	DetectionAnnotations    int                 `json:"detectionAnnotations"`
	SegmentationAnnotations int                 `json:"segmentationAnnotations"`
	MissingLabels           []string            `json:"missingLabels"`
	EmptyLabels             []string            `json:"emptyLabels"`
	InvalidLabels           map[string][]string `json:"invalidLabels"`
	ClassCounts             map[string]int      `json:"classCounts"`
}
