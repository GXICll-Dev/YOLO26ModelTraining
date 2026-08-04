package training

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"modeltraining-go-ts/internal/dataset"
	"modeltraining-go-ts/internal/tasks"
)

type Runner struct {
	YOLOCommand     string
	YOLOCommandArgs []string
	PythonCommand   string
	IsolatedPython  bool
	UltralyticsDir  string
	ModelDirs       []string
}

type RuntimeProbeReport struct {
	OK                 bool      `json:"ok"`
	Command            string    `json:"command"`
	PythonCommand      string    `json:"pythonCommand,omitempty"`
	UltralyticsVersion string    `json:"ultralyticsVersion,omitempty"`
	TorchVersion       string    `json:"torchVersion,omitempty"`
	TorchCUDA          string    `json:"torchCuda,omitempty"`
	CUDAReported       bool      `json:"cudaReportedAvailable"`
	CUDAAvailable      bool      `json:"cudaAvailable"`
	CUDASmokeError     string    `json:"cudaSmokeError,omitempty"`
	CUDADeviceCount    int       `json:"cudaDeviceCount,omitempty"`
	CUDADeviceName     string    `json:"cudaDeviceName,omitempty"`
	CUDAMemoryMiB      int64     `json:"cudaMemoryMiB,omitempty"`
	CommandOutput      string    `json:"commandOutput,omitempty"`
	PythonOutput       string    `json:"pythonOutput,omitempty"`
	Error              string    `json:"error,omitempty"`
	CheckedAt          time.Time `json:"checkedAt"`
}

func NewRunner() Runner {
	configuredPython := normalizeConfiguredCommand(os.Getenv("MT_PYTHON_CMD"))
	if managedRuntimeRequired() && configuredPython == "" {
		return Runner{}
	}
	configuredUltralytics := strings.TrimSpace(os.Getenv("ULTRALYTICS_DIR"))
	ultralyticsDir := resolveUltralyticsDir(configuredUltralytics, configuredPython != "", defaultUltralyticsDirCandidates()...)
	pythonCommand := resolvePythonCommand(os.Getenv("MT_PYTHON_CMD"), exec.LookPath)
	// Prefer the Python entrypoint whenever Python is available so automatic
	// device selection runs inside the same train/predict process. YOLO_CMD
	// remains an explicit source-development escape hatch.
	cmd, commandArgs := resolveYOLOCommand(os.Getenv("YOLO_CMD"), ultralyticsDir, pythonCommand, exec.LookPath)
	return Runner{
		YOLOCommand:     cmd,
		YOLOCommandArgs: commandArgs,
		PythonCommand:   pythonCommand,
		IsolatedPython:  configuredPython != "" && configuredUltralytics == "",
		UltralyticsDir:  ultralyticsDir,
		ModelDirs:       modelSearchDirs(ultralyticsDir),
	}
}

func managedRuntimeRequired() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("MT_MANAGED_RUNTIME_REQUIRED")))
	return value == "1" || value == "true" || value == "yes"
}

func (r Runner) StartTrain(parent context.Context, task *tasks.Task, cfg dataset.TrainingConfig) {
	ctx, cancel := context.WithCancel(parent)
	task.Start(ctx, cancel)
	go r.runTrain(ctx, task, cfg)
}

func (r Runner) StartPredict(parent context.Context, task *tasks.Task, cfg dataset.PredictionConfig) {
	ctx, cancel := context.WithCancel(parent)
	task.Start(ctx, cancel)
	go r.runPredict(ctx, task, cfg)
}

