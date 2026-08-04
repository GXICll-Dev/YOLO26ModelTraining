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
	"strconv"
	"strings"

	"modeltraining-go-ts/internal/dataset"
	"modeltraining-go-ts/internal/tasks"
)

func (r Runner) StartAutoAnnotate(parent context.Context, task *tasks.Task, cfg dataset.AutoAnnotateConfig) {
	ctx, cancel := context.WithCancel(parent)
	task.Start(ctx, cancel)
	go r.runAutoAnnotate(ctx, task, cfg)
}

func (r Runner) runAutoAnnotate(ctx context.Context, task *tasks.Task, cfg dataset.AutoAnnotateConfig) {
	projectMode := strings.TrimSpace(cfg.ProjectRoot) != ""
	root, imageDir, outputDir, classes, imageCount, err := autoAnnotateInputs(cfg)
	if err != nil {
		task.Fail(err)
		return
	}
	cfg.ProjectRoot = root
	if cfg.ImageSize <= 0 {
		cfg.ImageSize = dataset.DefaultTrainingConfig().ImageSize
	}
	if strings.TrimSpace(cfg.Device) == "" {
		cfg.Device = dataset.DefaultTrainingConfig().Device
	}
	if cfg.Confidence <= 0 {
		cfg.Confidence = 0.25
	}
	if cfg.Confidence < 0 || cfg.Confidence > 1 {
		task.Fail(errors.New("置信度必须在 0 到 1 之间"))
		return
	}
	if cfg.IOU <= 0 {
		cfg.IOU = 0.7
	}
	if cfg.IOU < 0 || cfg.IOU > 1 {
		task.Fail(errors.New("IOU 必须在 0 到 1 之间"))
		return
	}
	model, err := r.resolveAutoAnnotateModel(root, cfg.ModelPath, projectMode)
	if err != nil {
		task.Fail(err)
		return
	}
	if imageCount == 0 {
		task.Fail(errors.New("图片目录为空，无法自动标注"))
		return
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		task.Fail(err)
		return
	}
	pythonCommand, err := r.PythonExecutable()
	if err != nil {
		task.Fail(fmt.Errorf("未找到可用的 Python，无法调用 Ultralytics 自动标注: %w", err))
		return
	}
	classesJSON, _ := json.Marshal(classes)
	args := []string{
		"-u", "-c", autoAnnotatePythonScript(),
		model,
		imageDir,
		outputDir,
		string(classesJSON),
		formatFloat(cfg.Confidence),
		formatFloat(cfg.IOU),
		strconv.Itoa(cfg.ImageSize),
		cfg.Device,
		strconv.FormatBool(cfg.Overwrite),
	}

	task.SetProgress(5)
	task.Log("正在启动自动多边形标注。")
	task.Log(deviceModeMessage(cfg.Device))
	task.Log("自动标注模型：" + model)
	task.Log("图片目录：" + imageDir)
	task.Log("LabelMe 输出目录：" + outputDir)

	cmd := exec.CommandContext(ctx, pythonCommand, args...)
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
		task.Fail(fmt.Errorf("启动自动标注失败 %q: %w", pythonCommand, err))
		return
	}
	task.SetPID(cmd.Process.Pid)
	task.SetProgress(15)

	reportCh := make(chan dataset.AutoAnnotateReport, 1)
	doneCh := make(chan struct{}, 2)
	stdoutTail := newRecentLineBuffer(40)
	stderrTail := newRecentLineBuffer(40)
	go scanAutoAnnotatePipe(stdout, task, imageCount, reportCh, doneCh, "stdout", stdoutTail)
	go scanAutoAnnotatePipe(stderr, task, imageCount, nil, doneCh, "stderr", stderrTail)
	<-doneCh
	<-doneCh

	err = cmd.Wait()
	if ctx.Err() != nil {
		task.Log("自动标注已停止。")
		task.MarkCanceled()
		return
	}
	if err != nil {
		failure := detailedProcessError("Python 自动标注失败", err, stderrTail, stdoutTail)
		task.Log(fmt.Sprintf("[进程] Python 自动标注进程异常退出：%v。完整 Python 输出见上方原始日志。", err))
		task.Fail(failure)
		return
	}
	report := dataset.AutoAnnotateReport{ImageCount: imageCount, OutputDir: outputDir}
	reportReceived := false
	select {
	case parsed := <-reportCh:
		report = parsed
		reportReceived = true
	default:
	}
	if !reportReceived {
		task.Fail(errors.New("自动标注进程未返回结果摘要，请检查 Python/Ultralytics 运行环境"))
		return
	}
	if report.ImageCount == 0 {
		report.ImageCount = imageCount
	}
	if report.OutputDir == "" {
		report.OutputDir = outputDir
	}
	if len(report.Errors) > 0 {
		for index, message := range report.Errors {
			if index >= 10 {
				task.Log(fmt.Sprintf("还有 %d 条自动标注错误未展开，完整列表已保存在任务结果中。", len(report.Errors)-index))
				break
			}
			task.Log("自动标注错误：" + message)
		}
		if report.AnnotatedFiles == 0 {
			task.Fail(autoAnnotateReportError(report))
			return
		}
		task.Log(fmt.Sprintf("自动标注部分完成：生成 %d 个 JSON，%d 张处理失败；完整错误列表已保存在任务结果中。", report.AnnotatedFiles, len(report.Errors)))
	}
	task.SetProgress(100)
	task.Log(fmt.Sprintf("自动标注完成：生成 %d 个 JSON，跳过 %d 张，目标 %d 个。", report.AnnotatedFiles, report.SkippedFiles, report.ShapeCount))
	task.Succeed(report)
}

