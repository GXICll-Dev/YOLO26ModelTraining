package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"modeltraining-go-ts/internal/dataset"
	"modeltraining-go-ts/internal/license"
	"modeltraining-go-ts/internal/tasks"
	"modeltraining-go-ts/internal/training"
)

type Server struct {
	baseDir    string
	stateDir   string
	configPath string
	tasks      *tasks.Manager
	training   training.Runner
	license    license.Manager
	probeYOLO  func(context.Context) training.RuntimeProbeReport
	probeMu    sync.Mutex
	probeCache *training.RuntimeProbeReport
	probeKey   string
	webcamMu   sync.Mutex
	webcamStop context.CancelFunc
}

func NewServer(baseDir string) (*Server, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	stateDir := os.Getenv("MT_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Join(abs, "state")
	}
	runner := training.NewRunner()
	return &Server{
		baseDir:    abs,
		stateDir:   stateDir,
		configPath: filepath.Join(stateDir, "training_config.json"),
		tasks:      tasks.NewPersistentManager(filepath.Join(stateDir, "tasks")),
		training:   runner,
		license:    license.NewManager(stateDir),
		probeYOLO:  runner.ProbeRuntime,
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.handleAPI)
	mux.Handle("/", s.staticHandler())
	return withCommonHeaders(mux)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/health" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now()})
	case r.URL.Path == "/api/config" && r.Method == http.MethodGet:
		s.handleGetConfig(w, r)
	case r.URL.Path == "/api/config" && r.Method == http.MethodPut:
		s.handlePutConfig(w, r)
	case r.URL.Path == "/api/license/status" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.license.Check())
	case r.URL.Path == "/api/license/activate" && r.Method == http.MethodPost:
		s.handleActivate(w, r)
	case r.URL.Path == "/api/projects/open" && r.Method == http.MethodPost:
		s.handleOpenProject(w, r)
	case r.URL.Path == "/api/projects/state" && r.Method == http.MethodGet:
		s.handleProjectState(w, r)
	case r.URL.Path == "/api/projects/classes" && r.Method == http.MethodPut:
		s.handleSaveClasses(w, r)
	case r.URL.Path == "/api/projects/images/import" && r.Method == http.MethodPost:
		s.handleImportImages(w, r)
	case r.URL.Path == "/api/projects/images/upload" && r.Method == http.MethodPost:
		s.handleUploadImages(w, r)
	case (r.URL.Path == "/api/projects/convert-annotations" || r.URL.Path == "/api/projects/convert-voc") && r.Method == http.MethodPost:
		s.handleConvertAnnotations(w, r)
	case r.URL.Path == "/api/projects/validate-labels" && r.Method == http.MethodPost:
		s.handleValidateLabels(w, r)
	case r.URL.Path == "/api/projects/result" && r.Method == http.MethodGet:
		s.handleLatestResult(w, r)
	case r.URL.Path == "/api/projects/results" && r.Method == http.MethodGet:
		s.handleTrainingResults(w, r)
	case r.URL.Path == "/api/projects/preflight" && r.Method == http.MethodPost:
		s.handleProjectPreflight(w, r)
	case r.URL.Path == "/api/tasks/train" && r.Method == http.MethodPost:
		s.handleStartTrain(w, r)
	case r.URL.Path == "/api/tasks/predict" && r.Method == http.MethodPost:
		s.handleStartPredict(w, r)
	case r.URL.Path == "/api/tasks/predict-image" && r.Method == http.MethodPost:
		s.handleUploadPredictImage(w, r)
	case r.URL.Path == "/api/tasks/auto-annotate" && r.Method == http.MethodPost:
		s.handleAutoAnnotate(w, r)
	case r.URL.Path == "/api/tasks" && r.Method == http.MethodGet:
		s.handleTaskList(w, r)
	case r.URL.Path == "/api/predict/frame" && r.Method == http.MethodPost:
		s.handlePredictFrame(w, r)
	case r.URL.Path == "/api/predict/webcam" && r.Method == http.MethodGet:
		s.handlePredictWebcam(w, r)
	case r.URL.Path == "/api/predict/webcam/stop" && r.Method == http.MethodPost:
		s.stopPredictWebcam()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case strings.HasPrefix(r.URL.Path, "/api/tasks/"):
		s.handleTaskRoute(w, r)
	case r.URL.Path == "/api/files/image" && r.Method == http.MethodGet:
		s.handleImageFile(w, r)
	case r.URL.Path == "/api/open-path" && r.Method == http.MethodPost:
		s.handleOpenPath(w, r)
	case r.URL.Path == "/api/conda/envs" && r.Method == http.MethodGet:
		s.handleCondaEnvs(w, r)
	case r.URL.Path == "/api/diagnostics/export" && r.Method == http.MethodPost:
		s.handleExportDiagnostics(w, r)
	case (r.URL.Path == "/api/tools/labelme" || r.URL.Path == "/api/tools/labelimg") && r.Method == http.MethodPost:
		s.handleLaunchLabelMe(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := dataset.LoadTrainingConfig(s.configPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var cfg dataset.TrainingConfig
	if err := readJSON(r, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.ClearLegacyRuntimeSelection()
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := dataset.SaveTrainingConfig(s.configPath, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := s.license.Activate(req.Key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleOpenProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootPath string `json:"rootPath"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	state, err := dataset.EnsureProject(req.RootPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleProjectState(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("rootPath")
	state, err := dataset.State(root)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleSaveClasses(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootPath string   `json:"rootPath"`
		Classes  []string `json:"classes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := dataset.SaveClasses(req.RootPath, req.Classes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	state, _ := dataset.State(req.RootPath)
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleImportImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootPath string   `json:"rootPath"`
		Sources  []string `json:"sources"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	imported, skipped, err := dataset.ImportImages(req.RootPath, req.Sources)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	state, _ := dataset.State(req.RootPath)
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"state":    state,
	})
}

func (s *Server) handleUploadImages(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := dataset.NormalizeRoot(r.FormValue("rootPath"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := dataset.EnsureProject(root); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	destDir := dataset.Paths(root).ImagesDir
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	imported := 0
	var skipped []string
	files := r.MultipartForm.File["images"]
	for _, header := range files {
		if !supportedImageName(header.Filename) {
			skipped = append(skipped, header.Filename+": unsupported image type")
			continue
		}
		source, err := header.Open()
		if err != nil {
			skipped = append(skipped, header.Filename+": "+err.Error())
			continue
		}
		destPath := filepath.Join(destDir, filepath.Base(header.Filename))
		dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			_ = source.Close()
			if errors.Is(err, os.ErrExist) {
				skipped = append(skipped, header.Filename+": target file already exists")
			} else {
				skipped = append(skipped, header.Filename+": "+err.Error())
			}
			continue
		}
		_, copyErr := io.Copy(dest, source)
		closeErr := dest.Close()
		_ = source.Close()
		if copyErr != nil {
			skipped = append(skipped, header.Filename+": "+copyErr.Error())
			continue
		}
		if closeErr != nil {
			skipped = append(skipped, header.Filename+": "+closeErr.Error())
			continue
		}
		imported++
	}

	state, _ := dataset.State(root)
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"state":    state,
	})
}

func (s *Server) handleConvertAnnotations(w http.ResponseWriter, r *http.Request) {
	root, err := readRootRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task := s.tasks.Create("convert-voc", root)
	ctx, cancel := context.WithCancel(context.Background())
	task.Start(ctx, cancel)
	go func() {
		defer cancel()
		report, err := dataset.ConvertAnnotationsToYOLO(root)
		if err != nil {
			task.Log("标注转换失败：" + err.Error())
			task.Fail(err)
			return
		}
		totalFiles := report.XMLFiles + report.JSONFiles
		task.Log(fmt.Sprintf("标注转换完成：%d→%d 标签，%d 框，跳过 %d，问题 %d。", totalFiles, report.ConvertedFiles, report.Boxes, report.SkippedObjects, len(report.Errors)))
		task.Succeed(report)
	}()
	writeJSON(w, http.StatusAccepted, task.Snapshot())
}

func (s *Server) handleValidateLabels(w http.ResponseWriter, r *http.Request) {
	root, err := readRootRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task := s.tasks.Create("validate-labels", root)
	ctx, cancel := context.WithCancel(context.Background())
	task.Start(ctx, cancel)
	go func() {
		defer cancel()
		report, err := dataset.ValidateLabels(root)
		if err != nil {
			task.Log("标签验证失败：" + err.Error())
			task.Fail(err)
			return
		}
		problems := len(report.MissingLabels) + len(report.EmptyLabels) + len(report.InvalidLabels)
		task.Log(fmt.Sprintf("标签验证完成：%d 张图片，%d 标签可用，%d 框，问题 %d。", report.ImageCount, report.ValidLabels, report.TotalBoxes, problems))
		task.Succeed(report)
	}()
	writeJSON(w, http.StatusAccepted, task.Snapshot())
}

func (s *Server) handleLatestResult(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("rootPath")
	result, err := dataset.LatestResult(root)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleTrainingResults(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("rootPath")
	results, err := dataset.TrainingResults(root)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleProjectPreflight(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode       string  `json:"mode"`
		RootPath   string  `json:"rootPath"`
		ModelPath  string  `json:"modelPath"`
		ImagePath  string  `json:"imagePath"`
		Device     string  `json:"device"`
		ImageSize  int     `json:"imageSize"`
		Confidence float64 `json:"confidence"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	report := s.buildPreflightReport(req.Mode, req.RootPath, req.ModelPath, req.ImagePath, req.Device, req.ImageSize, req.Confidence)
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleStartTrain(w http.ResponseWriter, r *http.Request) {
	var cfg dataset.TrainingConfig
	if err := readJSON(r, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := dataset.NormalizeRoot(cfg.ProjectRoot)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.ProjectRoot = root
	report := s.buildPreflightReport("train", root, cfg.Model, "", cfg.Device, cfg.ImageSize, 0)
	if !report.Ready {
		writeError(w, http.StatusBadRequest, preflightError(report))
		return
	}
	task := s.tasks.Create("train", root)
	s.training.StartTrain(context.Background(), task, cfg)
	writeJSON(w, http.StatusAccepted, task.Snapshot())
}

func (s *Server) handleStartPredict(w http.ResponseWriter, r *http.Request) {
	var cfg dataset.PredictionConfig
	if err := readJSON(r, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := dataset.NormalizeRoot(cfg.ProjectRoot)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	imagePath, err := normalizeExistingImage(cfg.ImagePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.ProjectRoot = root
	cfg.ImagePath = imagePath
	report := s.buildPreflightReport("predict", root, cfg.ModelPath, imagePath, cfg.Device, cfg.ImageSize, cfg.Confidence)
	if !report.Ready {
		writeError(w, http.StatusBadRequest, preflightError(report))
		return
	}
	task := s.tasks.Create("predict", root)
	s.training.StartPredict(context.Background(), task, cfg)
	writeJSON(w, http.StatusAccepted, task.Snapshot())
}

func (s *Server) handleUploadPredictImage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := dataset.NormalizeRoot(r.FormValue("rootPath"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := dataset.EnsureProject(root); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	imagePath, err := savePredictUpload(root, r.MultipartForm.File["image"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := dataset.PredictionConfig{
		ProjectRoot: root,
		ImagePath:   imagePath,
		ModelPath:   strings.TrimSpace(r.FormValue("modelPath")),
		Device:      strings.TrimSpace(r.FormValue("device")),
		ImageSize:   parseIntDefault(r.FormValue("imageSize"), dataset.DefaultTrainingConfig().ImageSize),
		Confidence:  parseFloatDefault(r.FormValue("confidence"), 0.25),
	}
	report := s.buildPreflightReport("predict", root, cfg.ModelPath, imagePath, cfg.Device, cfg.ImageSize, cfg.Confidence)
	if !report.Ready {
		removePredictUpload(root, imagePath)
		writeError(w, http.StatusBadRequest, preflightError(report))
		return
	}
	task := s.tasks.Create("predict", root)
	s.training.StartPredict(context.Background(), task, cfg)
	writeJSON(w, http.StatusAccepted, task.Snapshot())
}

func (s *Server) handleAutoAnnotate(w http.ResponseWriter, r *http.Request) {
	var cfg dataset.AutoAnnotateConfig
	if err := readJSON(r, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	taskRoot := strings.TrimSpace(cfg.ProjectRoot)
	if taskRoot != "" {
		root, err := dataset.NormalizeRoot(taskRoot)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := dataset.EnsureProject(root); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cfg.ProjectRoot = root
		taskRoot = root
	} else {
		taskRoot = strings.TrimSpace(cfg.OutputDir)
		if taskRoot == "" {
			taskRoot = strings.TrimSpace(cfg.ImageDir)
		}
		if taskRoot == "" {
			writeError(w, http.StatusBadRequest, "请选择图片目录和输出目录")
			return
		}
		if abs, err := filepath.Abs(taskRoot); err == nil {
			taskRoot = filepath.Clean(abs)
		}
	}
	if cfg.ImageSize <= 0 {
		cfg.ImageSize = dataset.DefaultTrainingConfig().ImageSize
	}
	if strings.TrimSpace(cfg.Device) == "" {
		cfg.Device = dataset.DefaultTrainingConfig().Device
	}
	if cfg.Confidence <= 0 {
		cfg.Confidence = 0.25
	}
	if deviceRequestsCUDA(cfg.Device) {
		probe := s.cachedYOLORuntimeProbe()
		if probe.OK && !probe.CUDAAvailable {
			writeError(w, http.StatusBadRequest, cudaDeviceUnavailableMessage(cfg.Device, probe))
			return
		}
	}
	task := s.tasks.Create("auto-annotate", taskRoot)
	s.training.StartAutoAnnotate(context.Background(), task, cfg)
	writeJSON(w, http.StatusAccepted, task.Snapshot())
}

func (s *Server) handlePredictFrame(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := dataset.NormalizeRoot(r.FormValue("rootPath"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := dataset.EnsureProject(root); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	imagePath, err := savePredictFrame(root, r.MultipartForm.File["image"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer removePredictUpload(root, imagePath)

	cfg := dataset.PredictionConfig{
		ProjectRoot: root,
		ImagePath:   imagePath,
		ModelPath:   strings.TrimSpace(r.FormValue("modelPath")),
		Device:      strings.TrimSpace(r.FormValue("device")),
		ImageSize:   parseIntDefault(r.FormValue("imageSize"), dataset.DefaultTrainingConfig().ImageSize),
		Confidence:  parseFloatDefault(r.FormValue("confidence"), 0.25),
	}
	report := s.buildPreflightReport("predict", root, cfg.ModelPath, imagePath, cfg.Device, cfg.ImageSize, cfg.Confidence)
	if !report.Ready {
		writeError(w, http.StatusBadRequest, preflightError(report))
		return
	}

	result, err := s.predictFrame(r.Context(), root, cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePredictWebcam(w http.ResponseWriter, r *http.Request) {
	s.stopPredictWebcam()
	root, err := dataset.NormalizeRoot(r.URL.Query().Get("rootPath"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := dataset.EnsureProject(root); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	model, err := s.resolvePreflightModel(root, r.URL.Query().Get("modelPath"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	python, err := s.training.PythonExecutable()
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法启动 YOLO 实时识别: "+err.Error())
		return
	}

	camera := parseIntDefault(r.URL.Query().Get("camera"), 0)
	imageSize := parseIntDefault(r.URL.Query().Get("imageSize"), 416)
	if imageSize > 640 {
		imageSize = 640
	}
	confidence := parseFloatDefault(r.URL.Query().Get("confidence"), 0.25)
	device := strings.TrimSpace(r.URL.Query().Get("device"))
	if device == "" {
		device = dataset.DefaultTrainingConfig().Device
	}
	if deviceRequestsCUDA(device) {
		probe := s.cachedYOLORuntimeProbe()
		if probe.OK && !probe.CUDAAvailable {
			writeError(w, http.StatusBadRequest, cudaDeviceUnavailableMessage(device, probe))
			return
		}
	}

	ctx, cancel := context.WithCancel(r.Context())
	s.webcamMu.Lock()
	s.webcamStop = cancel
	s.webcamMu.Unlock()
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-u", "-c", webcamPythonScript(), model, strconv.Itoa(camera), formatFloatForCommand(confidence), strconv.Itoa(imageSize), device)
	cmd.Dir = root
	cmd.Env = s.training.CommandEnvironment()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("启动 YOLO 实时识别失败: %v", err))
		return
	}
	defer func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		s.webcamMu.Lock()
		if s.webcamStop != nil {
			s.webcamStop = nil
		}
		s.webcamMu.Unlock()
	}()
	go io.Copy(io.Discard, stderr)

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "close")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	buffer := make([]byte, 32*1024)
	for {
		n, readErr := stdout.Read(buffer)
		if n > 0 {
			if _, err := w.Write(buffer[:n]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (s *Server) stopPredictWebcam() {
	s.webcamMu.Lock()
	cancel := s.webcamStop
	s.webcamStop = nil
	s.webcamMu.Unlock()
	if cancel != nil {
		cancel()
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *Server) handleTaskRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "task id is required")
		return
	}
	task, ok := s.tasks.Get(parts[0])
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, task.Snapshot())
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		if !task.Cancel() {
			task.MarkCanceled()
		}
		writeJSON(w, http.StatusOK, task.Snapshot())
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		s.streamTask(w, r, task)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(r.URL.Query().Get("rootPath"))
	if root != "" {
		if normalized, err := dataset.NormalizeRoot(root); err == nil {
			root = normalized
		}
	}
	taskList := s.tasks.List()
	snapshots := make([]tasks.Task, 0, len(taskList))
	for _, task := range taskList {
		snapshot := task.Snapshot()
		if root != "" && !strings.EqualFold(filepath.Clean(snapshot.ProjectRoot), filepath.Clean(root)) {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	writeJSON(w, http.StatusOK, snapshots)
}

func (s *Server) streamTask(w http.ResponseWriter, r *http.Request, task *tasks.Task) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		snapshot := task.Snapshot()
		data, _ := json.Marshal(snapshot)
		fmt.Fprintf(w, "event: task\n")
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		if isFinal(snapshot.Status) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) handleOpenPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := openPath(req.Path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCondaEnvs(w http.ResponseWriter, r *http.Request) {
	// Backward-compatible endpoint for older frontends. Deliberately do not
	// inspect the host PATH or enumerate Conda: release tasks must remain bound
	// to the backend's startup-selected Python runtime.
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Server) handleExportDiagnostics(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootPath string   `json:"rootPath"`
		TaskIDs  []string `json:"taskIds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path, err := s.writeDiagnosticsReport(req.RootPath, req.TaskIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) handleImageFile(w http.ResponseWriter, r *http.Request) {
	path, err := normalizeExistingImage(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) handleLaunchLabelMe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootPath  string `json:"rootPath"`
		ImageDir  string `json:"imageDir"`
		OutputDir string `json:"outputDir"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root := strings.TrimSpace(req.RootPath)
	imageDir := strings.TrimSpace(req.ImageDir)
	outputDir := strings.TrimSpace(req.OutputDir)
	classesFile := ""
	if root != "" {
		normalized, err := dataset.NormalizeRoot(root)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		root = normalized
		paths := dataset.Paths(root)
		imageDir = paths.ImagesDir
		outputDir = paths.LabelMeDir
		classesFile = paths.ClassesFile
	} else {
		if imageDir == "" || outputDir == "" {
			writeError(w, http.StatusBadRequest, "请选择图片目录和 LabelMe 输出目录")
			return
		}
		absImageDir, err := filepath.Abs(imageDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		imageDir = filepath.Clean(absImageDir)
		if info, err := os.Stat(imageDir); err != nil || !info.IsDir() {
			writeError(w, http.StatusBadRequest, "图片目录不可用")
			return
		}
		absOutputDir, err := filepath.Abs(outputDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		outputDir = filepath.Clean(absOutputDir)
		root = filepath.Dir(imageDir)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := repairLabelMeImageData(imageDir, outputDir); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	configPath, err := s.ensureLabelMeConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	executable := resolveLabelMeExecutable(s.baseDir, root)
	args := []string{"--config", configPath, imageDir, "--output", outputDir, "--autosave"}
	if classesFile != "" {
		if info, err := os.Stat(classesFile); err == nil && info.Size() > 0 {
			args = append(args, "--labels", classesFile, "--validatelabel", "exact")
		}
	}
	cmd := exec.Command(executable, args...)
	if filepath.IsAbs(executable) {
		cmd.Dir = filepath.Dir(executable)
	}
	if err := cmd.Start(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pid": cmd.Process.Pid})
}

func (s *Server) ensureLabelMeConfig() (string, error) {
	dir := filepath.Join(s.stateDir, "labelme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.yaml")
	// Always override any stale user-level ~/.labelmerc AI default. Automatic
	// annotation is provided by this application, so LabelMe must never select
	// Sam2 (or another model that downloads weights) during startup.
	if err := os.WriteFile(path, []byte("ai:\n  default: null\n"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func repairLabelMeImageData(imageDir, outputDir string) error {
	entries, err := os.ReadDir(outputDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		jsonPath := filepath.Join(outputDir, entry.Name())
		data, err := os.ReadFile(jsonPath)
		if err != nil {
			return err
		}
		var annotation map[string]any
		if err := json.Unmarshal(data, &annotation); err != nil {
			continue
		}
		changed := false
		if value, ok := annotation["imageData"].(string); ok && strings.TrimSpace(value) == "" {
			imageName, _ := annotation["imagePath"].(string)
			if strings.TrimSpace(imageName) == "" {
				imageName = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			}
			imagePath := findLabelMeImageFile(imageDir, imageName)
			if imageBytes, err := os.ReadFile(imagePath); err == nil {
				annotation["imageData"] = base64.StdEncoding.EncodeToString(imageBytes)
				changed = true
			}
		}
		if raw, ok := annotation["shapes"].([]any); ok && len(raw) > 1 {
			seen := make(map[string]bool)
			deduped := make([]any, 0, len(raw))
			for _, item := range raw {
				shape, ok := item.(map[string]any)
				if !ok {
					deduped = append(deduped, item)
					continue
				}
				key := fmt.Sprintf("%v|%v", shape["label"], shape["points"])
				if seen[key] {
					changed = true
					continue
				}
				seen[key] = true
				deduped = append(deduped, item)
			}
			if changed {
				annotation["shapes"] = deduped
			}
		}
		if !changed {
			continue
		}
		updated, err := json.MarshalIndent(annotation, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(jsonPath, updated, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func findLabelMeImageFile(imageDir, imageName string) string {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return ""
	}
	if filepath.IsAbs(imageName) {
		return imageName
	}
	candidate := filepath.Join(imageDir, imageName)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	if filepath.Ext(imageName) != "" {
		return candidate
	}
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".bmp"} {
		candidate = filepath.Join(imageDir, imageName+ext)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return filepath.Join(imageDir, imageName)
}

func (s *Server) buildPreflightReport(mode, rootPath, modelPath, imagePath, device string, imageSize int, confidence float64) dataset.PreflightReport {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "train"
	}
	report := dataset.PreflightReport{Mode: mode, Ready: true, Checks: []dataset.PreflightCheck{}}
	add := func(key, label, level, message, hint string) {
		report.Checks = append(report.Checks, dataset.PreflightCheck{
			Key: key, Label: label, Level: level, Message: message, Hint: hint,
		})
		switch level {
		case "error":
			report.Errors++
			report.Ready = false
		case "warning":
			report.Warnings++
		}
	}

	root, err := dataset.NormalizeRoot(rootPath)
	if err != nil {
		add("project-root", "项目目录", "error", "项目根目录无效", err.Error())
		return report
	}
	if info, err := os.Stat(root); err != nil {
		add("project-root", "项目目录", "error", "项目目录不存在或不可访问", err.Error())
		return report
	} else if !info.IsDir() {
		add("project-root", "项目目录", "error", "项目根目录不是文件夹", "请选择项目根目录文件夹。")
		return report
	}
	add("project-root", "项目目录", "ok", root, "")

	paths := dataset.Paths(root)
	checkWritableDir(paths.ImagesDir, "images-train", "训练图片目录", add)
	checkWritableDir(paths.LabelsDir, "labels-train", "YOLO标签目录", add)
	checkWritableDir(paths.RunsDir, "runs-dir", "训练输出目录", add)
	checkWritableDir(paths.PredictDir, "predict-dir", "模型测试目录", add)

	classes, classErr := dataset.LoadClasses(root)
	if classErr != nil {
		add("classes", "类别文件", "error", "类别文件读取失败", classErr.Error())
	} else if len(classes) == 0 {
		classLevel := "warning"
		if mode == "train" {
			classLevel = "error"
		}
		add("classes", "类别文件", classLevel, "当前没有类别", "训练前请先添加至少一个类别。")
	} else {
		add("classes", "类别文件", "ok", fmt.Sprintf("%d 个类别", len(classes)), "")
	}

	images, imageErr := dataset.ListImages(root)
	if imageErr != nil {
		add("images", "训练图片", "error", "训练图片目录读取失败", imageErr.Error())
	} else if len(images) == 0 {
		add("images", "训练图片", levelForMode(mode, "train"), "没有训练图片", "训练前请先导入图片。")
	} else {
		add("images", "训练图片", "ok", fmt.Sprintf("%d 张图片", len(images)), "")
	}
	if imageErr == nil && len(images) > 0 {
		if split, err := dataset.PreviewDatasetSplit(root); err != nil {
			add("validation-split", "验证集划分", levelForMode(mode, "train"), "验证集划分失败", err.Error())
		} else if split.Mirrored {
			add("validation-split", "验证集划分", "warning", fmt.Sprintf("训练 %d 张 / 验证 %d 张，验证集复用训练图片", split.TrainImages, split.ValImages), "少于 5 张图片时会镜像验证集，指标只能作参考。")
		} else {
			add("validation-split", "验证集划分", "ok", fmt.Sprintf("训练 %d 张 / 验证 %d 张", split.TrainImages, split.ValImages), "")
		}
	}

	if labelCount, err := countTextFiles(paths.LabelsDir); err != nil {
		add("labels", "YOLO标签", "warning", "标签目录读取失败", err.Error())
	} else if labelCount == 0 {
		add("labels", "YOLO标签", "warning", "还没有 YOLO 标签文件", "可先打开 LabelMe 标注，再执行标注转 YOLO 格式。")
	} else {
		add("labels", "YOLO标签", "ok", fmt.Sprintf("%d 个标签文件", labelCount), "")
	}

	modelLevel := "warning"
	if mode == "train" || mode == "predict" {
		modelLevel = "error"
	}
	if resolved, err := s.resolvePreflightModel(root, modelPath); err != nil {
		add("model", "YOLO26模型", modelLevel, "模型文件不可用", err.Error())
	} else {
		add("model", "YOLO26模型", "ok", resolved, "")
	}

	if _, err := exec.LookPath(s.training.YOLOCommand); err != nil {
		commandLevel := "warning"
		if mode == "train" || mode == "predict" {
			commandLevel = "error"
		}
		add("yolo-command", "YOLO命令", commandLevel, fmt.Sprintf("未找到可执行程序 %s", s.training.YOLOCommand), "内置运行时不完整，请重新解压或安装完整程序。")
	} else if len(s.training.YOLOCommandArgs) > 0 {
		message := s.training.CommandDisplay()
		if s.training.UltralyticsDir != "" {
			message += "（使用内置 YOLO26 源码）"
		}
		add("yolo-command", "YOLO命令", "ok", message, "")
	} else {
		add("yolo-command", "YOLO命令", "ok", s.training.CommandDisplay(), "")
	}

	var runtimeProbe *training.RuntimeProbeReport
	if _, err := exec.LookPath(s.training.YOLOCommand); err == nil {
		probe := s.cachedYOLORuntimeProbe()
		runtimeProbe = &probe
		runtimeLevel := "warning"
		if mode == "train" || mode == "predict" {
			runtimeLevel = "error"
		}
		if probe.OK {
			message := fmt.Sprintf("Ultralytics %s / Torch %s", probe.UltralyticsVersion, probe.TorchVersion)
			hint := ""
			if probe.CUDAAvailable {
				message += " / CUDA 可用"
				if probe.TorchCUDA != "" {
					message += " " + probe.TorchCUDA
				}
				if probe.CUDADeviceName != "" {
					message += " / " + probe.CUDADeviceName
				}
				if probe.CUDAMemoryMiB > 0 {
					message += fmt.Sprintf("（%d MiB）", probe.CUDAMemoryMiB)
				}
			} else if probe.CUDAReported {
				message += " / 已检测到 CUDA"
				if probe.TorchCUDA != "" {
					message += " " + probe.TorchCUDA
				}
				if probe.CUDADeviceName != "" {
					message += " / " + probe.CUDADeviceName
				}
				message += "，但张量测试失败，当前回退 CPU"
				hint = probe.CUDASmokeError
			} else if probe.TorchCUDA != "" {
				message += " / 已内置 CUDA " + probe.TorchCUDA + "，当前回退 CPU"
			} else {
				message += " / CPU-only Torch"
			}
			add("yolo-runtime", "YOLO运行时", "ok", message, hint)
		} else {
			add("yolo-runtime", "YOLO运行时", runtimeLevel, "Ultralytics/Torch 运行时不可用", probe.Error)
		}
	}

	labelMe := resolveLabelMeExecutable(s.baseDir, root)
	if filepath.IsAbs(labelMe) {
		if info, err := os.Stat(labelMe); err == nil && !info.IsDir() {
			add("labelme", "LabelMe", "ok", labelMe, "")
		} else {
			add("labelme", "LabelMe", "warning", "未找到可执行的 LabelMe", "请确认 tools/labelme/labelme.exe 已随程序打包。")
		}
	} else {
		add("labelme", "LabelMe", "warning", "LabelMe 需要依赖系统 PATH", "建议使用项目内置 tools/labelme。")
	}

	if imageSize < 64 || imageSize > 4096 {
		add("image-size", "图像尺寸", "error", "图像尺寸超出 64..4096", "请在训练参数中调整图像尺寸。")
	} else if imageSize > 1280 {
		add("image-size", "图像尺寸", "warning", "图像尺寸较大，可能显著增加训练/测试时间", "")
	} else {
		add("image-size", "图像尺寸", "ok", fmt.Sprintf("%d", imageSize), "")
	}

	if strings.TrimSpace(device) == "" {
		device = dataset.DefaultTrainingConfig().Device
	}
	if training.IsAutomaticDevice(device) {
		message := "自动选择（预计 CPU）"
		hint := "每个执行进程都会运行 CUDA 张量测试；不可用时自动使用 CPU。"
		if runtimeProbe != nil && runtimeProbe.OK && runtimeProbe.CUDAAvailable {
			message = "自动选择（预计 CUDA 0）"
			if runtimeProbe.CUDADeviceName != "" {
				message += " / " + runtimeProbe.CUDADeviceName
			}
		} else if runtimeProbe != nil && runtimeProbe.OK && runtimeProbe.CUDAReported && runtimeProbe.CUDASmokeError != "" {
			hint = "CUDA 张量测试失败，将回退 CPU：" + runtimeProbe.CUDASmokeError
		}
		add("device", "运行设备", "ok", message, hint)
	} else if deviceRequestsCUDA(device) && runtimeProbe != nil && runtimeProbe.OK && !runtimeProbe.CUDAAvailable {
		add("device", "运行设备", "error", cudaDeviceUnavailableMessage(device, *runtimeProbe), "请选择自动或 CPU；如果已检测到显卡，请更新到支持该 GPU 架构的 Torch/CUDA 运行时。")
	} else {
		add("device", "运行设备", "ok", device, "")
	}

	if mode == "predict" {
		if image, err := normalizeExistingImage(imagePath); err != nil {
			add("predict-image", "测试图片", "error", "测试图片不可用", err.Error())
		} else {
			add("predict-image", "测试图片", "ok", image, "")
		}
		if confidence <= 0 || confidence > 1 {
			add("confidence", "置信度", "error", "置信度必须在 0..1 之间", "")
		} else {
			add("confidence", "置信度", "ok", fmt.Sprintf("%.2f", confidence), "")
		}
	}

	return report
}

func deviceRequestsCUDA(device string) bool {
	device = strings.ToLower(strings.TrimSpace(device))
	if device == "" || device == "cpu" || device == "mps" {
		return false
	}
	if device == "cuda" || strings.HasPrefix(device, "cuda:") {
		return true
	}
	device = strings.Trim(device, "[]")
	parts := strings.Split(device, ",")
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(strings.TrimSpace(part)); err != nil {
			return false
		}
	}
	return true
}

func cudaDeviceUnavailableMessage(device string, probe training.RuntimeProbeReport) string {
	message := device + " 需要 CUDA，但当前没有通过张量测试的 NVIDIA CUDA 设备"
	if probe.CUDAReported && strings.TrimSpace(probe.CUDASmokeError) != "" {
		message += "：" + strings.TrimSpace(probe.CUDASmokeError)
	}
	return message
}

func (s *Server) writeDiagnosticsReport(rootPath string, taskIDs []string) (string, error) {
	if err := os.MkdirAll(filepath.Join(s.stateDir, "diagnostics"), 0o755); err != nil {
		return "", err
	}
	rootPath = strings.TrimSpace(rootPath)

	cfg, _ := dataset.LoadTrainingConfig(s.configPath)
	var project *dataset.ProjectState
	var projectErr string
	var trainPreflight *dataset.PreflightReport
	if rootPath != "" {
		if root, err := dataset.NormalizeRoot(rootPath); err == nil {
			rootPath = root
			if state, err := dataset.State(root); err == nil {
				project = state
			} else {
				projectErr = err.Error()
			}
			report := s.buildPreflightReport("train", root, cfg.Model, "", cfg.Device, cfg.ImageSize, 0)
			trainPreflight = &report
		} else {
			projectErr = err.Error()
		}
	}

	labelMe := resolveLabelMeExecutable(s.baseDir, rootPath)
	labelMeExists := false
	if filepath.IsAbs(labelMe) {
		if info, err := os.Stat(labelMe); err == nil && !info.IsDir() {
			labelMeExists = true
		}
	}
	yolo26Model := dataset.DefaultYOLO26Model()
	yolo26ModelExists := false
	if info, err := os.Stat(yolo26Model); err == nil && !info.IsDir() && info.Size() > 0 {
		yolo26ModelExists = true
	}
	yoloRuntime := s.cachedYOLORuntimeProbe()

	taskSnapshots := make([]tasks.Task, 0)
	seen := map[string]bool{}
	for _, id := range taskIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if task, ok := s.tasks.Get(id); ok {
			taskSnapshots = append(taskSnapshots, task.Snapshot())
		}
	}
	if len(taskSnapshots) == 0 {
		for _, task := range s.tasks.List() {
			taskSnapshots = append(taskSnapshots, task.Snapshot())
		}
	}

	wd, _ := os.Getwd()
	executable, _ := os.Executable()
	report := map[string]any{
		"generatedAt": time.Now(),
		"runtime": map[string]any{
			"goos":       runtime.GOOS,
			"goarch":     runtime.GOARCH,
			"baseDir":    s.baseDir,
			"stateDir":   s.stateDir,
			"workingDir": wd,
			"executable": executable,
		},
		"resources": map[string]any{
			"yoloCommand":       s.training.CommandDisplay(),
			"pythonCommand":     s.training.PythonCommand,
			"ultralyticsDir":    s.training.UltralyticsDir,
			"modelDirs":         s.training.ModelDirs,
			"yolo26Model":       yolo26Model,
			"yolo26ModelExists": yolo26ModelExists,
			"yoloRuntime":       yoloRuntime,
			"labelme":           labelMe,
			"labelmeExists":     labelMeExists,
		},
		"config":         cfg,
		"project":        project,
		"projectError":   projectErr,
		"trainPreflight": trainPreflight,
		"tasks":          taskSnapshots,
	}

	name := "diagnostics-" + time.Now().Format("20060102-150405") + ".json"
	path := filepath.Join(s.stateDir, "diagnostics", name)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Server) predictFrame(ctx context.Context, root string, cfg dataset.PredictionConfig) (dataset.PredictionResult, error) {
	model, err := s.resolvePreflightModel(root, cfg.ModelPath)
	if err != nil {
		return dataset.PredictionResult{}, err
	}
	if cfg.ImageSize <= 0 {
		cfg.ImageSize = dataset.DefaultTrainingConfig().ImageSize
	}
	if strings.TrimSpace(cfg.Device) == "" {
		cfg.Device = dataset.DefaultTrainingConfig().Device
	}
	if cfg.Confidence <= 0 {
		cfg.Confidence = 0.25
	}

	paths := dataset.Paths(root)
	runName := "camera-frame-" + time.Now().Format("20060102-150405-000000000")
	outputDir := filepath.Join(paths.PredictDir, runName)
	defer os.RemoveAll(outputDir)

	args := []string{
		"detect", "predict",
		"model=" + model,
		"source=" + cfg.ImagePath,
		"imgsz=" + strconv.Itoa(cfg.ImageSize),
	}
	if !training.IsAutomaticDevice(cfg.Device) {
		args = append(args, "device="+cfg.Device)
	}
	args = append(args,
		"conf="+formatFloatForCommand(cfg.Confidence),
		"save=False",
		"save_txt=True",
		"save_conf=True",
		"project="+paths.PredictDir,
		"name="+runName,
		"exist_ok=False",
	)
	commandArgs := append(append([]string{}, s.training.YOLOCommandArgs...), args...)
	cmd := exec.CommandContext(ctx, s.training.YOLOCommand, commandArgs...)
	cmd.Dir = root
	cmd.Env = s.training.CommandEnvironment()
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return dataset.PredictionResult{}, ctx.Err()
	}
	if err != nil {
		return dataset.PredictionResult{}, fmt.Errorf("实时识别失败: %w%s", err, commandOutputSuffix(output))
	}
	detections := readFrameDetections(root, outputDir, cfg.ImagePath)
	return dataset.PredictionResult{
		RunID:          runName,
		ModelPath:      model,
		SourceImage:    cfg.ImagePath,
		OutputDir:      outputDir,
		Detections:     detections,
		DetectionCount: len(detections),
		RanAt:          time.Now(),
		Device:         cfg.Device,
		ImageSize:      cfg.ImageSize,
		Confidence:     cfg.Confidence,
	}, nil
}

func checkWritableDir(path, key, label string, add func(string, string, string, string, string)) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		add(key, label, "error", "目录无法创建", err.Error())
		return
	}
	probe := filepath.Join(path, ".modeltraining-write-check")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		add(key, label, "error", "目录不可写", err.Error())
		return
	}
	_ = os.Remove(probe)
	add(key, label, "ok", path, "")
}

func levelForMode(mode, expected string) string {
	if mode == expected {
		return "error"
	}
	return "warning"
}

func preflightError(report dataset.PreflightReport) string {
	var issues []string
	for _, check := range report.Checks {
		if check.Level == "error" {
			issues = append(issues, check.Label+"："+check.Message)
		}
	}
	if len(issues) == 0 {
		return "启动体检未通过"
	}
	return "启动体检未通过：" + strings.Join(issues, "；")
}

func (s *Server) cachedYOLORuntimeProbe() training.RuntimeProbeReport {
	key := s.training.CommandDisplay() + "|" + s.training.PythonCommand + "|" + s.training.UltralyticsDir
	now := time.Now()
	s.probeMu.Lock()
	if s.probeCache != nil && s.probeKey == key && now.Sub(s.probeCache.CheckedAt) < 2*time.Minute {
		cached := *s.probeCache
		s.probeMu.Unlock()
		return cached
	}
	probe := s.probeYOLO
	s.probeMu.Unlock()

	if probe == nil {
		return training.RuntimeProbeReport{
			OK:        false,
			Command:   s.training.CommandDisplay(),
			Error:     "YOLO runtime probe is not configured",
			CheckedAt: now,
		}
	}
	// Loading a self-contained CUDA Torch runtime can take noticeably longer
	// on the first cold start because several large DLLs must be mapped.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	report := probe(ctx)
	if report.Command == "" {
		report.Command = s.training.CommandDisplay()
	}
	if report.CheckedAt.IsZero() {
		report.CheckedAt = time.Now()
	}

	s.probeMu.Lock()
	s.probeCache = &report
	s.probeKey = key
	s.probeMu.Unlock()
	return report
}

func countTextFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			count++
		}
	}
	return count, nil
}

func (s *Server) resolvePreflightModel(root, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		if latest, err := dataset.LatestResult(root); err == nil && latest != nil && latest.ModelPath != "" {
			model = latest.ModelPath
		} else {
			model = dataset.DefaultYOLO26Model()
		}
	}
	candidates := []string{}
	if filepath.IsAbs(model) {
		candidates = append(candidates, model)
	} else {
		candidates = append(candidates, filepath.Join(root, model))
		for _, dir := range s.training.ModelDirs {
			candidates = append(candidates, filepath.Join(dir, model))
		}
		candidates = append(candidates,
			filepath.Join(s.baseDir, "third_party", "ultralytics-8.4.10", model),
			filepath.Join(s.baseDir, model),
			model,
		)
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Size() > 0 {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				return filepath.Clean(candidate), nil
			}
			return filepath.Clean(abs), nil
		}
	}
	return "", fmt.Errorf("未找到模型文件: %s", model)
}

func resolveLabelMeExecutable(baseDir, root string) string {
	for _, candidate := range []string{
		os.Getenv("LABELME_PATH"),
		filepath.Join(baseDir, "tools", "labelme", "labelme.exe"),
		filepath.Join(baseDir, "tools", "labelme"),
		filepath.Join(baseDir, "labelme", "labelme.exe"),
		filepath.Join(baseDir, "labelme"),
		filepath.Join(root, "labelme.exe"),
		filepath.Join(baseDir, "labelme.exe"),
		"labelme.exe",
		"labelme",
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			resolved := candidate
			if info, err := os.Stat(resolved); err == nil && info.IsDir() {
				resolved = filepath.Join(resolved, "labelme.exe")
			}
			if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
				return resolved
			}
			continue
		}
		return candidate
	}
	return "labelme"
}

func (s *Server) staticHandler() http.Handler {
	dist := firstExistingPath(
		filepath.Join(s.baseDir, "web", "dist"),
		filepath.Join(s.baseDir, "dist"),
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dist == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><title>ModelTraining</title><p>Frontend is not built yet. Run <code>npm install</code> and <code>npm run build</code> in <code>web</code>.</p>"))
			return
		}
		rel := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
		path := filepath.Join(dist, rel)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			http.ServeFile(w, r, path)
			return
		}
		http.ServeFile(w, r, filepath.Join(dist, "index.html"))
	})
}

func firstExistingPath(candidates ...string) string {
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func readRootRequest(r *http.Request) (string, error) {
	var req struct {
		RootPath string `json:"rootPath"`
	}
	if err := readJSON(r, &req); err != nil {
		return "", err
	}
	return dataset.NormalizeRoot(req.RootPath)
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isFinal(status tasks.Status) bool {
	return status == tasks.StatusSucceeded || status == tasks.StatusFailed || status == tasks.StatusCanceled
}

func supportedImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".bmp":
		return true
	default:
		return false
	}
}

func normalizeExistingImage(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("image path is required")
	}
	if !supportedImageName(path) {
		return "", errors.New("unsupported image type")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("image path must be a file")
	}
	return filepath.Clean(abs), nil
}

func savePredictUpload(root string, files []*multipart.FileHeader) (string, error) {
	if len(files) == 0 {
		return "", errors.New("image file is required")
	}
	header := files[0]
	if !supportedImageName(header.Filename) {
		return "", errors.New("unsupported image type")
	}
	source, err := header.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()

	destDir := dataset.Paths(root).PredictDir
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	base := strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	if base == "" || base == "." {
		base = "image"
	}
	destPath := filepath.Join(destDir, fmt.Sprintf("%s-%s%s", sanitizeFileStem(base), time.Now().Format("20060102-150405.000000000"), ext))
	dest, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		return "", err
	}
	if err := dest.Close(); err != nil {
		return "", err
	}
	return destPath, nil
}

func savePredictFrame(root string, files []*multipart.FileHeader) (string, error) {
	if len(files) == 0 {
		return "", errors.New("image file is required")
	}
	header := files[0]
	source, err := header.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()

	destDir := filepath.Join(dataset.Paths(root).PredictDir, "_camera_frames")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	destPath := filepath.Join(destDir, "frame-"+time.Now().Format("20060102-150405.000000000")+".jpg")
	dest, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		return "", err
	}
	if err := dest.Close(); err != nil {
		return "", err
	}
	return destPath, nil
}

func removePredictUpload(root, imagePath string) {
	predictDir := dataset.Paths(root).PredictDir
	absPredictDir, err := filepath.Abs(predictDir)
	if err != nil {
		return
	}
	absImage, err := filepath.Abs(imagePath)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(absPredictDir, absImage)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return
	}
	_ = os.Remove(absImage)
}

func readFrameDetections(root, outputDir, sourceImage string) []dataset.PredictionDetection {
	classes, _ := dataset.LoadClasses(root)
	labelDir := filepath.Join(outputDir, "labels")
	labelFile := filepath.Join(labelDir, strings.TrimSuffix(filepath.Base(sourceImage), filepath.Ext(sourceImage))+".txt")
	detections := parseFrameLabelFile(labelFile, classes)
	sort.SliceStable(detections, func(i, j int) bool {
		return detections[i].Confidence > detections[j].Confidence
	})
	return detections
}

func parseFrameLabelFile(path string, classes []string) []dataset.PredictionDetection {
	file, err := os.Open(path)
	if err != nil {
		return []dataset.PredictionDetection{}
	}
	defer file.Close()

	var detections []dataset.PredictionDetection
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		classID, err := strconv.Atoi(strings.TrimPrefix(fields[0], "\ufeff"))
		if err != nil {
			continue
		}
		values := make([]float64, 4)
		ok := true
		for i := 0; i < 4; i++ {
			values[i], err = strconv.ParseFloat(fields[i+1], 64)
			if err != nil {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		confidence := 0.0
		if len(fields) >= 6 {
			if parsed, err := strconv.ParseFloat(fields[5], 64); err == nil {
				confidence = parsed
			}
		}
		className := strconv.Itoa(classID)
		if classID >= 0 && classID < len(classes) {
			className = classes[classID]
		}
		detections = append(detections, dataset.PredictionDetection{
			ClassID:    classID,
			ClassName:  className,
			Confidence: confidence,
			XCenter:    values[0],
			YCenter:    values[1],
			Width:      values[2],
			Height:     values[3],
			LabelPath:  path,
		})
	}
	return detections
}

func formatFloatForCommand(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func commandOutputSuffix(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	if len(text) > 1200 {
		text = text[len(text)-1200:]
	}
	return ": " + text
}

func webcamPythonScript() string {
	return training.PythonDeviceResolverScript() + `
import cv2
from ultralytics import YOLO

model_path = sys.argv[1]
camera_index = int(sys.argv[2])
conf = float(sys.argv[3])
imgsz = int(sys.argv[4])
device = sys.argv[5]
device = mt_resolve_device(device)

model = YOLO(model_path)
cap = cv2.VideoCapture(camera_index, cv2.CAP_DSHOW)
if not cap.isOpened():
    cap = cv2.VideoCapture(camera_index)
if not cap.isOpened():
    raise RuntimeError(f"无法打开摄像头 {camera_index}")

cap.set(cv2.CAP_PROP_FRAME_WIDTH, 640)
cap.set(cv2.CAP_PROP_FRAME_HEIGHT, 480)
cap.set(cv2.CAP_PROP_FPS, 30)
cap.set(cv2.CAP_PROP_BUFFERSIZE, 1)

try:
    while True:
        ok, frame = cap.read()
        if not ok:
            break
        results = model.predict(frame, imgsz=imgsz, conf=conf, device=device, verbose=False)
        annotated = results[0].plot()
        ok, encoded = cv2.imencode(".jpg", annotated, [int(cv2.IMWRITE_JPEG_QUALITY), 82])
        if not ok:
            continue
        data = encoded.tobytes()
        sys.stdout.buffer.write(b"--frame\r\n")
        sys.stdout.buffer.write(b"Content-Type: image/jpeg\r\n")
        sys.stdout.buffer.write(b"Content-Length: " + str(len(data)).encode("ascii") + b"\r\n\r\n")
        sys.stdout.buffer.write(data)
        sys.stdout.buffer.write(b"\r\n")
        sys.stdout.buffer.flush()
finally:
    cap.release()
`
}

func sanitizeFileStem(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "image"
	}
	return out
}

func parseIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseFloatDefault(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func openPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is required")
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