func (r Runner) ProbeRuntime(ctx context.Context) RuntimeProbeReport {
	report := RuntimeProbeReport{
		Command:       r.CommandDisplay(),
		PythonCommand: strings.TrimSpace(r.PythonCommand),
		CheckedAt:     time.Now(),
	}
	if strings.TrimSpace(r.YOLOCommand) == "" {
		report.Error = "YOLO command is empty"
		return report
	}
	if _, err := exec.LookPath(r.YOLOCommand); err != nil {
		report.Error = fmt.Sprintf("YOLO command is not available: %v", err)
		return report
	}

	if !r.yoloRunsThroughPython() {
		output, err := r.runRuntimeCommand(ctx, r.YOLOCommand, append(append([]string{}, r.YOLOCommandArgs...), "version"))
		report.CommandOutput = strings.TrimSpace(string(output))
		if err != nil {
			report.Error = fmt.Sprintf("YOLO command could not report version: %v%s", err, probeOutputSuffix(output))
			return report
		}
		if report.UltralyticsVersion == "" {
			report.UltralyticsVersion = firstLine(report.CommandOutput)
		}
	}

	pythonCommand, err := r.PythonExecutable()
	if err != nil {
		report.Error = "Python command is not available for Ultralytics/Torch probe: " + err.Error()
		return report
	}
	report.PythonCommand = pythonCommand

	output, err := r.runRuntimeCommand(ctx, pythonCommand, []string{"-c", runtimeProbeScript})
	report.PythonOutput = strings.TrimSpace(string(output))
	if err != nil {
		report.Error = fmt.Sprintf("Ultralytics/Torch probe failed: %v%s", err, probeOutputSuffix(output))
		return report
	}
	var parsed struct {
		UltralyticsVersion string `json:"ultralyticsVersion"`
		TorchVersion       string `json:"torchVersion"`
		TorchCUDA          string `json:"torchCuda"`
		CUDAReported       bool   `json:"cudaReportedAvailable"`
		CUDAAvailable      bool   `json:"cudaAvailable"`
		CUDASmokeError     string `json:"cudaSmokeError"`
		CUDADeviceCount    int    `json:"cudaDeviceCount"`
		CUDADeviceName     string `json:"cudaDeviceName"`
		CUDAMemoryMiB      int64  `json:"cudaMemoryMiB"`
	}
	payload, err := runtimeProbePayload(report.PythonOutput)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		report.Error = "Ultralytics/Torch probe returned invalid JSON: " + err.Error()
		return report
	}
	report.UltralyticsVersion = firstNonEmpty(parsed.UltralyticsVersion, report.UltralyticsVersion)
	report.TorchVersion = parsed.TorchVersion
	report.TorchCUDA = parsed.TorchCUDA
	report.CUDAReported = parsed.CUDAReported
	report.CUDAAvailable = parsed.CUDAAvailable
	report.CUDASmokeError = parsed.CUDASmokeError
	report.CUDADeviceCount = parsed.CUDADeviceCount
	report.CUDADeviceName = parsed.CUDADeviceName
	report.CUDAMemoryMiB = parsed.CUDAMemoryMiB
	report.OK = report.UltralyticsVersion != "" && report.TorchVersion != ""
	if !report.OK {
		report.Error = "Ultralytics or Torch version is empty"
	}
	return report
}

func (r Runner) runTrain(ctx context.Context, task *tasks.Task, cfg dataset.TrainingConfig) {
	root, err := dataset.NormalizeRoot(cfg.ProjectRoot)
	if err != nil {
		task.Fail(err)
		return
	}
	cfg.ProjectRoot = root
	cfg.ClearLegacyRuntimeSelection()
	if strings.TrimSpace(cfg.Device) == "" {
		cfg.Device = dataset.DefaultTrainingConfig().Device
	}
	if err := validateConfig(cfg); err != nil {
		task.Fail(err)
		return
	}
	classes, err := dataset.LoadClasses(root)
	if err != nil {
		task.Fail(err)
		return
	}
	if len(classes) == 0 {
		task.Fail(errors.New("classes.txt 为空，请先添加类别"))
		return
	}
	split, err := dataset.WriteDatasetYAMLWithSplit(root, classes)
	if err != nil {
		task.Fail(err)
		return
	}

	paths := dataset.Paths(root)
	model := r.resolveModelPath(root, cfg.Model)
	args := buildYOLOArgs(paths.YamlFile, paths.RunsDir, model, cfg)
	startedAt := time.Now()

	task.Log("正在启动 YOLO26 训练。")
	task.Log(deviceModeMessage(cfg.Device))
	task.Log(fmt.Sprintf("数据集划分：训练 %d 张，验证 %d 张。", split.TrainImages, split.ValImages))
	if split.Mirrored {
		task.Log("样本量较少，验证集会复用训练图片，训练指标仅供参考。")
	}
	if r.UltralyticsDir != "" {
		task.Log("YOLO26 源码目录：" + r.UltralyticsDir)
	}
	if model != cfg.Model {
		task.Log("已解析模型文件：" + model)
	}

	command := r.YOLOCommand
	commandArgs := r.commandArgs(args)
	task.Log("执行命令：" + r.CommandDisplay(args...))
	cmd := exec.CommandContext(ctx, command, commandArgs...)
	cmd.Dir = root
	cmd.Env = r.commandEnv()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		task.Fail(err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		task.Fail(err)
		return
	}
	if err := cmd.Start(); err != nil {
		task.Fail(fmt.Errorf("启动 YOLO 命令失败 %q: %w", command, err))
		return
	}
	task.SetPID(cmd.Process.Pid)

	var wg sync.WaitGroup
	stdoutTail := newRecentLineBuffer(40)
	stderrTail := newRecentLineBuffer(40)
	wg.Add(2)
	go scanPipe(&wg, stdout, task, cfg.Epochs, "stdout", stdoutTail)
	go scanPipe(&wg, stderr, task, cfg.Epochs, "stderr", stderrTail)
	wg.Wait()

	err = cmd.Wait()
	if ctx.Err() != nil {
		task.Log("训练已停止。")
		runDir := latestTrainingRunDirAfter(paths.RunsDir, startedAt)
		r.saveTrainingManifest(task, root, runDir, model, cfg, classes, split, args, "canceled", "训练已停止", startedAt, time.Now())
		task.MarkCanceled()
		return
	}
	if err != nil {
		failure := detailedProcessError("YOLO 训练失败", err, stderrTail, stdoutTail)
		task.Log(fmt.Sprintf("[进程] YOLO 训练进程异常退出：%v。完整 Python 输出见上方原始日志。", err))
		runDir := latestTrainingRunDirAfter(paths.RunsDir, startedAt)
		r.saveTrainingManifest(task, root, runDir, model, cfg, classes, split, args, "failed", failure.Error(), startedAt, time.Now())
		task.Fail(failure)
		return
	}
	result, _ := dataset.LatestResult(root)
	runDir := ""
	if result != nil {
		runDir = result.RunDir
	}
	r.saveTrainingManifest(task, root, runDir, model, cfg, classes, split, args, "succeeded", "", startedAt, time.Now())
	result, _ = dataset.LatestResult(root)
	task.Log("训练完成。")
	task.Succeed(result)
}