func autoAnnotateReportError(report dataset.AutoAnnotateReport) error {
	if len(report.Errors) == 0 || report.AnnotatedFiles > 0 {
		return nil
	}
	detail := strings.TrimSpace(report.Errors[0])
	if detail == "" {
		detail = "未提供详细错误"
	}
	return fmt.Errorf("自动标注失败：%d 张图片处理出错，未生成任何 JSON；首个错误：%s", len(report.Errors), detail)
}

func autoAnnotateInputs(cfg dataset.AutoAnnotateConfig) (root, imageDir, outputDir string, classes []string, imageCount int, err error) {
	if strings.TrimSpace(cfg.ProjectRoot) != "" {
		root, err = dataset.NormalizeRoot(cfg.ProjectRoot)
		if err != nil {
			return "", "", "", nil, 0, err
		}
		paths := dataset.Paths(root)
		imageDir = paths.ImagesDir
		outputDir = paths.LabelMeDir
		classes, _ = dataset.LoadClasses(root)
		images, listErr := dataset.ListImages(root)
		if listErr != nil {
			return "", "", "", nil, 0, listErr
		}
		return root, imageDir, outputDir, classes, len(images), nil
	}
	imageDir, err = normalizeExistingDir(cfg.ImageDir)
	if err != nil {
		return "", "", "", nil, 0, fmt.Errorf("图片目录不可用: %w", err)
	}
	outputDir = strings.TrimSpace(cfg.OutputDir)
	if outputDir == "" {
		return "", "", "", nil, 0, errors.New("请选择 LabelMe 输出目录")
	}
	if outputDir, err = filepath.Abs(outputDir); err != nil {
		return "", "", "", nil, 0, err
	}
	outputDir = filepath.Clean(outputDir)
	root = filepath.Dir(imageDir)
	imageCount, err = countImagesInDir(imageDir)
	return root, imageDir, outputDir, []string{}, imageCount, err
}

func (r Runner) resolveAutoAnnotateModel(root, model string, projectMode bool) (string, error) {
	model = strings.TrimSpace(model)
	if projectMode {
		return r.resolvePredictionModel(root, model)
	}
	if model == "" {
		model = dataset.DefaultYOLO26Model()
	}
	resolved := r.resolveModelPath(root, model)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("模型文件不可访问: %w", err)
	}
	if info.IsDir() || info.Size() == 0 {
		return "", errors.New("模型路径必须是非空文件")
	}
	return resolved, nil
}

func normalizeExistingDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("路径为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("路径必须是目录")
	}
	return abs, nil
}

func countImagesInDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".jpg", ".jpeg", ".png", ".bmp":
			count++
		}
	}
	return count, nil
}

func scanAutoAnnotatePipe(pipe any, task *tasks.Task, total int, reportCh chan<- dataset.AutoAnnotateReport, doneCh chan<- struct{}, stream string, tail *recentLineBuffer) {
	defer func() { doneCh <- struct{}{} }()
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
		if strings.HasPrefix(line, "AUTO_ANNOTATE_RESULT ") {
			if reportCh != nil {
				var report dataset.AutoAnnotateReport
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "AUTO_ANNOTATE_RESULT ")), &report); err == nil {
					reportCh <- report
				}
			}
			continue
		}
		if strings.HasPrefix(line, "AUTO_ANNOTATE_PROGRESS ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				current, _ := strconv.Atoi(parts[1])
				shapes, _ := strconv.Atoi(parts[3])
				if total > 0 {
					task.SetProgress(15 + current*80/total)
				}
				task.Log(fmt.Sprintf("[自动标注摘要] 自动标注进度：%d/%d，当前目标 %d 个。", current, total, shapes))
			}
			continue
		}
		if localized := localizedDeviceResolutionLog(line); localized != "" {
			task.Log("[YOLO 摘要] " + localized)
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		task.Log(fmt.Sprintf("[日志采集] 读取 Python %s 失败：%v", stream, err))
	}
}