func (r Runner) runPredict(ctx context.Context, task *tasks.Task, cfg dataset.PredictionConfig) {
	root, err := dataset.NormalizeRoot(cfg.ProjectRoot)
	if err != nil {
		task.Fail(err)
		return
	}
	cfg.ProjectRoot = root
	cfg.ClearLegacyRuntimeSelection()
	if err := validatePredictionConfig(cfg); err != nil {
		task.Fail(err)
		return
	}
	model, err := r.resolvePredictionModel(root, cfg.ModelPath)
	if err != nil {
		task.Fail(err)
		return
	}
	paths := dataset.Paths(root)
	if err := os.MkdirAll(paths.PredictDir, 0o755); err != nil {
		task.Fail(err)
		return
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

	runName := "predict-" + time.Now().Format("20060102-150405-000000000")
	outputDir := filepath.Join(paths.PredictDir, runName)
	args := buildYOLOPredictArgs(model, cfg.ImagePath, paths.PredictDir, runName, cfg)

	task.SetProgress(5)
	task.Log("正在启动 YOLO26 模型测试。")
	task.Log(deviceModeMessage(cfg.Device))
	if r.UltralyticsDir != "" {
		task.Log("YOLO26 源码目录：" + r.UltralyticsDir)
	}
	task.Log("测试模型：" + model)
	task.Log("测试图片：" + cfg.ImagePath)

	command := r.YOLOCommand
	commandArgs := r.commandArgs(args)
	task.Log("执行命令：" + r.CommandDisplay(args...))

	cmd := exec.CommandContext(ctx, command, commandArgs...)
	cmd.Dir = root
	cmd.Env = r.commandEnv()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		task.Fail(err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		task.Fail(err)
		return
	}
	if err := cmd.Start(); err != nil {
		task.Fail(fmt.Errorf("启动 YOLO 命令失败 %q: %w", r.YOLOCommand, err))
		return
	}
	task.SetPID(cmd.Process.Pid)
	task.SetProgress(25)

	var wg sync.WaitGroup
	stdoutTail := newRecentLineBuffer(40)
	stderrTail := newRecentLineBuffer(40)
	wg.Add(2)
	go scanPipe(&wg, stdout, task, 0, "stdout", stdoutTail)
	go scanPipe(&wg, stderr, task, 0, "stderr", stderrTail)
	wg.Wait()

	err = cmd.Wait()
	if ctx.Err() != nil {
		task.Log("模型测试已停止。")
		task.MarkCanceled()
		return
	}
	if err != nil {
		failure := detailedProcessError("YOLO 模型测试失败", err, stderrTail, stdoutTail)
		task.Log(fmt.Sprintf("[进程] YOLO 模型测试进程异常退出：%v。完整 Python 输出见上方原始日志。", err))
		task.Fail(failure)
		return
	}

	artifacts := findPredictionArtifacts(outputDir)
	detections := readPredictionDetections(root, outputDir, cfg.ImagePath)
	result := dataset.PredictionResult{
		RunID:          runName,
		ModelPath:      model,
		SourceImage:    cfg.ImagePath,
		OutputDir:      outputDir,
		Artifacts:      artifacts,
		Detections:     detections,
		DetectionCount: len(detections),
		RanAt:          time.Now(),
		Device:         cfg.Device,
		ImageSize:      cfg.ImageSize,
		Confidence:     cfg.Confidence,
	}
	if len(artifacts) > 0 {
		result.OutputImage = artifacts[0]
		task.Log("识别结果图：" + artifacts[0])
	} else {
		task.Log("模型测试已完成，但没有在输出目录中找到结果图：" + outputDir)
	}
	if err := dataset.SavePredictionResult(result); err != nil {
		task.Log("模型测试历史保存失败：" + err.Error())
	}
	task.Log(fmt.Sprintf("检测目标数量：%d。", len(detections)))
	task.Log("模型测试完成。")
	task.Succeed(result)
}

func validateConfig(cfg dataset.TrainingConfig) error {
	if cfg.Epochs <= 0 || cfg.Epochs > 500 {
		return errors.New("训练轮数必须在 1 到 500 之间")
	}
	if cfg.BatchSize <= 0 || cfg.BatchSize > 256 {
		return errors.New("批次大小必须在 1 到 256 之间")
	}
	if cfg.ImageSize < 64 || cfg.ImageSize > 4096 {
		return errors.New("图像尺寸必须在 64 到 4096 之间")
	}
	if cfg.Device == "" {
		return errors.New("请选择训练设备")
	}
	return nil
}

func validatePredictionConfig(cfg dataset.PredictionConfig) error {
	if strings.TrimSpace(cfg.ImagePath) == "" {
		return errors.New("请先选择测试图片")
	}
	if info, err := os.Stat(cfg.ImagePath); err != nil {
		return err
	} else if info.IsDir() {
		return errors.New("测试图片路径必须是文件")
	}
	if cfg.ImageSize <= 0 {
		cfg.ImageSize = dataset.DefaultTrainingConfig().ImageSize
	}
	if cfg.ImageSize < 64 || cfg.ImageSize > 4096 {
		return errors.New("图像尺寸必须在 64 到 4096 之间")
	}
	if cfg.Confidence < 0 || cfg.Confidence > 1 {
		return errors.New("置信度必须在 0 到 1 之间")
	}
	return nil
}

func buildYOLOArgs(yamlPath, runsDir, model string, cfg dataset.TrainingConfig) []string {
	args := []string{
		"detect", "train",
		"data=" + yamlPath,
		"model=" + model,
		"epochs=" + strconv.Itoa(cfg.Epochs),
		"batch=" + strconv.Itoa(cfg.BatchSize),
		"imgsz=" + strconv.Itoa(cfg.ImageSize),
	}
	if !IsAutomaticDevice(cfg.Device) {
		args = append(args, "device="+cfg.Device)
	}
	return append(args,
		"lr0="+formatFloat(cfg.LearningRate),
		"momentum="+formatFloat(cfg.Momentum),
		"weight_decay="+formatFloat(cfg.WeightDecay),
		"patience="+strconv.Itoa(cfg.Patience),
		"workers="+strconv.Itoa(cfg.Workers),
		"fliplr="+formatFloat(cfg.FlipLR),
		"flipud="+formatFloat(cfg.FlipUD),
		"mosaic="+formatFloat(cfg.Mosaic),
		"mixup="+formatFloat(cfg.Mixup),
		"copy_paste="+formatFloat(cfg.CopyPaste),
		"augment="+strconv.FormatBool(cfg.Augment),
		"cache=False",
		"pretrained=True",
		"plots=True",
		"save=True",
		"save_period=10",
		"cos_lr=True",
		"project="+runsDir,
		"name=train",
		"exist_ok=False",
	)
}

func buildYOLOPredictArgs(model, sourceImage, predictDir, runName string, cfg dataset.PredictionConfig) []string {
	if cfg.ImageSize <= 0 {
		cfg.ImageSize = dataset.DefaultTrainingConfig().ImageSize
	}
	if strings.TrimSpace(cfg.Device) == "" {
		cfg.Device = dataset.DefaultTrainingConfig().Device
	}
	if cfg.Confidence <= 0 {
		cfg.Confidence = 0.25
	}
	args := []string{
		"detect", "predict",
		"model=" + model,
		"source=" + sourceImage,
		"imgsz=" + strconv.Itoa(cfg.ImageSize),
	}
	if !IsAutomaticDevice(cfg.Device) {
		args = append(args, "device="+cfg.Device)
	}
	return append(args,
		"conf="+formatFloat(cfg.Confidence),
		"save=True",
		"save_txt=True",
		"save_conf=True",
		"project="+predictDir,
		"name="+runName,
		"exist_ok=False",
	)
}

func scanPipe(wg *sync.WaitGroup, pipe any, task *tasks.Task, totalEpochs int, stream string, tail *recentLineBuffer) {
	defer wg.Done()
	reader, ok := pipe.(interface{ Read([]byte) (int, error) })
	if !ok {
		return
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanner.Split(splitConsoleLines)
	for scanner.Scan() {
		line := stripANSI(scanner.Text())
		if strings.TrimSpace(line) == "" {
			continue
		}
		if tail != nil {
			tail.Add(line)
		}
		task.Log(pythonStreamLog(stream, line))
		if progress := parseProgress(line, totalEpochs); progress >= 0 {
			task.SetProgress(progress)
			if localized := localizedYOLOLog(line, totalEpochs, progress); localized != "" {
				task.Log("[YOLO 摘要] " + localized)
			}
			continue
		}
		if localized := localizedYOLOLog(line, totalEpochs, -1); localized != "" {
			task.Log("[YOLO 摘要] " + localized)
		}
	}
	if err := scanner.Err(); err != nil {
		task.Log(fmt.Sprintf("[日志采集] 读取 Python %s 失败：%v", stream, err))
	}
}

type recentLineBuffer struct {
	mu    sync.Mutex
	limit int
	lines []string
}

func newRecentLineBuffer(limit int) *recentLineBuffer {
	if limit <= 0 {
		limit = 40
	}
	return &recentLineBuffer{limit: limit}
}

func (b *recentLineBuffer) Add(line string) {
	if b == nil || strings.TrimSpace(line) == "" {
		return
	}
	b.mu.Lock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.limit {
		b.lines = append([]string(nil), b.lines[len(b.lines)-b.limit:]...)
	}
	b.mu.Unlock()
}

func (b *recentLineBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "\n")
}

func detailedProcessError(label string, err error, stderrTail, stdoutTail *recentLineBuffer) error {
	detail := ""
	source := "stderr"
	if stderrTail != nil {
		detail = strings.TrimSpace(stderrTail.String())
	}
	if detail == "" && stdoutTail != nil {
		detail = strings.TrimSpace(stdoutTail.String())
		source = "stdout"
	}
	if detail == "" {
		return fmt.Errorf("%s：%w", label, err)
	}
	return fmt.Errorf("%s：%w\nPython %s 尾部：\n%s", label, err, source, detail)
}

func pythonStreamLog(stream, line string) string {
	stream = strings.ToLower(strings.TrimSpace(stream))
	if stream != "stderr" {
		stream = "stdout"
	}
	return "[Python " + stream + "] " + line
}

func splitConsoleLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for index, value := range data {
		if value != '\n' && value != '\r' {
			continue
		}
		advance = index + 1
		if value == '\r' && len(data) > index+1 && data[index+1] == '\n' {
			advance++
		}
		return advance, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func localizedYOLOLog(line string, totalEpochs int, progress int) string {
	lower := strings.ToLower(line)
	if totalEpochs > 0 && progress >= 0 {
		matches := epochPattern.FindStringSubmatch(line)
		if len(matches) == 3 {
			return fmt.Sprintf("训练进度：第 %s / %s 轮，约 %d%%。", matches[1], matches[2], progress)
		}
		return fmt.Sprintf("训练进度已更新，约 %d%%。", progress)
	}
	if resolved := localizedDeviceResolutionLog(line); resolved != "" {
		return resolved
	}
	switch {
	case strings.Contains(lower, "traceback") || strings.Contains(lower, "exception") || strings.Contains(lower, "error"):
		return "YOLO 运行时出现错误，请检查模型路径、图片格式、显卡环境和依赖安装。"
	case strings.Contains(lower, "warning"):
		return "YOLO 给出提醒，建议检查数据集、模型文件或运行环境。"
	case strings.Contains(lower, "results saved to"):
		return "YOLO 结果已保存到输出目录。"
	case strings.Contains(lower, "speed:"):
		return "YOLO 速度统计已生成。"
	case strings.Contains(lower, "image ") && strings.Contains(lower, "/"):
		return "YOLO 正在处理测试图片。"
	case strings.Contains(lower, "epoch"):
		return "YOLO 正在准备训练轮次。"
	default:
		return ""
	}
}

func localizedDeviceResolutionLog(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return ""
	}
	switch fields[0] {
	case "MT_DEVICE_RESOLVED":
		if fields[1] == "0" {
			return "运行设备：已通过 CUDA 张量测试并自动选择 NVIDIA CUDA 0。"
		}
		if fields[1] == "cpu" {
			return "运行设备：CUDA 张量测试不可用，已自动回退 CPU。"
		}
	case "MT_DEVICE_VALIDATED":
		return "运行设备：已通过 CUDA 张量测试（" + fields[1] + "）。"
	}
	return ""
}

var epochPattern = regexp.MustCompile(`(?i)\b(\d+)\s*/\s*(\d+)\b`)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}

func parseProgress(line string, totalEpochs int) int {
	if totalEpochs <= 0 {
		return -1
	}
	matches := epochPattern.FindStringSubmatch(line)
	if len(matches) != 3 {
		return -1
	}
	current, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1
	}
	total, err := strconv.Atoi(matches[2])
	if err != nil || total <= 0 || total != totalEpochs || current < 0 || current > total {
		return -1
	}
	return current * 100 / total
}

func (r Runner) resolveModelPath(root, model string) string {
	if strings.TrimSpace(model) == "" {
		model = dataset.DefaultYOLO26Model()
	}
	if filepath.IsAbs(model) {
		return model
	}
	candidates := []string{filepath.Join(root, model)}
	for _, dir := range r.ModelDirs {
		candidates = append(candidates, filepath.Join(dir, model))
	}
	if workingDir, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workingDir, model))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Size() > 0 {
			return candidate
		}
	}
	return model
}

func (r Runner) resolvePredictionModel(root, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model != "" {
		if filepath.IsAbs(model) {
			info, err := os.Stat(model)
			if err != nil {
				return "", fmt.Errorf("模型文件不可访问：%w", err)
			}
			if info.IsDir() || info.Size() == 0 {
				return "", errors.New("模型路径必须是非空文件")
			}
		}
		return r.resolveModelPath(root, model), nil
	}
	latest, err := dataset.LatestResult(root)
	if err != nil {
		return "", err
	}
	if latest != nil && latest.ModelPath != "" {
		if info, err := os.Stat(latest.ModelPath); err == nil && !info.IsDir() {
			return latest.ModelPath, nil
		}
	}
	return r.resolveModelPath(root, dataset.DefaultYOLO26Model()), nil
}