func autoAnnotatePythonScript() string {
	return pythonDeviceResolverScript + `
import json
import os
from pathlib import Path

import cv2
import base64
from ultralytics import YOLO

model_path, images_dir, labelme_dir, classes_json, conf, prediction_iou, imgsz, device, overwrite = sys.argv[1:10]
classes = json.loads(classes_json)
conf = float(conf)
prediction_iou = float(prediction_iou)
imgsz = int(imgsz)
device = mt_resolve_device(device)
overwrite = overwrite.lower() == "true"
image_exts = {".jpg", ".jpeg", ".png", ".bmp"}
image_paths = sorted([p for p in Path(images_dir).iterdir() if p.is_file() and p.suffix.lower() in image_exts])
Path(labelme_dir).mkdir(parents=True, exist_ok=True)
model = YOLO(model_path)
annotated = 0
skipped = 0
shape_total = 0
errors = []

def class_name(class_id):
    if 0 <= class_id < len(classes):
        return classes[class_id]
    names = getattr(model, "names", {})
    if isinstance(names, dict):
        return str(names.get(class_id, class_id))
    if isinstance(names, list) and 0 <= class_id < len(names):
        return str(names[class_id])
    return str(class_id)

def clamp_point(x, y, width, height):
    x = max(0.0, min(float(width - 1), float(x)))
    y = max(0.0, min(float(height - 1), float(y)))
    return [x, y]

for index, image_path in enumerate(image_paths, start=1):
    out_path = Path(labelme_dir) / (image_path.stem + ".json")
    if out_path.exists() and not overwrite:
        skipped += 1
        print(f"AUTO_ANNOTATE_PROGRESS {index} skipped 0", flush=True)
        continue
    image = cv2.imread(str(image_path))
    if image is None:
        skipped += 1
        errors.append(f"{image_path.name}: 图片无法读取")
        print(f"AUTO_ANNOTATE_PROGRESS {index} error 0", flush=True)
        continue
    height, width = image.shape[:2]
    try:
        results = model.predict(str(image_path), imgsz=imgsz, conf=conf, iou=prediction_iou, device=device, verbose=False)
    except Exception as exc:
        skipped += 1
        errors.append(f"{image_path.name}: {exc}")
        print(f"AUTO_ANNOTATE_PROGRESS {index} error 0", flush=True)
        continue
    shapes = []
    for result in results:
        boxes = getattr(result, "boxes", None)
        masks = getattr(result, "masks", None)
        cls_values = []
        if boxes is not None and boxes.cls is not None:
            cls_values = [int(v) for v in boxes.cls.detach().cpu().tolist()]
        mask_polygons = []
        if masks is not None and getattr(masks, "xy", None) is not None:
            mask_polygons = masks.xy
        if mask_polygons:
            for det_index, polygon in enumerate(mask_polygons):
                class_id = cls_values[det_index] if det_index < len(cls_values) else 0
                points = [clamp_point(x, y, width, height) for x, y in polygon.tolist()]
                if len(points) >= 3:
                    shapes.append({
                        "label": class_name(class_id),
                        "points": points,
                        "group_id": None,
                        "description": "",
                        "shape_type": "polygon",
                        "flags": {},
                        "mask": None
                    })
            continue
        if boxes is not None and boxes.xyxy is not None:
            xyxy_values = boxes.xyxy.detach().cpu().tolist()
            for det_index, box in enumerate(xyxy_values):
                if len(box) < 4:
                    continue
                x1, y1, x2, y2 = box[:4]
                class_id = cls_values[det_index] if det_index < len(cls_values) else 0
                points = [
                    clamp_point(x1, y1, width, height),
                    clamp_point(x2, y1, width, height),
                    clamp_point(x2, y2, width, height),
                    clamp_point(x1, y2, width, height)
                ]
                shapes.append({
                    "label": class_name(class_id),
                    "points": points,
                    "group_id": None,
                    "description": "",
                    "shape_type": "polygon",
                    "flags": {},
                    "mask": None
                })
    deduped = []
    for shape in shapes:
        pts = shape["points"]
        key_pts = tuple(tuple(round(v, 1) for v in p) for p in pts)
        key = (shape["label"], key_pts)
        is_dup = False
        for existing in deduped:
            e_pts = existing["points"]
            e_key = (existing["label"], tuple(tuple(round(v, 1) for v in p) for p in e_pts))
            if key == e_key:
                is_dup = True
                break
            if shape["label"] != existing["label"]:
                continue
            ax1 = min(p[0] for p in pts)
            ay1 = min(p[1] for p in pts)
            ax2 = max(p[0] for p in pts)
            ay2 = max(p[1] for p in pts)
            bx1 = min(p[0] for p in e_pts)
            by1 = min(p[1] for p in e_pts)
            bx2 = max(p[0] for p in e_pts)
            by2 = max(p[1] for p in e_pts)
            ix1 = max(ax1, bx1)
            iy1 = max(ay1, by1)
            ix2 = min(ax2, bx2)
            iy2 = min(ay2, by2)
            if ix2 > ix1 and iy2 > iy1:
                inter = (ix2 - ix1) * (iy2 - iy1)
                area_a = (ax2 - ax1) * (ay2 - ay1)
                area_b = (bx2 - bx1) * (by2 - by1)
                overlap_iou = inter / (area_a + area_b - inter + 1e-6)
                if overlap_iou > 0.5:
                    is_dup = True
                    break
        if not is_dup:
            deduped.append(shape)
    shapes = deduped
    annotation = {
        "version": "5.0.1",
        "flags": {},
        "shapes": shapes,
        "imagePath": image_path.name,
        "imageData": base64.b64encode(image_path.read_bytes()).decode("utf-8"),
        "imageHeight": height,
        "imageWidth": width
    }
    out_path.write_text(json.dumps(annotation, ensure_ascii=False, indent=2), encoding="utf-8")
    annotated += 1
    shape_total += len(shapes)
    print(f"AUTO_ANNOTATE_PROGRESS {index} written {len(shapes)}", flush=True)

print("AUTO_ANNOTATE_RESULT " + json.dumps({
    "imageCount": len(image_paths),
    "annotatedFiles": annotated,
    "skippedFiles": skipped,
    "shapeCount": shape_total,
    "outputDir": str(Path(labelme_dir)),
    "errors": errors
}, ensure_ascii=False), flush=True)
`
}