func findPredictionArtifacts(outputDir string) []string {
	var artifacts []string
	_ = filepath.WalkDir(outputDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jpg", ".jpeg", ".png", ".bmp":
			artifacts = append(artifacts, path)
		}
		return nil
	})
	return artifacts
}

func readPredictionDetections(root, outputDir, sourceImage string) []dataset.PredictionDetection {
	classes, _ := dataset.LoadClasses(root)
	labelDir := filepath.Join(outputDir, "labels")
	var labelFiles []string
	if sourceImage != "" {
		sourceLabel := filepath.Join(labelDir, strings.TrimSuffix(filepath.Base(sourceImage), filepath.Ext(sourceImage))+".txt")
		if info, err := os.Stat(sourceLabel); err == nil && !info.IsDir() {
			labelFiles = append(labelFiles, sourceLabel)
		}
	}
	if len(labelFiles) == 0 {
		entries, err := os.ReadDir(labelDir)
		if err != nil {
			return []dataset.PredictionDetection{}
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
				labelFiles = append(labelFiles, filepath.Join(labelDir, entry.Name()))
			}
		}
	}

	var detections []dataset.PredictionDetection
	for _, labelFile := range labelFiles {
		detections = append(detections, parsePredictionLabelFile(labelFile, classes)...)
	}
	sort.SliceStable(detections, func(i, j int) bool {
		return detections[i].Confidence > detections[j].Confidence
	})
	return detections
}

func parsePredictionLabelFile(path string, classes []string) []dataset.PredictionDetection {
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

func (r Runner) saveTrainingManifest(task *tasks.Task, root, runDir, model string, cfg dataset.TrainingConfig, classes []string, split dataset.DatasetSplit, args []string, status, message string, startedAt, finishedAt time.Time) {
	if strings.TrimSpace(runDir) == "" {
		return
	}
	paths := dataset.Paths(root)
	images, _ := dataset.ListImages(root)
	labelCount := countFilesWithExt(paths.LabelsDir, ".txt")
	modelPath := filepath.Join(runDir, "weights", "best.pt")
	if info, err := os.Stat(modelPath); err != nil || info.IsDir() {
		modelPath = ""
	}
	manifest := dataset.TrainingManifest{
		Version:            1,
		RunDir:             runDir,
		ModelPath:          modelPath,
		ResolvedModel:      model,
		Config:             cfg,
		Classes:            append([]string(nil), classes...),
		ImageCount:         len(images),
		ClassCount:         len(classes),
		LabelCount:         labelCount,
		TrainingImages:     split.TrainImages,
		ValidationImages:   split.ValImages,
		ValidationMirrored: split.Mirrored,
		Command:            r.CommandDisplay(args...),
		CommandArgs:        append([]string(nil), r.commandArgs(args)...),
		YOLOCommand:        r.YOLOCommand,
		YOLOCommandArgs:    append([]string(nil), r.YOLOCommandArgs...),
		PythonCommand:      r.PythonCommand,
		UltralyticsDir:     r.UltralyticsDir,
		StartedAt:          startedAt,
		FinishedAt:         finishedAt,
		Status:             status,
		Error:              message,
	}
	if err := dataset.SaveTrainingManifest(manifest); err != nil {
		task.Log("训练记录保存失败：" + err.Error())
		return
	}
	task.Log("训练记录已保存：" + filepath.Join(runDir, "training-manifest.json"))
}

func latestTrainingRunDirAfter(runsDir string, startedAt time.Time) string {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return ""
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(strings.ToLower(entry.Name()), "train") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(startedAt.Add(-2 * time.Second)) {
			continue
		}
		candidates = append(candidates, candidate{path: filepath.Join(runsDir, entry.Name()), mod: info.ModTime()})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mod.After(candidates[j].mod)
	})
	return candidates[0].path
}

func countFilesWithExt(dir, ext string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ext) {
			count++
		}
	}
	return count
}

const runtimeProbePrefix = "MT_RUNTIME_PROBE "

const pythonDeviceResolverScript = `import sys
import torch

def mt_cuda_indices(value):
    normalized = str(value or "").strip().lower()
    if normalized == "cuda":
        return [0]
    if normalized.startswith("cuda:"):
        normalized = normalized.split(":", 1)[1]
    normalized = normalized.strip("[]")
    parts = [part.strip() for part in normalized.split(",") if part.strip()]
    if parts and all(part.isdigit() for part in parts):
        return [int(part) for part in parts]
    return []

def mt_cuda_tensor_smoke(device_index=0):
    if not bool(torch.cuda.is_available()):
        return False, "torch.cuda.is_available() returned false"
    try:
        device = torch.device(f"cuda:{int(device_index)}")
        sample = torch.arange(16, dtype=torch.float32, device=device)
        result = (sample * sample + 1.0).sum()
        torch.cuda.synchronize(device)
        if float(result.item()) <= 0.0:
            raise RuntimeError("CUDA tensor smoke returned an invalid result")
        del result
        del sample
        return True, ""
    except Exception as exc:
        try:
            torch.cuda.empty_cache()
        except Exception:
            pass
        return False, f"{type(exc).__name__}: {exc}"

def mt_resolve_device(value):
    requested = str(value or "").strip()
    normalized = requested.lower()
    if normalized in {"", "auto"}:
        ok, error = mt_cuda_tensor_smoke(0)
        resolved = "0" if ok else "cpu"
        suffix = "" if ok or not error else " " + error.replace("\n", " ")
        print(f"MT_DEVICE_RESOLVED {resolved}{suffix}", file=sys.stderr, flush=True)
        return resolved
    indices = mt_cuda_indices(requested)
    if indices:
        for index in indices:
            ok, error = mt_cuda_tensor_smoke(index)
            if not ok:
                raise RuntimeError(f"CUDA device {index} failed tensor smoke: {error}")
        print(f"MT_DEVICE_VALIDATED {requested}", file=sys.stderr, flush=True)
    return requested
`

// PythonDeviceResolverScript returns the shared child-process device resolver
// used by Python API features implemented in the app package.
func PythonDeviceResolverScript() string {
	return pythonDeviceResolverScript
}

const runtimeProbeScript = pythonDeviceResolverScript + `
import json
import ultralytics

cuda_reported = bool(torch.cuda.is_available())
device_count = int(torch.cuda.device_count()) if cuda_reported else 0
device_name = torch.cuda.get_device_name(0) if device_count else ""
memory_mib = int(torch.cuda.get_device_properties(0).total_memory // (1024 * 1024)) if device_count else 0
cuda_smoke_ok = False
cuda_smoke_error = ""
if cuda_reported and device_count:
    cuda_smoke_ok, cuda_smoke_error = mt_cuda_tensor_smoke(0)
cuda_available = bool(cuda_reported and device_count and cuda_smoke_ok)
print("MT_RUNTIME_PROBE " + json.dumps({
  "ultralyticsVersion": getattr(ultralytics, "__version__", ""),
  "torchVersion": getattr(torch, "__version__", ""),
  "torchCuda": getattr(torch.version, "cuda", None) or "",
  "cudaReportedAvailable": cuda_reported,
  "cudaAvailable": cuda_available,
  "cudaSmokeError": cuda_smoke_error,
  "cudaDeviceCount": device_count,
  "cudaDeviceName": device_name,
  "cudaMemoryMiB": memory_mib
}))`

const ultralyticsEntrypointScript = pythonDeviceResolverScript + `
requested_device = "auto"
device_arg_index = None
for index, argument in enumerate(sys.argv[1:], start=1):
    if argument.startswith("device="):
        requested_device = argument.split("=", 1)[1]
        device_arg_index = index
        break
resolved_device = mt_resolve_device(requested_device)
resolved_argument = "device=" + resolved_device
if device_arg_index is None:
    sys.argv.append(resolved_argument)
else:
    sys.argv[device_arg_index] = resolved_argument
from ultralytics.cfg import entrypoint
entrypoint()`

func (r Runner) runRuntimeCommand(ctx context.Context, command string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = r.commandEnv()
	return cmd.CombinedOutput()
}

func runtimeProbePayload(output string) (string, error) {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(stripANSI(lines[index]))
		if strings.HasPrefix(line, runtimeProbePrefix) {
			payload := strings.TrimSpace(strings.TrimPrefix(line, runtimeProbePrefix))
			if payload != "" {
				return payload, nil
			}
		}
	}
	return "", errors.New("Ultralytics/Torch probe did not return a result marker")
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func probeOutputSuffix(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	if len(text) > 500 {
		text = text[:500]
	}
	return ": " + text
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// IsAutomaticDevice reports whether the child process should run its CUDA
// tensor smoke and resolve the task to CUDA 0 or CPU.
func IsAutomaticDevice(device string) bool {
	device = strings.ToLower(strings.TrimSpace(device))
	return device == "" || device == "auto"
}

func deviceModeMessage(device string) string {
	if IsAutomaticDevice(device) {
		return "运行设备：自动检测（优先使用 NVIDIA CUDA，CUDA 不可用时回退 CPU）。"
	}
	return "运行设备：" + device
}

func modelSearchDirs(ultralyticsDir string) []string {
	var dirs []string
	for _, value := range []string{os.Getenv("YOLO_MODEL_DIR"), ultralyticsDir} {
		for _, dir := range filepath.SplitList(value) {
			dir = strings.TrimSpace(dir)
			if dir != "" {
				dirs = appendUniqueDir(dirs, dir)
			}
		}
	}
	return dirs
}

func resolveYOLOCommand(configured, ultralyticsDir, preferredPython string, lookPath func(string) (string, error)) (string, []string) {
	configured = normalizeConfiguredCommand(configured)
	if configured != "" {
		return configured, nil
	}
	preferredPython = normalizeConfiguredCommand(preferredPython)
	if preferredPython != "" {
		return preferredPython, []string{"-c", ultralyticsEntrypointScript}
	}
	if _, err := lookPath("yolo"); err == nil {
		return "yolo", nil
	}
	if ultralyticsDir != "" {
		for _, candidate := range []string{"python", "python3"} {
			if _, err := lookPath(candidate); err == nil {
				return candidate, []string{"-c", ultralyticsEntrypointScript}
			}
		}
	}
	return "yolo", nil
}

func resolvePythonCommand(modelTrainingCommand string, lookPath func(string) (string, error)) string {
	if configured := normalizeConfiguredCommand(modelTrainingCommand); configured != "" {
		return configured
	}
	for _, candidate := range []string{"python", "python3"} {
		if _, err := lookPath(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func normalizeConfiguredCommand(command string) string {
	command = strings.TrimSpace(command)
	if len(command) >= 2 {
		if (command[0] == '"' && command[len(command)-1] == '"') ||
			(command[0] == '\'' && command[len(command)-1] == '\'') {
			command = strings.TrimSpace(command[1 : len(command)-1])
		}
	}
	return command
}

func (r Runner) yoloRunsThroughPython() bool {
	if len(r.YOLOCommandArgs) < 2 || r.YOLOCommandArgs[0] != "-c" || r.YOLOCommandArgs[1] != ultralyticsEntrypointScript {
		return false
	}
	return sameCommand(r.YOLOCommand, r.PythonCommand)
}

func sameCommand(left, right string) bool {
	left = normalizeConfiguredCommand(left)
	right = normalizeConfiguredCommand(right)
	if left == "" || right == "" {
		return false
	}
	if filepath.IsAbs(left) || filepath.IsAbs(right) {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return strings.EqualFold(left, right)
}

// PythonExecutable returns the single Python runtime used by probes and
// Python-based features such as automatic annotation and webcam prediction.
func (r Runner) PythonExecutable() (string, error) {
	command := normalizeConfiguredCommand(r.PythonCommand)
	if command == "" {
		return "", errors.New("Python command is empty; set MT_PYTHON_CMD")
	}
	if _, err := exec.LookPath(command); err != nil {
		return "", fmt.Errorf("Python command %q is not available: %w", command, err)
	}
	return command, nil
}

func (r Runner) commandArgs(args []string) []string {
	commandArgs := make([]string, 0, len(r.YOLOCommandArgs)+len(args))
	commandArgs = append(commandArgs, r.YOLOCommandArgs...)
	commandArgs = append(commandArgs, args...)
	return commandArgs
}

func (r Runner) CommandDisplay(args ...string) string {
	parts := []string{r.YOLOCommand}
	if r.yoloRunsThroughPython() {
		parts = append(parts, "-c", "<modeltraining-ultralytics-entrypoint>")
	} else {
		parts = append(parts, r.YOLOCommandArgs...)
	}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func defaultUltralyticsDirCandidates() []string {
	var candidates []string
	for _, root := range defaultToolRoots() {
		candidates = append(candidates,
			filepath.Join(root, "third_party", "ultralytics-8.4.10"),
			filepath.Join(root, "ultralytics-8.4.10"),
		)
	}
	return candidates
}

func resolveUltralyticsDir(configured string, configuredPython bool, autoCandidates ...string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return firstExistingDir(configured)
	}
	if configuredPython {
		return ""
	}
	return firstExistingDir(autoCandidates...)
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

func firstExistingDir(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func appendUniqueDir(dirs []string, dir string) []string {
	clean := filepath.Clean(dir)
	for _, existing := range dirs {
		if strings.EqualFold(filepath.Clean(existing), clean) {
			return dirs
		}
	}
	return append(dirs, clean)
}

func (r Runner) commandEnv() []string {
	env := os.Environ()
	if r.IsolatedPython {
		env = setEnv(env, "PYTHONPATH", "")
	}
	if r.UltralyticsDir == "" {
		return env
	}
	pythonPath := r.UltralyticsDir
	if current := os.Getenv("PYTHONPATH"); current != "" {
		pythonPath += string(os.PathListSeparator) + current
	}
	env = setEnv(env, "PYTHONPATH", pythonPath)
	env = setEnv(env, "ULTRALYTICS_DIR", r.UltralyticsDir)
	return env
}

// CommandEnvironment returns the environment shared by all YOLO/Python child
// processes. Packaged runtimes use it to avoid inheriting a machine's stale
// PYTHONPATH, while source mode still prepends the selected Ultralytics tree.
func (r Runner) CommandEnvironment() []string {
	return r.commandEnv()
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), strings.ToUpper(prefix)) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
