import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode, type RefObject } from "react";
import { api, query } from "./api";
import type { AppUpdateStatus, AutoAnnotateReport, ConvertReport, PredictionResult, PreflightReport, ProjectPaths, ProjectState, RuntimeStatus, Task, TrainingConfig, TrainingResult, ValidationReport } from "./types";

type Tab = "directory" | "logs" | "test" | "annotate";
type LogScope = "main" | "predict";
type ClassDialogState = { mode: "add" | "edit" | "delete"; value: string; index?: number; error?: string } | null;
type ConfigUpdater = <K extends keyof TrainingConfig>(key: K, value: TrainingConfig[K]) => void;
type LayoutRect = { id: string; label: string; x: number; y: number; width: number; height: number };
type DragState = { id: string; mode: "move" | "resize"; startX: number; startY: number; startRect: LayoutRect };

declare global {
  interface Window {
    electronWindow?: {
      minimize: () => Promise<void>;
      close: () => Promise<void>;
      chooseDirectory?: () => Promise<string>;
      chooseModelFile?: () => Promise<string>;
      chooseImageFiles?: () => Promise<string[]>;
      onToggleLayoutInspector?: (callback: () => void) => () => void;
    };
    electronDesktop?: {
      getAppVersion: () => Promise<string>;
      runtime: {
        getStatus: () => Promise<RuntimeStatus | null>;
        refresh: () => Promise<RuntimeStatus | null>;
        install: () => Promise<RuntimeStatus>;
        cancel: () => Promise<boolean>;
        onStatus: (callback: (status: RuntimeStatus) => void) => () => void;
      };
      update: {
        getStatus: () => Promise<AppUpdateStatus | null>;
        check: () => Promise<AppUpdateStatus | null>;
        download: () => Promise<AppUpdateStatus>;
        cancel: () => Promise<boolean>;
        install: () => Promise<AppUpdateStatus>;
        onStatus: (callback: (status: AppUpdateStatus) => void) => () => void;
      };
    };
  }
}

const browserRuntimeStatus: RuntimeStatus = {
  phase: "ready",
  ready: true,
  source: "development",
  runtimeId: "development",
  localVersion: "development",
  availableVersion: "",
  runtimeRoot: "",
  manifestURL: "",
  downloadedBytes: 0,
  totalBytes: 0,
  percent: 100,
  currentPackage: "",
  message: "开发模式运行环境",
  error: ""
};

const defaultUpdateStatus: AppUpdateStatus = {
  phase: "idle",
  currentVersion: "0.3.2",
  latestVersion: "0.3.2",
  updateAvailable: false,
  releaseName: "",
  releaseNotes: "",
  publishedAt: "",
  installerSize: 0,
  downloadedBytes: 0,
  percent: 0,
  message: "",
  error: ""
};

const defaultConfig: TrainingConfig = {
  version: 2,
  epochs: 50,
  batchSize: 4,
  imageSize: 640,
  device: "auto",
  learningRate: 0.01,
  momentum: 0.937,
  weightDecay: 0.0005,
  patience: 20,
  workers: 2,
  fliplr: 0.5,
  flipud: 0,
  mosaic: 1,
  mixup: 0,
  copyPaste: 0,
  augment: true,
  model: ""
};

function portableTrainingConfig(value: TrainingConfig): TrainingConfig {
  return {
    version: value.version,
    projectRoot: value.projectRoot,
    epochs: value.epochs,
    batchSize: value.batchSize,
    imageSize: value.imageSize,
    device: value.device,
    learningRate: value.learningRate,
    momentum: value.momentum,
    weightDecay: value.weightDecay,
    patience: value.patience,
    workers: value.workers,
    fliplr: value.fliplr,
    flipud: value.flipud,
    mosaic: value.mosaic,
    mixup: value.mixup,
    copyPaste: value.copyPaste,
    augment: value.augment,
    model: value.model
  };
}

const finalStatuses = new Set(["succeeded", "failed", "canceled"]);
const storageKeys = {
  projectRoot: "modeltraining.lastProjectRoot",
  testModelPath: "modeltraining.testModelPath",
  taskId: "modeltraining.activeTaskId",
  predictTaskId: "modeltraining.activePredictTaskId"
};

export default function App() {
  const [tab, setTab] = useState<Tab>("directory");
  const [paramsOpen, setParamsOpen] = useState(false);
  const [rootPath, setRootPath] = useState("");
  const [project, setProject] = useState<ProjectState | null>(null);
  const [classes, setClasses] = useState<string[]>([]);
  const [selectedClass, setSelectedClass] = useState<number | null>(null);
  const [classDialog, setClassDialog] = useState<ClassDialogState>(null);
  const [selectedRunDir, setSelectedRunDir] = useState("");
  const [selectedPredictionRunId, setSelectedPredictionRunId] = useState("");
  const [config, setConfig] = useState<TrainingConfig>(defaultConfig);
  const [task, setTask] = useState<Task | null>(null);
  const [convertTask, setConvertTask] = useState<Task | null>(null);
  const [validateTask, setValidateTask] = useState<Task | null>(null);
  const [predictTask, setPredictTask] = useState<Task | null>(null);
  const [taskHistory, setTaskHistory] = useState<Task[]>([]);
  const [logScope, setLogScope] = useState<LogScope>("main");
  const [preflight, setPreflight] = useState<PreflightReport | null>(null);
  const [prediction, setPrediction] = useState<PredictionResult | null>(null);
  const [testModelPath, setTestModelPath] = useState("");
  const [autoModelPath, setAutoModelPath] = useState("");
  const [autoImageDir, setAutoImageDir] = useState("");
  const [autoOutputDir, setAutoOutputDir] = useState("");
  const [autoConfidence, setAutoConfidence] = useState(0.25);
  const [autoIou, setAutoIou] = useState(0.7);
  const [autoImageSize, setAutoImageSize] = useState(640);
  const [autoDevice, setAutoDevice] = useState("auto");
  const [autoOverwrite, setAutoOverwrite] = useState(false);
  const [confidence, setConfidence] = useState(0.25);
  const [cameras, setCameras] = useState<MediaDeviceInfo[]>([]);
  const [selectedCameraId, setSelectedCameraId] = useState("");
  const [cameraStreamUrl, setCameraStreamUrl] = useState("");
  const [cameraError, setCameraError] = useState("");
  const [message, setMessage] = useState("就绪");
  const [busy, setBusy] = useState(false);
  const [layoutInspectorOpen, setLayoutInspectorOpen] = useState(false);
  const [appVersion, setAppVersion] = useState("0.3.2");
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimeStatus>(browserRuntimeStatus);
  const [runtimeDialogOpen, setRuntimeDialogOpen] = useState(false);
  const [updateStatus, setUpdateStatus] = useState<AppUpdateStatus>(defaultUpdateStatus);
  const [updateDialogOpen, setUpdateDialogOpen] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const classDialogInputRef = useRef<HTMLInputElement | null>(null);
  const testFileInputRef = useRef<HTMLInputElement | null>(null);
  const eventSourceRef = useRef<EventSource | null>(null);
  const predictEventSourceRef = useRef<EventSource | null>(null);
  const taskPollRef = useRef<number | null>(null);
  const predictPollRef = useRef<number | null>(null);
  const logRef = useRef<HTMLDivElement | null>(null);
  const autoLogRef = useRef<HTMLDivElement | null>(null);
  const autoModelTouchedRef = useRef(false);

  useEffect(() => {
    api<TrainingConfig>("/api/config")
      .then((loaded) => setConfig(portableTrainingConfig({ ...defaultConfig, ...loaded })))
      .catch((err) => setMessage(err.message));
    const savedRoot = readStoredValue(storageKeys.projectRoot);
    if (savedRoot) {
      setRootPath(savedRoot);
      void openProjectAt(savedRoot, { restored: true });
    }
    void restoreStoredTasks();
    void refreshCameras();
    return () => {
      eventSourceRef.current?.close();
      predictEventSourceRef.current?.close();
      clearStoredTaskPolls();
      stopCamera();
    };
  }, []);

  useEffect(() => {
    const desktop = window.electronDesktop;
    if (!desktop) return;
    let active = true;
    void desktop.getAppVersion().then((version) => {
      if (active && version) setAppVersion(version);
    });
    void desktop.runtime.getStatus().then((status) => {
      if (!active || !status) return;
      setRuntimeStatus(status);
      if (!status.ready && status.phase !== "checking") setRuntimeDialogOpen(true);
    });
    void desktop.update.getStatus().then((status) => {
      if (active && status) setUpdateStatus(status);
    });
    const unsubscribeRuntime = desktop.runtime.onStatus((status) => {
      if (!active) return;
      setRuntimeStatus(status);
      if (!status.ready && status.phase !== "checking") setRuntimeDialogOpen(true);
    });
    const unsubscribeUpdate = desktop.update.onStatus((status) => {
      if (active) setUpdateStatus(status);
    });
    return () => {
      active = false;
      unsubscribeRuntime();
      unsubscribeUpdate();
    };
  }, []);

  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight;
    }
  }, [taskHistory]);

  useEffect(() => {
    const handleLayoutShortcut = (event: KeyboardEvent) => {
      if (event.ctrlKey && event.code === "Space") {
        event.preventDefault();
        setLayoutInspectorOpen((current) => !current);
      }
    };
    window.addEventListener("keydown", handleLayoutShortcut);
    return () => window.removeEventListener("keydown", handleLayoutShortcut);
  }, []);

  useEffect(() => {
    return window.electronWindow?.onToggleLayoutInspector?.(() => {
      setLayoutInspectorOpen((current) => !current);
    });
  }, []);

  useEffect(() => {
    if (classDialog?.mode !== "delete") {
      window.setTimeout(() => classDialogInputRef.current?.focus(), 0);
    }
  }, [classDialog]);

  const trainingRuns = project?.trainingRuns ?? [];
  const predictionRuns = project?.predictionRuns ?? [];
  const latest = project?.latestRun ?? null;
  const selectedResult = trainingRuns.find((run) => run.runDir === selectedRunDir) ?? latest;
  const running = task?.status === "running" || task?.status === "pending";
  const predicting = predictTask?.status === "running" || predictTask?.status === "pending";
  const logTask = logScope === "predict" ? predictTask : task;
  const logs = useMemo(() => taskLogs(logTask), [logTask]);
  const autoAnnotateLogs = useMemo(() => {
    const types = new Set(["auto-annotate", "convert-voc"]);
    return taskHistory
      .filter((t) => types.has(t.type))
      .flatMap((t) => {
        const entries = (t.logs ?? []).map((log) => ({ at: log.at, message: log.message }));
        if (t.type !== "auto-annotate" || !finalStatuses.has(t.status)) return entries;
        const report = readAutoAnnotateReport(t);
        if (!report) return entries;
        const at = t.finishedAt || t.startedAt || t.createdAt || new Date(0).toISOString();
        const visibleErrors = (report.errors ?? []).slice(0, 10);
        const resultEntries = visibleErrors.map((error) => ({ at, message: `自动标注错误：${error}` }));
        if ((report.errors?.length ?? 0) > visibleErrors.length) {
          resultEntries.push({ at, message: `另有 ${(report.errors?.length ?? 0) - visibleErrors.length} 条错误，完整内容保存在任务结果中。` });
        }
        if (report.annotatedFiles === 0) {
          resultEntries.push({ at, message: "自动标注未生成任何 JSON，请检查模型、设备和图片内容。" });
        }
        return [...entries, ...resultEntries];
      })
      .sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());
  }, [taskHistory]);
  useEffect(() => {
    if (autoLogRef.current) {
      autoLogRef.current.scrollTop = autoLogRef.current.scrollHeight;
    }
  }, [autoAnnotateLogs]);
  const runningLogTask = taskHistory.find((item) => item.status === "running" || item.status === "pending") ?? logTask;
  const logRunning = runningLogTask?.status === "running" || runningLogTask?.status === "pending";
  const latestAutoAnnotateTask = taskHistory.find((item) => item.type === "auto-annotate") ?? null;
  const sampleHint = useMemo(() => sampleRecommendation(project?.sampleCount ?? 0), [project?.sampleCount]);
  const activeModelPath = testModelPath.trim() || selectedResult?.modelPath || latest?.modelPath || config.model;
  const activeModelSource = testModelPath.trim() ? "手动选择" : selectedResult?.modelPath ? "选中训练记录" : latest?.modelPath ? "最新训练记录" : config.model ? "训练模型" : "未选择";
  const suggestedAutoModelPath = latest?.modelPath?.trim() || config.model.trim();
  const autoOutcome = describeAutoAnnotateTask(latestAutoAnnotateTask);
  const preflightErrors = preflight?.checks.filter((check) => check.level === "error") ?? [];
  const preflightWarnings = preflight?.checks.filter((check) => check.level === "warning") ?? [];
  const protectedTaskLabel = closeProtectionLabel(running, predicting);
  const runtimeReady = runtimeStatus.ready;
  const runtimeBusy = ["downloading", "verifying", "installing", "validating"].includes(runtimeStatus.phase);
  const updateBusy = ["downloading", "verifying", "installing"].includes(updateStatus.phase);

  useEffect(() => {
    if (!autoModelTouchedRef.current && suggestedAutoModelPath) {
      setAutoModelPath(suggestedAutoModelPath);
    }
  }, [suggestedAutoModelPath]);

  useEffect(() => {
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!running && !predicting) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", handleBeforeUnload);
    return () => window.removeEventListener("beforeunload", handleBeforeUnload);
  }, [running, predicting]);

  async function runAction<T>(action: () => Promise<T>) {
    setBusy(true);
    try {
      return await action();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
      return undefined;
    } finally {
      setBusy(false);
    }
  }

  function requireRuntime() {
    if (runtimeReady) return true;
    setRuntimeDialogOpen(true);
    setMessage("请先下载并自动部署 Python/PyTorch/CUDA 运行环境。");
    return false;
  }

  async function refreshRuntimeStatus() {
    try {
      const status = await window.electronDesktop?.runtime.refresh();
      if (status) setRuntimeStatus(status);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
    }
  }

  async function installRuntime() {
    if (!window.electronDesktop) return;
    setRuntimeDialogOpen(true);
    try {
      await window.electronDesktop.runtime.install();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
    }
  }

  async function cancelRuntimeInstall() {
    await window.electronDesktop?.runtime.cancel();
  }

  async function checkForUpdates() {
    setUpdateDialogOpen(true);
    try {
      const status = await window.electronDesktop?.update.check();
      if (status) setUpdateStatus(status);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
    }
  }

  async function downloadUpdate() {
    if (!window.electronDesktop) return;
    try {
      await window.electronDesktop.update.download();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
    }
  }

  async function installUpdate() {
    if (!window.electronDesktop) return;
    if (running || predicting) {
      const confirmed = window.confirm(`${protectedTaskLabel}，更新会关闭软件并中断当前任务。\n\n确定继续更新吗？`);
      if (!confirmed) return;
    }
    try {
      await window.electronDesktop.update.install();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
    }
  }

  async function restoreStoredTasks() {
    const taskId = readStoredValue(storageKeys.taskId);
    const predictTaskId = readStoredValue(storageKeys.predictTaskId);
    await Promise.all([
      taskId ? restoreTaskById(taskId, "main") : Promise.resolve(),
      predictTaskId ? restoreTaskById(predictTaskId, "predict") : Promise.resolve()
    ]);
  }

  async function restoreTaskById(id: string, scope: LogScope) {
    try {
      const restored = await api<Task>(`/api/tasks/${id}`);
      if (scope === "predict") {
        handlePredictTaskSnapshot(restored, { restored: true });
        if (!finalStatuses.has(restored.status)) {
          setLogScope("predict");
          setTab("test");
          watchPredictTask(restored.id);
        }
      } else {
        handleTaskSnapshot(restored, { restored: true });
        if (!finalStatuses.has(restored.status)) {
          setLogScope("main");
          setTab("logs");
          watchTask(restored.id);
        }
      }
    } catch {
      writeStoredValue(scope === "predict" ? storageKeys.predictTaskId : storageKeys.taskId, "");
    }
  }

  async function openProjectAt(path: string, options?: { restored?: boolean }) {
    const loadedState = await runAction(() =>
      api<ProjectState>("/api/projects/open", {
        method: "POST",
        body: JSON.stringify({ rootPath: path })
      })
    );
    const state = loadedState ? normalizeProjectState(loadedState) : undefined;
    if (state) {
      setProject(state);
      setRootPath(state.rootPath);
      setClasses(state.classes);
      setSelectedClass(null);
      setSelectedRunDir(state.latestRun?.runDir ?? "");
      setSelectedPredictionRunId(state.predictionRuns[0]?.runId ?? "");
      restoreLatestPrediction(state);
      void restoreProjectTaskHistory(state.rootPath);
      writeStoredValue(storageKeys.projectRoot, state.rootPath);
      setMessage(options?.restored ? "已恢复上次项目。" : "项目已载入，目录结构和 mydata.yaml 已同步。");
    }
  }

  async function restoreProjectTaskHistory(projectRoot: string) {
    try {
      const restoredTasks = await api<Task[]>(`/api/tasks?${query({ rootPath: projectRoot })}`);
      const sorted = restoredTasks
        .slice()
        .sort((a, b) => latestTaskTime(b) - latestTaskTime(a));
      setTaskHistory(sorted);
      const latestMain = sorted.find((item) => item.type !== "predict");
      const latestConvert = sorted.find((item) => item.type === "convert-voc");
      const latestValidate = sorted.find((item) => item.type === "validate-labels");
      if (latestMain) setTask(latestMain);
      if (latestConvert && !finalStatuses.has(latestConvert.status)) setConvertTask(latestConvert);
      if (latestValidate && !finalStatuses.has(latestValidate.status)) setValidateTask(latestValidate);
    } catch {
      // 历史日志是辅助信息，读取失败不影响项目使用。
    }
  }

  async function openProject() {
    if (!rootPath.trim()) {
      setMessage("请选择项目根目录。");
      return;
    }
    await openProjectAt(rootPath);
  }

  async function browseProjectRoot() {
    if (!window.electronWindow?.chooseDirectory) {
      await openProject();
      return;
    }
    const selected = await runAction(() => window.electronWindow!.chooseDirectory!());
    if (!selected) return;
    setRootPath(selected);
    await openProjectAt(selected);
  }

  function updateTestModelPath(value: string) {
    setTestModelPath(value);
    writeStoredValue(storageKeys.testModelPath, value);
  }

  async function refreshProject(options?: { quiet?: boolean }) {
    if (!rootPath) return;
    const loadedState = await runAction(() =>
      api<ProjectState>(`/api/projects/state?${query({ rootPath })}`)
    );
    const state = loadedState ? normalizeProjectState(loadedState) : undefined;
    if (state) {
      setProject(state);
      setClasses(state.classes);
      setSelectedClass((current) => (current !== null && current < state.classes.length ? current : null));
      setSelectedRunDir((current) => state.trainingRuns.some((run) => run.runDir === current) ? current : state.latestRun?.runDir ?? "");
      setSelectedPredictionRunId((current) => state.predictionRuns.some((run) => run.runId === current) ? current : state.predictionRuns[0]?.runId ?? "");
      if (!options?.quiet) {
        setMessage("目录已刷新。");
      }
    }
  }

  async function ensureProjectReady() {
    if (project) return project;
    if (!rootPath.trim()) {
      setMessage("请先选择项目根目录。");
      return null;
    }
    const loadedState = await runAction(() =>
      api<ProjectState>("/api/projects/open", {
        method: "POST",
        body: JSON.stringify({ rootPath: rootPath.trim() })
      })
    );
    const state = loadedState ? normalizeProjectState(loadedState) : undefined;
    if (!state) return null;
    setProject(state);
    setRootPath(state.rootPath);
    setClasses(state.classes);
    setSelectedClass((current) => (current !== null && current < state.classes.length ? current : null));
    setSelectedRunDir(state.latestRun?.runDir ?? "");
    setSelectedPredictionRunId(state.predictionRuns[0]?.runId ?? "");
    restoreLatestPrediction(state);
    writeStoredValue(storageKeys.projectRoot, state.rootPath);
    return state;
  }

  async function saveClassList(nextClasses: string[]) {
    const activeProject = await ensureProjectReady();
    if (!activeProject) return;
    const loadedState = await runAction(() =>
      api<ProjectState>("/api/projects/classes", {
        method: "PUT",
        body: JSON.stringify({ rootPath: activeProject.rootPath, classes: nextClasses })
      })
    );
    const state = loadedState ? normalizeProjectState(loadedState) : undefined;
    if (state) {
      setProject(state);
      setClasses(state.classes);
      setSelectedClass(null);
      setSelectedRunDir((current) => state.trainingRuns.some((run) => run.runDir === current) ? current : state.latestRun?.runDir ?? "");
      setSelectedPredictionRunId((current) => state.predictionRuns.some((run) => run.runId === current) ? current : state.predictionRuns[0]?.runId ?? "");
      setMessage("类别已保存，YAML 文件已更新。");
    }
  }

  async function addClass() {
    const activeProject = await ensureProjectReady();
    if (!activeProject) return;
    setClassDialog({ mode: "add", value: "" });
  }

  async function editClass() {
    const activeProject = await ensureProjectReady();
    if (!activeProject || selectedClass === null) return;
    const currentClasses = activeProject.classes ?? classes;
    if (selectedClass >= currentClasses.length) return;
    setClassDialog({ mode: "edit", value: currentClasses[selectedClass], index: selectedClass });
  }

  async function removeClass() {
    const activeProject = await ensureProjectReady();
    if (!activeProject || selectedClass === null) return;
    const currentClasses = activeProject.classes ?? classes;
    if (selectedClass >= currentClasses.length) return;
    setClassDialog({ mode: "delete", value: currentClasses[selectedClass], index: selectedClass });
  }

  async function submitClassDialog() {
    if (!classDialog) return;
    const activeProject = await ensureProjectReady();
    if (!activeProject) return;
    const currentClasses = activeProject.classes ?? classes;
    const index = classDialog.index ?? -1;

    if (classDialog.mode === "delete") {
      if (index < 0 || index >= currentClasses.length) {
        setClassDialog((current) => current ? { ...current, error: "请选择要删除的类别。" } : current);
        return;
      }
      setClassDialog(null);
      await saveClassList(currentClasses.filter((_, itemIndex) => itemIndex !== index));
      return;
    }

    const next = classDialog.value.trim();
    if (!next) {
      setClassDialog({ ...classDialog, error: "类别名称不能为空。" });
      return;
    }
    const duplicateIndex = currentClasses.findIndex((item, itemIndex) => item === next && itemIndex !== index);
    if (duplicateIndex >= 0) {
      setClassDialog({ ...classDialog, error: `类别 '${next}' 已存在。` });
      return;
    }
    if (classDialog.mode === "edit") {
      if (index < 0 || index >= currentClasses.length) {
        setClassDialog({ ...classDialog, error: "请选择要编辑的类别。" });
        return;
      }
      const nextClasses = [...currentClasses];
      nextClasses[index] = next;
      setClassDialog(null);
      await saveClassList(nextClasses);
      return;
    }
    setClassDialog(null);
    await saveClassList([...currentClasses, next]);
  }

  async function uploadImages(files: FileList | null) {
    if (!project || !files?.length) return;
    const body = new FormData();
    body.append("rootPath", project.rootPath);
    Array.from(files).forEach((file) => body.append("images", file));
    const result = await runAction(async () => {
      const response = await fetch("/api/projects/images/upload", { method: "POST", body });
      const text = await response.text();
      const payload = text ? JSON.parse(text) : undefined;
      if (!response.ok) throw new Error(payload?.error ?? response.statusText);
      return payload as { imported: number; skipped: string[]; state: ProjectState };
    });
    if (result) {
      setProject(normalizeProjectState(result.state));
      setMessage(importSummary(result.imported, result.skipped));
    }
    if (fileInputRef.current) fileInputRef.current.value = "";
  }

  async function chooseTrainingImages() {
    if (!project) return;
    const chooseImageFiles = window.electronWindow?.chooseImageFiles;
    if (!chooseImageFiles) {
      fileInputRef.current?.click();
      return;
    }
    const result = await runAction(async () => {
      const sources = await chooseImageFiles();
      if (!sources.length) return null;
      return api<{ imported: number; skipped: string[]; state: ProjectState }>("/api/projects/images/import", {
        method: "POST",
        body: JSON.stringify({ rootPath: project.rootPath, sources })
      });
    });
    if (!result) return;
    setProject(normalizeProjectState(result.state));
    setMessage(importSummary(result.imported, result.skipped));
  }

  async function saveConfig() {
    await runAction(() =>
      api<TrainingConfig>("/api/config", {
        method: "PUT",
        body: JSON.stringify(portableTrainingConfig(config))
      })
    );
    setMessage("训练参数已保存。");
  }

  async function browseTrainingModel() {
    if (!window.electronWindow?.chooseModelFile) {
      await saveConfig();
      return;
    }
    const selected = await runAction(() => window.electronWindow!.chooseModelFile!());
    if (!selected) return;
    const nextConfig = { ...portableTrainingConfig(config), model: selected };
    setConfig(nextConfig);
    await runAction(() =>
      api<TrainingConfig>("/api/config", {
        method: "PUT",
        body: JSON.stringify(nextConfig)
      })
    );
    setMessage("训练模型已选择。");
  }

  async function runPreflight(mode: "train" | "predict", payload: Record<string, unknown>, options?: { interactive?: boolean }) {
    const report = await runAction(() =>
      api<PreflightReport>("/api/projects/preflight", {
        method: "POST",
        body: JSON.stringify({ mode, ...payload })
      })
    );
    if (!report) return false;
    setPreflight(report);
    const errors = report.checks.filter((check) => check.level === "error");
    const warnings = report.checks.filter((check) => check.level === "warning");
    if (errors.length) {
      setMessage(`启动体检未通过：${errors[0].label}`);
      if (options?.interactive) {
        window.alert(`启动体检未通过：\n${preflightSummary(errors)}`);
      }
      return false;
    }
    if (warnings.length) {
      setMessage(`启动体检有 ${warnings.length} 个提醒。`);
      if (options?.interactive) {
        return window.confirm(`启动体检有提醒：\n${preflightSummary(warnings)}\n\n是否继续启动？`);
      }
      return true;
    }
    setMessage("启动体检通过。");
    return true;
  }

  async function checkTrainingPreflight(options?: { interactive?: boolean }) {
    if (!requireRuntime()) return false;
    if (!project) {
      setMessage("请先设置项目根目录。");
      setTab("directory");
      return false;
    }
    return runPreflight("train", {
      rootPath: project.rootPath,
      modelPath: config.model,
      device: config.device,
      imageSize: config.imageSize
    }, options);
  }

  async function startProjectTask(path: string, options?: { stayOnPage?: boolean }) {
    if (!project) return;
    const nextTask = await runAction(() =>
      api<Task>(path, {
        method: "POST",
        body: JSON.stringify({ rootPath: project.rootPath })
      })
    );
    if (nextTask) {
      handleTaskSnapshot(nextTask);
      setLogScope("main");
      if (!options?.stayOnPage) {
        setTab("logs");
      }
      watchTask(nextTask.id);
    }
  }

  async function startTraining() {
    if (!requireRuntime()) return;
    if (!project) {
      setMessage("请先设置项目根目录。");
      setTab("directory");
      return;
    }
    if (!project.sampleCount) {
      setMessage("请先添加训练图片。");
      return;
    }
    if (!classes.length) {
      setMessage("请先添加至少一个类别。");
      setTab("directory");
      return;
    }
    const preflightOK = await checkTrainingPreflight({ interactive: true });
    if (!preflightOK) return;
    await saveConfig();
    const nextTask = await runAction(() =>
      api<Task>("/api/tasks/train", {
        method: "POST",
        body: JSON.stringify({ ...portableTrainingConfig(config), projectRoot: project.rootPath })
      })
    );
    if (nextTask) {
      handleTaskSnapshot(nextTask);
      setLogScope("main");
      setTab("logs");
      watchTask(nextTask.id);
    }
  }

  async function cancelTask() {
    if (!task) return;
    if (!window.confirm("确定要停止训练吗？")) return;
    const nextTask = await runAction(() =>
      api<Task>(`/api/tasks/${task.id}/cancel`, { method: "POST", body: "{}" })
    );
    if (nextTask) handleTaskSnapshot(nextTask);
  }

  async function cancelPredictTask() {
    if (!predictTask) return;
    if (!window.confirm("确定要停止当前模型测试吗？")) return;
    const nextTask = await runAction(() =>
      api<Task>(`/api/tasks/${predictTask.id}/cancel`, { method: "POST", body: "{}" })
    );
    if (nextTask) {
      handlePredictTaskSnapshot(nextTask);
      setLogScope("predict");
      setMessage("模型测试已停止。");
    }
  }

  function clearPredictionResult() {
    setPrediction(null);
    setPredictTask(null);
    writeStoredValue(storageKeys.predictTaskId, "");
    setSelectedPredictionRunId("");
    setLogScope("main");
    setMessage("测试结果已清空。");
  }

  function loadPredictionHistory(runId: string) {
    setSelectedPredictionRunId(runId);
    if (!runId) return;
    const historical = predictionRuns.find((run) => run.runId === runId);
    if (!historical) {
      setMessage("未找到这条模型测试记录。");
      return;
    }
    setPrediction(historical);
    setPredictTask(null);
    setLogScope("predict");
    setMessage("已载入历史模型测试结果。");
  }

  function restoreLatestPrediction(state: ProjectState) {
    const latestPrediction = state.predictionRuns[0];
    setPredictTask(null);
    setPrediction(latestPrediction ?? null);
  }

  async function openPath(path?: string) {
    if (!path) return;
    await runAction(() =>
      api<{ ok: boolean }>("/api/open-path", {
        method: "POST",
        body: JSON.stringify({ path })
      })
    );
  }

  async function exportDiagnostics() {
    const result = await runAction(() =>
      api<{ path: string }>("/api/diagnostics/export", {
        method: "POST",
        body: JSON.stringify({
          rootPath: project?.rootPath ?? rootPath,
          taskIds: [task?.id, predictTask?.id].filter(Boolean)
        })
      })
    );
    if (!result?.path) return;
    setMessage("诊断报告已生成。");
    await openPath(result.path);
  }

  async function launchLabelMe() {
    if (!requireRuntime()) return;
    if (!project) return;
    await runAction(() =>
      api<{ ok: boolean }>("/api/tools/labelme", {
        method: "POST",
        body: JSON.stringify({ rootPath: project.rootPath })
      })
    );
    setMessage("LabelMe 启动请求已发送。");
  }

  async function startAutoAnnotate() {
    if (!requireRuntime()) return;
    const modelPath = autoModelPath.trim() || suggestedAutoModelPath;
    if (!modelPath) {
      setMessage("请先选择自动标注模型。");
      return;
    }
    if (!autoModelPath.trim()) {
      setAutoModelPath(modelPath);
    }
    if (!autoImageDir.trim()) {
      setMessage("请先选择自动标注图片目录。");
      return;
    }
    if (!autoOutputDir.trim()) {
      setMessage("请先选择 LabelMe 输出目录。");
      return;
    }
    const nextTask = await runAction(() =>
      api<Task>("/api/tasks/auto-annotate", {
        method: "POST",
        body: JSON.stringify({
          imageDir: autoImageDir,
          outputDir: autoOutputDir,
          modelPath,
          device: autoDevice,
          imageSize: autoImageSize,
          confidence: autoConfidence,
          iou: autoIou,
          overwrite: autoOverwrite
        })
      })
    );
    if (!nextTask) return;
    handleTaskSnapshot(nextTask);
    setMessage("自动多边形标注已启动。");
    watchTask(nextTask.id);
  }

  async function browseAutoModel() {
    if (!window.electronWindow?.chooseModelFile) return;
    const selected = await runAction(() => window.electronWindow!.chooseModelFile!());
    if (selected) {
      autoModelTouchedRef.current = true;
      setAutoModelPath(selected);
    }
  }

  async function browseAutoImageDir() {
    if (!window.electronWindow?.chooseDirectory) return;
    const selected = await runAction(() => window.electronWindow!.chooseDirectory!());
    if (selected) setAutoImageDir(selected);
  }

  async function browseAutoOutputDir() {
    if (!window.electronWindow?.chooseDirectory) return;
    const selected = await runAction(() => window.electronWindow!.chooseDirectory!());
    if (selected) setAutoOutputDir(selected);
  }

  async function openAutoLabelMe() {
    if (!requireRuntime()) return;
    if (!autoImageDir.trim() || !autoOutputDir.trim()) {
      setMessage("请先选择图片目录和 LabelMe 输出目录。");
      return;
    }
    await runAction(() =>
      api<{ ok: boolean }>("/api/tools/labelme", {
        method: "POST",
        body: JSON.stringify({ imageDir: autoImageDir, outputDir: autoOutputDir })
      })
    );
    setMessage("LabelMe 启动请求已发送。");
  }

  async function convertAutoAnnotations() {
    if (!autoOutputDir.trim()) {
      setMessage("请先选择输出目录。");
      return;
    }
    const root = autoOutputDir.replace(/[\\/](labelmejson|labels|images|xml|Annotations)?[\\/]?$/i, "");
    const nextTask = await runAction(() =>
      api<Task>("/api/projects/convert-annotations", {
        method: "POST",
        body: JSON.stringify({ rootPath: root })
      })
    );
    if (!nextTask) return;
    upsertTaskHistory(nextTask);
    watchAutoTask(nextTask.id);
  }

  async function createAutoProject() {
    if (!window.electronWindow?.chooseDirectory) {
      setMessage("当前环境不支持目录选择。");
      return;
    }
    const selected = await runAction(() => window.electronWindow!.chooseDirectory!());
    if (!selected) return;
    const state = await runAction(() =>
      api<ProjectState>("/api/projects/open", {
        method: "POST",
        body: JSON.stringify({ rootPath: selected })
      })
    );
    if (!state) return;
    const normalized = normalizeProjectState(state);
    setProject(normalized);
    setRootPath(normalized.rootPath);
    setClasses(normalized.classes);
    setSelectedClass(null);
    setSelectedRunDir(normalized.latestRun?.runDir ?? "");
    setSelectedPredictionRunId(normalized.predictionRuns[0]?.runId ?? "");
    restoreLatestPrediction(normalized);
    writeStoredValue(storageKeys.projectRoot, normalized.rootPath);
    setAutoImageDir(normalized.paths.imagesDir);
    setAutoOutputDir(normalized.paths.labelmeDir);
    if (!autoModelTouchedRef.current) {
      setAutoModelPath(normalized.latestRun?.modelPath?.trim() || config.model.trim());
    }
    setAutoDevice("auto");
    setAutoImageSize(config.imageSize);
    setMessage("新项目已创建，自动标注目录已填入。");
  }

  async function refreshCameras() {
    if (!navigator.mediaDevices?.enumerateDevices) {
      setCameraError("当前环境不支持摄像头访问。");
      return;
    }
    try {
      const devices = await navigator.mediaDevices.enumerateDevices();
      const videoDevices = devices.filter((device) => device.kind === "videoinput");
      setCameras(videoDevices);
      setSelectedCameraId((current) => current || videoDevices[0]?.deviceId || "");
      setCameraError(videoDevices.length ? "" : "未发现可用摄像头。");
    } catch (err) {
      setCameraError(cameraAccessErrorMessage(err));
    }
  }

  async function startCamera() {
    if (!requireRuntime()) return;
    if (!project) {
      setMessage("请先设置项目根目录。");
      setTab("directory");
      return;
    }
    stopCamera();
    const cameraIndex = Math.max(0, cameras.findIndex((camera) => camera.deviceId === selectedCameraId));
    const streamUrl = `/api/predict/webcam?${query({
      rootPath: project.rootPath,
      modelPath: activeModelPath || "",
      device: config.device,
      imageSize: String(config.imageSize),
      confidence: String(confidence),
      camera: String(cameraIndex)
    })}`;
    setCameraStreamUrl(streamUrl);
    setCameraError("");
    setMessage("YOLO 实时识别已启动。");
  }

  function stopCamera() {
    setCameraStreamUrl("");
    void fetch("/api/predict/webcam/stop", { method: "POST" }).catch(() => undefined);
    setMessage("YOLO 实时识别已停止。");
  }

  async function predictSelectedImage(files: FileList | null) {
    const file = files?.[0];
    if (!file) return;
    await uploadPredictBlob(file, file.name);
    if (testFileInputRef.current) testFileInputRef.current.value = "";
  }

  async function uploadPredictBlob(blob: Blob, filename: string) {
    if (!requireRuntime()) return;
    if (!project) {
      setMessage("请先设置项目根目录。");
      setTab("directory");
      return;
    }
    const body = new FormData();
    body.append("rootPath", project.rootPath);
    body.append("image", blob, filename);
    body.append("modelPath", activeModelPath || "");
    body.append("device", config.device);
    body.append("imageSize", String(config.imageSize));
    body.append("confidence", String(confidence));
    const nextTask = await runAction(async () => {
      const response = await fetch("/api/tasks/predict-image", { method: "POST", body });
      const text = await response.text();
      const payload = text ? JSON.parse(text) : undefined;
      if (!response.ok) throw new Error(payload?.error ?? response.statusText);
      return payload as Task;
    });
    if (nextTask) {
      setPrediction(null);
      handlePredictTaskSnapshot(nextTask);
      setLogScope("predict");
      setTab("test");
      setMessage("模型测试已启动。");
      watchPredictTask(nextTask.id);
    }
  }

  async function browseTestModel() {
    if (!window.electronWindow?.chooseModelFile) {
      setMessage("请在输入框中填写 .pt 模型路径。");
      return;
    }
    const selected = await runAction(() => window.electronWindow!.chooseModelFile!());
    if (!selected) return;
    updateTestModelPath(selected);
    setMessage("测试模型已切换。");
  }

  function followTrainingModel() {
    updateTestModelPath("");
    setMessage("模型测试将跟随当前训练记录。");
  }

  function handleTaskSnapshot(nextTask: Task, options?: { restored?: boolean }) {
    upsertTaskHistory(nextTask);
    setTask(nextTask);
    if (nextTask.type === "convert-voc") {
      setConvertTask(nextTask);
      if (finalStatuses.has(nextTask.status)) {
        window.setTimeout(() => setConvertTask((current) => current?.id === nextTask.id ? null : current), 2000);
      }
    }
    if (nextTask.type === "validate-labels") {
      setValidateTask(nextTask);
      if (finalStatuses.has(nextTask.status)) {
        window.setTimeout(() => setValidateTask((current) => current?.id === nextTask.id ? null : current), 2000);
      }
    }
    if (nextTask.type === "auto-annotate" && finalStatuses.has(nextTask.status) && !options?.restored) {
      const outcome = describeAutoAnnotateTask(nextTask);
      setMessage(outcome.detail ? `自动标注${outcome.label}：${outcome.detail}` : `自动标注${outcome.label}。`);
    }
    if (nextTask.status === "failed" && nextTask.type !== "auto-annotate" && !options?.restored) {
      setMessage(taskErrorHeadline(nextTask.error, `${taskTypeLabel(nextTask.type)}失败，请查看 Python 原始日志。`));
    } else if (nextTask.status === "succeeded" && nextTask.type === "train" && !options?.restored) {
      setMessage("训练完成，详细 Python 日志和训练记录已保存。");
    }
    if (finalStatuses.has(nextTask.status)) {
      writeStoredValue(storageKeys.taskId, "");
      if (!options?.restored) {
        void refreshProject();
      }
    } else {
      writeStoredValue(storageKeys.taskId, nextTask.id);
    }
  }

  function handlePredictTaskSnapshot(nextTask: Task, options?: { restored?: boolean }) {
    upsertTaskHistory(nextTask);
    setPredictTask(nextTask);
    if (nextTask.status === "succeeded" && nextTask.result) {
      const result = nextTask.result as PredictionResult;
      setPrediction(result);
      setSelectedPredictionRunId(result.runId ?? "");
      if (!options?.restored) {
        setMessage("模型测试完成。");
      }
    } else if (nextTask.status === "failed") {
      setMessage(nextTask.error || "模型测试失败。");
    }
    if (finalStatuses.has(nextTask.status)) {
      writeStoredValue(storageKeys.predictTaskId, "");
      if (!options?.restored) {
        void refreshProject({ quiet: true });
      }
    } else {
      writeStoredValue(storageKeys.predictTaskId, nextTask.id);
    }
  }

  function upsertTaskHistory(nextTask: Task) {
    setTaskHistory((current) => {
      const rest = current.filter((item) => item.id !== nextTask.id);
      return [nextTask, ...rest].sort((a, b) => latestTaskTime(b) - latestTaskTime(a));
    });
  }

  function watchTask(id: string) {
    clearTaskPoll();
    eventSourceRef.current?.close();
    const source = new EventSource(`/api/tasks/${id}/events`);
    eventSourceRef.current = source;
    source.addEventListener("task", (event) => {
      const nextTask = JSON.parse((event as MessageEvent).data) as Task;
      handleTaskSnapshot(nextTask);
      if (finalStatuses.has(nextTask.status)) {
        source.close();
        eventSourceRef.current = null;
      }
    });
    source.onerror = () => {
      source.close();
      eventSourceRef.current = null;
      startTaskPolling(id);
    };
  }

  function watchAutoTask(id: string) {
    const source = new EventSource(`/api/tasks/${id}/events`);
    source.addEventListener("task", (event) => {
      const nextTask = JSON.parse((event as MessageEvent).data) as Task;
      upsertTaskHistory(nextTask);
      if (finalStatuses.has(nextTask.status)) {
        source.close();
        void refreshProject();
      }
    });
    source.onerror = () => {
      source.close();
    };
  }

  function watchPredictTask(id: string) {
    clearPredictPoll();
    predictEventSourceRef.current?.close();
    const source = new EventSource(`/api/tasks/${id}/events`);
    predictEventSourceRef.current = source;
    source.addEventListener("task", (event) => {
      const nextTask = JSON.parse((event as MessageEvent).data) as Task;
      handlePredictTaskSnapshot(nextTask);
      if (finalStatuses.has(nextTask.status)) {
        source.close();
        predictEventSourceRef.current = null;
      }
    });
    source.onerror = () => {
      source.close();
      predictEventSourceRef.current = null;
      startPredictTaskPolling(id);
    };
  }

  function startTaskPolling(id: string) {
    clearTaskPoll();
    taskPollRef.current = window.setTimeout(() => void pollTask(id), 1200);
  }

  async function pollTask(id: string) {
    try {
      const nextTask = await api<Task>(`/api/tasks/${id}`);
      handleTaskSnapshot(nextTask);
      if (!finalStatuses.has(nextTask.status)) {
        startTaskPolling(id);
      }
    } catch (err) {
      writeStoredValue(storageKeys.taskId, "");
      setMessage(err instanceof Error ? `训练任务连接已断开：${err.message}` : "训练任务连接已断开。");
    }
  }

  function startPredictTaskPolling(id: string) {
    clearPredictPoll();
    predictPollRef.current = window.setTimeout(() => void pollPredictTask(id), 1200);
  }

  async function pollPredictTask(id: string) {
    try {
      const nextTask = await api<Task>(`/api/tasks/${id}`);
      handlePredictTaskSnapshot(nextTask);
      if (!finalStatuses.has(nextTask.status)) {
        startPredictTaskPolling(id);
      }
    } catch (err) {
      writeStoredValue(storageKeys.predictTaskId, "");
      setMessage(err instanceof Error ? `模型测试任务连接已断开：${err.message}` : "模型测试任务连接已断开。");
    }
  }

  function clearTaskPoll() {
    if (taskPollRef.current !== null) {
      window.clearTimeout(taskPollRef.current);
      taskPollRef.current = null;
    }
  }

  function clearPredictPoll() {
    if (predictPollRef.current !== null) {
      window.clearTimeout(predictPollRef.current);
      predictPollRef.current = null;
    }
  }

  function clearStoredTaskPolls() {
    clearTaskPoll();
    clearPredictPoll();
  }

  function updateConfig<K extends keyof TrainingConfig>(key: K, value: TrainingConfig[K]) {
    setConfig((current) => ({ ...current, [key]: value }));
  }

  function minimizeWindow() {
    void window.electronWindow?.minimize();
  }

  function closeWindow() {
    if (running || predicting) {
      const ok = window.confirm(`${protectedTaskLabel}，关闭窗口会中断当前任务。\n\n确定要关闭吗？`);
      if (!ok) {
        setMessage("已取消关闭，当前任务继续运行。");
        setTab("logs");
        setLogScope(predicting && !running ? "predict" : "main");
        return;
      }
    }
    if (window.electronWindow) {
      void window.electronWindow.close();
      return;
    }
    window.close();
  }

  return (
    <div className="qt-shell">
      <div className="qt-window">
        <div className="qt-title-row">
          <div className="title-menu">
            <nav className="qt-tabs" aria-label="顶部导航">
              <TabButton active={tab === "directory"} label="目录管理" onClick={() => { setTab("directory"); setParamsOpen(false); }} />
              <TabButton active={tab === "logs"} label="训练日志" onClick={() => { setTab("logs"); setParamsOpen(false); }} />
              <TabButton active={tab === "test"} label="模型测试" onClick={() => { setTab("test"); setParamsOpen(false); }} />
              <TabButton active={tab === "annotate"} label="自动标注" onClick={() => { setTab("annotate"); setParamsOpen(false); }} />
            </nav>
          </div>
          <div className="qt-title-center">
            <strong>基于YOLO26的铝型方管缺陷智能分类检测系统</strong>
          </div>
          <div className="window-controls">
            <button className="window-control minimize" type="button" title="最小化" aria-label="最小化" onClick={minimizeWindow}>
              <span aria-hidden="true">-</span>
            </button>
            <button className="window-control close" type="button" title="关闭" aria-label="关闭" onClick={closeWindow}>
              <span aria-hidden="true">×</span>
            </button>
          </div>
        </div>

        <div className="workbench">
          <section className={tab === "test" || tab === "annotate" ? "workbench-main test-mode" : "workbench-main"}>
            <main className="qt-content">
          {tab === "directory" && (
            <section className="directory-layout">
              <GroupBox title="1. 项目根目录设置" className="directory-root-box" layoutId="directory.root">
                <div className="project-row">
                  <input
                    value={rootPath}
                    onChange={(event) => setRootPath(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") void openProject();
                    }}
                    placeholder="选择项目根目录..."
                  />
                  <button className="preflight-inline-button" onClick={() => void checkTrainingPreflight({ interactive: false })} disabled={!project || busy || !runtimeReady}>
                    启动前体检
                  </button>
                  <button onClick={browseProjectRoot} disabled={busy}>浏览...</button>
                </div>
              </GroupBox>

              <ProjectSummaryBox project={project} />

              <GroupBox title="2. 类别管理" className="directory-class-box" layoutId="directory.classes">
                <div className="class-listbox">
                  {classes.length ? (
                    classes.map((item, index) => (
                      <button
                        key={`${item}-${index}`}
                        className={selectedClass === index ? "list-item selected" : "list-item"}
                        onClick={() => setSelectedClass(index)}
                      >
                        {item}
                      </button>
                    ))
                  ) : (
                    <div className="list-empty">暂无类别，请添加</div>
                  )}
                </div>
                <div className="three-buttons">
                  <button onClick={addClass} disabled={busy || (!project && !rootPath.trim())}>添加类别</button>
                  <button onClick={editClass} disabled={!project || selectedClass === null || busy}>编辑类别</button>
                  <button onClick={removeClass} disabled={!project || selectedClass === null || busy}>删除类别</button>
                </div>
              </GroupBox>

              <GroupBox title="3. 目录结构" className="directory-paths-box" layoutId="directory.paths">
                <div className="directory-model-row">
                  <label>训练模型:</label>
                  <input
                    value={config.model}
                    placeholder=""
                    onChange={(event) => updateConfig("model", event.target.value)}
                  />
                  <button type="button" onClick={() => void browseTrainingModel()} disabled={busy}>选择</button>
                </div>
                <DirectoryRows paths={project?.paths ?? null} openPath={openPath} />
              </GroupBox>

              <GroupBox title="4. 选择训练图片" className="directory-import-box" layoutId="directory.import">
                <input
                  ref={fileInputRef}
                  className="hidden-input"
                  type="file"
                  multiple
                  accept=".jpg,.jpeg,.png,.bmp"
                  onChange={(event) => void uploadImages(event.target.files)}
                />
                <button className="select-image-button" onClick={() => void chooseTrainingImages()} disabled={!project || busy}>
                  选择图片文件
                </button>
                <div className="sample-count">当前训练集图片数: {project?.sampleCount ?? 0}</div>
              </GroupBox>

              <GroupBox title="5. 辅助功能" className="directory-tools-box" layoutId="directory.tools">
                <div className="tool-grid">
                  <button className="tool green" onClick={launchLabelMe} disabled={!project || busy || !runtimeReady}>
                    LabelMe
                  </button>
                  <button className="tool purple" onClick={() => void refreshProject()} disabled={!project || busy}>
                    刷新目录
                  </button>
                  <button className="tool blue" onClick={() => setParamsOpen(true)} disabled={busy}>
                    训练参数
                  </button>
                </div>
              </GroupBox>

              <GroupBox title="6. 训练图片列表" className="directory-image-list-box" layoutId="directory.images">
                <div className="image-list-summary">
                  <Metric title="图片" value={String(project?.sampleCount ?? 0)} />
                  <Metric title="标签" value={String(project?.labelCount ?? 0)} />
                  <Metric title="类别" value={String(project?.classes.length ?? 0)} />
                </div>
                <div className="image-listbox">
                  {project?.trainImages?.length ? (
                    project.trainImages.map((image) => (
                      <div className="image-list-item" key={image.path}>
                        <span>{image.name}</span>
                        <small>{formatBytes(image.size)}</small>
                      </div>
                    ))
                  ) : (
                    <div className="list-empty">还没有训练图片</div>
                  )}
                </div>
                <button className="wide-button" onClick={() => void refreshProject()} disabled={!project || busy}>刷新图片列表</button>
              </GroupBox>

              <TrainingResultsBox
                title="7. 训练结果"
                className="directory-results-box"
                layoutId="directory.results"
                result={selectedResult}
                runs={trainingRuns}
                selectedRunDir={selectedRunDir}
                onSelectRun={setSelectedRunDir}
                openPath={openPath}
                footer={
                  <TaskResultSplit
                    convertTask={convertTask}
                    validateTask={validateTask}
                    disabled={!project || busy || running}
                    onConvert={() => startProjectTask("/api/projects/convert-annotations", { stayOnPage: true })}
                    onValidate={() => startProjectTask("/api/projects/validate-labels", { stayOnPage: true })}
                  />
                }
              />
            </section>
          )}

          {tab === "logs" && (
            <section className="logs-layout">
              <div className="log-task-switcher" data-layout-id="logs.switcher" data-layout-label="日志任务切换">
                <TaskLogCard
                  active={logScope === "main"}
                  label="训练与项目任务"
                  task={task}
                  fallback="等待训练、转换或验证任务"
                  onClick={() => setLogScope("main")}
                />
                <TaskLogCard
                  active={logScope === "predict"}
                  label="模型测试任务"
                  task={predictTask}
                  fallback="等待图片或摄像头测试"
                  onClick={() => setLogScope("predict")}
                />
              </div>
              <div className="log-toolbar" data-layout-id="logs.toolbar" data-layout-label="日志工具栏">
                <span>{logs.length ? `总日志 / ${logs.length} 条` : "暂无任务日志"}</span>
                <button type="button" onClick={() => void exportDiagnostics()} disabled={busy}>
                  导出诊断报告
                </button>
                <button type="button" onClick={() => openPath(project?.rootPath)} disabled={!project || busy}>
                  打开项目目录
                </button>
              </div>
              <div className="log-window" ref={logRef} data-layout-id="logs.window" data-layout-label="日志窗口">
                {logs.length ? (
                  logs.map((log, index) => (
                    <div className="log-line" key={`${log.at}-${index}`}>
                      <time>{formatTime(log.at)}</time>
                      <span>{log.message}</span>
                    </div>
                  ))
                ) : (
                  <div className="log-empty">任务启动后日志会出现在这里</div>
                )}
              </div>
              {logRunning && (
                <div className="progress-bar">
                  <div style={{ width: `${runningLogTask?.progress ?? 0}%` }} />
                </div>
              )}
            </section>
          )}

          {tab === "test" && (
            <section className="test-layout">
              <GroupBox title="1. 测试图片与模型" className="test-input-box" layoutId="test.input">
                <input
                  ref={testFileInputRef}
                  className="hidden-input"
                  type="file"
                  accept=".jpg,.jpeg,.png,.bmp"
                  onChange={(event) => void predictSelectedImage(event.target.files)}
                />
                <div className="test-controls">
                  <div className="test-control-row">
                    <button className="select-image-button" onClick={() => testFileInputRef.current?.click()} disabled={!project || predicting || busy || !runtimeReady}>
                      选择图片测试
                    </button>
                    <button onClick={() => openPath(project?.paths.predictDir)} disabled={!project}>
                      打开测试目录
                    </button>
                  </div>
                  <div className="test-control-row">
                    <button type="button" onClick={browseTestModel} disabled={predicting || busy}>
                      选择
                    </button>
                    <button type="button" onClick={followTrainingModel} disabled={!testModelPath.trim() || predicting || busy}>
                      跟随
                    </button>
                  </div>
                </div>
                <div className="model-picker-row">
                  <label>测试模型:</label>
                  <input
                    value={testModelPath}
                    onChange={(event) => updateTestModelPath(event.target.value)}
                    placeholder=""
                  />
                </div>
                <div className="test-param-row">
                  <label>置信度:</label>
                  <input
                    type="range"
                    min="0.05"
                    max="0.95"
                    step="0.05"
                    value={confidence}
                    onChange={(event) => setConfidence(Number(event.target.value))}
                  />
                  <strong>{confidence.toFixed(2)}</strong>
                </div>
                <div className="model-source-line">
                  模型来源: {activeModelSource}{activeModelPath ? ` · ${shortPath(activeModelPath)}` : ""}
                </div>
              </GroupBox>

              <GroupBox title="2. 摄像头测试" className="test-camera-box" layoutId="test.camera">
                <div className="camera-toolbar">
                  <select value={selectedCameraId} onChange={(event) => setSelectedCameraId(event.target.value)}>
                    {cameras.length ? (
                      cameras.map((camera, index) => (
                        <option key={camera.deviceId || index} value={camera.deviceId}>
                          {camera.label || `摄像头 ${index + 1}`}
                        </option>
                      ))
                    ) : (
                      <option value="">未发现摄像头</option>
                    )}
                  </select>
                  <button onClick={() => void refreshCameras()} disabled={busy}>刷新</button>
                </div>
                <div className="camera-box">
                  {cameraStreamUrl ? (
                    <>
                      <img
                        className="camera-stream"
                        src={cameraStreamUrl}
                        alt="YOLO 实时识别视频流"
                        onError={() => setCameraError("YOLO 实时识别流启动失败，请检查摄像头、模型和内置运行时。")}
                      />
                    </>
                  ) : (
                    <div className="camera-empty">YOLO 实时识别画面</div>
                  )}
                </div>
                {cameraError && <div className="inline-error">{cameraError}</div>}
                <div className="three-buttons">
                  <button onClick={() => void startCamera()} disabled={!project || busy || !runtimeReady}>打开实时识别</button>
                  <button onClick={stopCamera} disabled={!cameraStreamUrl}>停止摄像头</button>
                </div>
              </GroupBox>

              <TrainingResultsBox
                title="3. 训练结果"
                className="test-results-box"
                layoutId="test.results"
                result={selectedResult}
                runs={trainingRuns}
                selectedRunDir={selectedRunDir}
                onSelectRun={setSelectedRunDir}
                openPath={openPath}
                onUseModel={(modelPath) => {
                  updateTestModelPath(modelPath);
                  setMessage("已将选中的训练模型用于模型测试。");
                }}
              />

              <GroupBox title="4. 识别结果" className="test-result-box" layoutId="test.prediction">
                <PredictionResultBox
                  task={predictTask}
                  result={prediction}
                  history={predictionRuns}
                  selectedRunId={selectedPredictionRunId}
                  onChangeHistory={setSelectedPredictionRunId}
                  onSelectHistory={loadPredictionHistory}
                  openPath={openPath}
                  onCancel={cancelPredictTask}
                  onClear={clearPredictionResult}
                />
              </GroupBox>
            </section>
          )}

          {tab === "annotate" && (
            <section className="auto-annotate-layout">
              <GroupBox title="自动标注设置" className="auto-annotate-settings-box" layoutId="annotate.settings">
                <div className="auto-form">
                  <div className="auto-field-row">
                    <label>模型:</label>
                    <input
                      value={autoModelPath}
                      onChange={(event) => {
                        autoModelTouchedRef.current = true;
                        setAutoModelPath(event.target.value);
                      }}
                    />
                    <button type="button" onClick={() => void browseAutoModel()} disabled={busy || running}>选择</button>
                  </div>
                  <div className="auto-field-row">
                    <label>图片目录:</label>
                    <input value={autoImageDir} onChange={(event) => setAutoImageDir(event.target.value)} />
                    <button type="button" onClick={() => void browseAutoImageDir()} disabled={busy || running}>选择</button>
                  </div>
                  <div className="auto-field-row">
                    <label>输出目录:</label>
                    <input value={autoOutputDir} onChange={(event) => setAutoOutputDir(event.target.value)} />
                    <button type="button" onClick={() => void browseAutoOutputDir()} disabled={busy || running}>选择</button>
                  </div>
                  <div className="auto-param-grid">
                    <label>
                      <span>设备</span>
                      <select value={autoDevice} onChange={(event) => setAutoDevice(event.target.value)}>
                        <option value="auto">自动检测（GPU 优先）</option>
                        <option value="cpu">CPU</option>
                        <option value="cuda:0">CUDA 0</option>
                      </select>
                    </label>
                    <label>
                      <span>尺寸</span>
                      <select value={String(autoImageSize)} onChange={(event) => setAutoImageSize(Number(event.target.value))}>
                        {["320", "416", "512", "640", "768", "1024"].map((item) => <option key={item} value={item}>{item}</option>)}
                      </select>
                    </label>
                    <label>
                      <span>置信度</span>
                      <input
                        type="number"
                        min="0.01"
                        max="1"
                        step="0.01"
                        value={autoConfidence}
                        onChange={(event) => setAutoConfidence(Number(event.target.value))}
                      />
                    </label>
                    <label>
                      <span>IOU</span>
                      <input
                        type="number"
                        min="0.01"
                        max="1"
                        step="0.01"
                        value={autoIou}
                        onChange={(event) => setAutoIou(Number(event.target.value))}
                      />
                    </label>
                    <label className="auto-check">
                      <input type="checkbox" checked={autoOverwrite} onChange={(event) => setAutoOverwrite(event.target.checked)} />
                      <span>覆盖已有 JSON</span>
                    </label>
                  </div>
                  <div className="auto-actions">
                    <button className="big-action start" type="button" onClick={() => void startAutoAnnotate()} disabled={busy || running || predicting || !runtimeReady}>
                      开始自动标注
                    </button>
                    <button type="button" onClick={() => void createAutoProject()} disabled={busy || running}>
                      创建/使用一个项目
                    </button>
                  </div>
                </div>
              </GroupBox>

              <GroupBox title="辅助功能" className="auto-annotate-actions-box" layoutId="annotate.actions">
                <div className="auto-next-actions">
                  <button type="button" onClick={() => void openAutoLabelMe()} disabled={busy || !autoImageDir || !autoOutputDir || !runtimeReady}>
                    LabelMe
                  </button>
                  <button type="button" onClick={() => openPath(autoOutputDir)} disabled={!autoOutputDir}>
                    打开输出目录
                  </button>
                  <button type="button" onClick={() => void convertAutoAnnotations()} disabled={!autoOutputDir || busy || running}>
                    标注转 YOLO
                  </button>
                  <button className="big-action start" type="button" onClick={() => void startTraining()} disabled={!project || running || busy || !runtimeReady}>
                    再次训练
                  </button>
                </div>
                <div className="auto-task-info">
                  <div>
                    <span>任务 ID</span>
                    <strong>{latestAutoAnnotateTask?.id ?? "-"}</strong>
                  </div>
                  <div>
                    <span>退出码</span>
                    <strong>{taskExitCode(latestAutoAnnotateTask)}</strong>
                  </div>
                  <div>
                    <span>耗时</span>
                    <strong>{taskDuration(latestAutoAnnotateTask)}</strong>
                  </div>
                  <div className={`auto-result ${autoOutcome.tone}`} title={autoOutcome.detail}>
                    <span>结果</span>
                    <strong>{autoOutcome.label}</strong>
                  </div>
                </div>
              </GroupBox>

              <GroupBox title="任务日志" className="auto-annotate-log-box" layoutId="annotate.logs">
                <div className="log-window compact-log" ref={autoLogRef}>
                  {autoAnnotateLogs.length ? autoAnnotateLogs.map((log, index) => (
                    <div className="log-line" key={`${log.at}-${index}`}>
                      <time>{formatTime(log.at)}</time>
                      <span>{log.message}</span>
                    </div>
                  )) : (
                    <div className="log-empty">自动标注开始后会显示日志</div>
                  )}
                </div>
                {latestAutoAnnotateTask?.status === "running" && (
                  <div className="progress-bar">
                    <div style={{ width: `${latestAutoAnnotateTask?.progress ?? 0}%` }} />
                  </div>
                )}
              </GroupBox>
            </section>
          )}
            </main>

            {tab !== "test" && tab !== "annotate" && (
              <div className="bottom-actions" data-layout-id="common.actions" data-layout-label="底部操作按钮">
                <button className="big-action start" onClick={startTraining} disabled={!project || running || busy || !runtimeReady}>
                  开始训练
                </button>
                <button className="big-action stop" onClick={cancelTask} disabled={!running}>
                  停止训练
                </button>
                <button className="big-action open" onClick={() => openPath(project?.paths.runsDir)} disabled={!project}>
                  打开训练结果
                </button>
              </div>
            )}

            <div className="qt-status" data-layout-id="common.status" data-layout-label="底部状态栏">
              <div className="status-left">
                <button
                  type="button"
                  className={`version-status ${updateStatus.updateAvailable ? "has-update" : ""}`}
                  title={updateStatus.updateAvailable ? `发现新版本 v${updateStatus.latestVersion}` : "点击检查软件更新"}
                  onClick={() => {
                    setUpdateDialogOpen(true);
                    if (window.electronDesktop && updateStatus.phase === "idle" && !updateStatus.releaseName) void checkForUpdates();
                  }}
                >
                  {updateStatus.updateAvailable && <span className="status-red-dot" aria-hidden="true" />}
                  版本 v{appVersion}
                </button>
                <button
                  type="button"
                  className={`runtime-status ${runtimeReady ? "ready" : "missing"}`}
                  title={runtimeStatus.message}
                  onClick={() => setRuntimeDialogOpen(true)}
                >
                  {!runtimeReady && <span className="status-red-dot" aria-hidden="true" />}
                  {runtimeReady ? "运行环境正常" : "运行环境未安装"}
                </button>
                <span className="status-message">{message}</span>
              </div>
              <span>{logTask ? `当前任务: ${taskTypeLabel(logTask.type)} / ${taskStatusLabel(logTask.status)}` : ""}</span>
            </div>
          </section>
        </div>
        {classDialog && (
          <ClassDialog
            dialog={classDialog}
            inputRef={classDialogInputRef}
            busy={busy}
            onChange={(value) => setClassDialog((current) => current ? { ...current, value, error: "" } : current)}
            onCancel={() => setClassDialog(null)}
            onSubmit={() => void submitClassDialog()}
          />
        )}
        {paramsOpen && (
          <TrainingParamsDrawer
            config={config}
            busy={busy}
            onUpdate={updateConfig}
            onSave={() => void saveConfig()}
            onClose={() => setParamsOpen(false)}
          />
        )}
        {runtimeDialogOpen && (
          <RuntimeInstallDialog
            status={runtimeStatus}
            busy={runtimeBusy}
            onInstall={() => void installRuntime()}
            onCancel={() => void cancelRuntimeInstall()}
            onRefresh={() => void refreshRuntimeStatus()}
            onClose={() => setRuntimeDialogOpen(false)}
          />
        )}
        {updateDialogOpen && (
          <AppUpdateDialog
            status={updateStatus}
            busy={updateBusy}
            onCheck={() => void checkForUpdates()}
            onDownload={() => void downloadUpdate()}
            onInstall={() => void installUpdate()}
            onCancel={() => void window.electronDesktop?.update.cancel()}
            onClose={() => setUpdateDialogOpen(false)}
          />
        )}
        {layoutInspectorOpen && <LayoutInspector onClose={() => setLayoutInspectorOpen(false)} />}
      </div>
    </div>
  );
}

function RuntimeInstallDialog({
  status,
  busy,
  onInstall,
  onCancel,
  onRefresh,
  onClose
}: {
  status: RuntimeStatus;
  busy: boolean;
  onInstall: () => void;
  onCancel: () => void;
  onRefresh: () => void;
  onClose: () => void;
}) {
  const total = status.totalBytes || 0;
  const downloaded = Math.min(status.downloadedBytes || 0, total || status.downloadedBytes || 0);
  return (
    <div className="modal-backdrop runtime-modal-backdrop" role="presentation">
      <section className="desktop-modal runtime-modal" role="dialog" aria-modal="true" aria-label="运行环境管理">
        <header>
          <div>
            <strong>{status.ready ? "运行环境" : "需要安装运行环境"}</strong>
          </div>
          <button type="button" className="modal-close" onClick={onClose} disabled={status.phase === "installing" || status.phase === "validating"}>×</button>
        </header>
        <div className="modal-body">
          <div className={`runtime-state-card ${status.ready ? "ok" : status.phase === "failed" ? "error" : "warning"}`}>
            <strong>{status.message || (status.ready ? "运行环境已就绪。" : "尚未安装运行环境。")}</strong>
          </div>
          <div className="runtime-facts">
            <div><span>环境标识</span><strong>{status.runtimeId || "windows-x64-cuda126-py311"}</strong></div>
            <div><span>下载大小</span><strong>{total ? formatBytes(total) : "等待服务器清单"}</strong></div>
            <div><span>本地版本</span><strong>{status.localVersion || "未安装"}</strong></div>
          </div>
          {(busy || status.percent > 0 && !status.ready) && (
            <div className="desktop-download-progress">
              <div><span>{status.currentPackage || status.message}</span><strong>{Math.max(0, Math.min(100, status.percent || 0))}%</strong></div>
              <div className="progress-bar"><div style={{ width: `${Math.max(0, Math.min(100, status.percent || 0))}%` }} /></div>
              <small>{total ? `${formatBytes(downloaded)} / ${formatBytes(total)}` : status.message}</small>
            </div>
          )}
          {status.error && <pre className="desktop-modal-error">{status.error}</pre>}
          {!status.ready && <p className="runtime-space-hint">开始前请确保磁盘至少有约 10 GiB 可用空间。</p>}
        </div>
        <footer>
          <button type="button" onClick={onRefresh} disabled={busy}>重新检测</button>
          {busy ? (
            <button type="button" className="danger-action" onClick={onCancel}>取消下载</button>
          ) : status.ready && !status.updateAvailable ? (
            <button type="button" className="primary-action" onClick={onClose}>完成</button>
          ) : (
            <button type="button" className="primary-action" onClick={onInstall} disabled={!status.availableVersion}>
              {status.updateAvailable ? "更新运行环境" : "下载并自动安装"}
            </button>
          )}
        </footer>
      </section>
    </div>
  );
}

function AppUpdateDialog({
  status,
  busy,
  onCheck,
  onDownload,
  onInstall,
  onCancel,
  onClose
}: {
  status: AppUpdateStatus;
  busy: boolean;
  onCheck: () => void;
  onDownload: () => void;
  onInstall: () => void;
  onCancel: () => void;
  onClose: () => void;
}) {
  const published = status.publishedAt ? new Date(status.publishedAt).toLocaleString("zh-CN") : "-";
  return (
    <div className="modal-backdrop update-modal-backdrop" role="presentation">
      <section className="desktop-modal update-modal" role="dialog" aria-modal="true" aria-label="软件更新">
        <header>
          <div>
            <strong>软件更新</strong>
          </div>
          <button type="button" className="modal-close" onClick={onClose} disabled={status.phase === "installing"}>×</button>
        </header>
        <div className="modal-body">
          <div className={`update-version-card ${status.updateAvailable ? "available" : "current"}`}>
            <span>{status.updateAvailable ? "发现新版本" : "当前版本"}</span>
            <strong>v{status.latestVersion || status.currentVersion}</strong>
            <small>发布时间：{published}</small>
          </div>
          <div className="release-notes">
            <strong>更新内容</strong>
            <pre>{status.releaseNotes || status.message || "点击“检查更新”获取 GitHub Release 更新内容。"}</pre>
          </div>
          {status.installerSize > 0 && <div className="installer-size">安装包大小：{formatBytes(status.installerSize)}</div>}
          {busy && (
            <div className="desktop-download-progress">
              <div><span>{status.message}</span><strong>{status.percent}%</strong></div>
              <div className="progress-bar"><div style={{ width: `${status.percent}%` }} /></div>
              <small>{status.installerSize ? `${formatBytes(status.downloadedBytes)} / ${formatBytes(status.installerSize)}` : ""}</small>
            </div>
          )}
          {status.error && <pre className="desktop-modal-error">{status.error}</pre>}
        </div>
        <footer>
          <button type="button" onClick={onCheck} disabled={busy}>检查更新</button>
          {busy && status.phase === "downloading" && <button type="button" className="danger-action" onClick={onCancel}>取消下载</button>}
          {status.phase === "downloaded" ? (
            <button type="button" className="primary-action" onClick={onInstall}>重启并安装</button>
          ) : status.updateAvailable ? (
            <button type="button" className="primary-action" onClick={onDownload} disabled={busy}>立即下载</button>
          ) : (
            <button type="button" className="primary-action" onClick={onClose}>关闭</button>
          )}
        </footer>
      </section>
    </div>
  );
}

function TabButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button className={active ? "qt-tab active" : "qt-tab"} onClick={onClick}>
      {label}
    </button>
  );
}

function TrainingParamsDrawer({
  config,
  busy,
  onUpdate,
  onSave,
  onClose
}: {
  config: TrainingConfig;
  busy: boolean;
  onUpdate: ConfigUpdater;
  onSave: () => void;
  onClose: () => void;
}) {
  return (
    <aside className="params-drawer" role="dialog" aria-modal="false" aria-label="训练参数">
      <div className="params-drawer-head">
        <div className="params-panel-title">
          <strong>训练参数</strong>
          <span>基础训练、优化器和数据增强集中配置</span>
        </div>
        <button type="button" onClick={onClose}>关闭</button>
      </div>

      <div className="param-merged-grid">
        <div className="param-compact-section">
          <div className="param-section-title">运行环境</div>
          <div className="param-inline-grid">
            <div className="portable-runtime-readonly" aria-label="Python 运行时">
              <span>Python 环境</span>
              <strong>内置 Python（CUDA 自动检测 / CPU 回退）</strong>
              <em>只读</em>
            </div>
          </div>
        </div>

        <div className="param-compact-section">
          <div className="param-section-title">基础训练</div>
          <div className="param-inline-grid">
            <ParamInlineNumber title="训练轮数" value={config.epochs} min={1} max={500} suffix="轮" onChange={(value) => onUpdate("epochs", value)} />
            <ParamInlineNumber title="批次大小" value={config.batchSize} min={1} max={32} onChange={(value) => onUpdate("batchSize", value)} />
            <ParamInlineSelect title="图像尺寸" value={String(config.imageSize)} options={["320", "416", "512", "640", "768"]} onChange={(value) => onUpdate("imageSize", Number(value))} />
            <ParamInlineSelect
              title="训练设备"
              value={config.device}
              options={["auto", "cpu", "cuda:0"]}
              labels={["自动（推荐）", "仅 CPU", "NVIDIA CUDA 0"]}
              onChange={(value) => onUpdate("device", value)}
            />
            <ParamInlineNumber title="早停耐心" value={config.patience} min={0} max={100} suffix="轮" onChange={(value) => onUpdate("patience", value)} />
            <ParamInlineNumber title="工作线程" value={config.workers} min={0} max={8} onChange={(value) => onUpdate("workers", value)} />
          </div>
        </div>

        <div className="param-compact-section">
          <div className="param-section-title">优化器</div>
          <div className="param-inline-grid">
            <ParamInlineNumber title="学习率" value={config.learningRate} min={0.0001} max={0.1} step={0.0001} onChange={(value) => onUpdate("learningRate", value)} />
            <ParamInlineNumber title="动量" value={config.momentum} min={0.1} max={0.99} step={0.001} onChange={(value) => onUpdate("momentum", value)} />
            <ParamInlineNumber title="权重衰减" value={config.weightDecay} min={0} max={0.01} step={0.0001} onChange={(value) => onUpdate("weightDecay", value)} />
          </div>
        </div>

        <div className="param-compact-section">
          <div className="param-section-title">数据增强</div>
          <div className="param-inline-grid">
            <ParamInlineSelect title="水平翻转" value={probabilityValue(config.fliplr)} options={["0.0", "0.5", "1.0"]} onChange={(value) => onUpdate("fliplr", Number(value))} />
            <ParamInlineSelect title="垂直翻转" value={probabilityValue(config.flipud)} options={["0.0", "0.5", "1.0"]} onChange={(value) => onUpdate("flipud", Number(value))} />
            <ParamInlineSelect title="Mosaic" value={probabilityValue(config.mosaic)} options={["0.0", "0.5", "1.0"]} onChange={(value) => onUpdate("mosaic", Number(value))} />
            <ParamInlineSelect title="MixUp" value={probabilityValue(config.mixup)} options={["0.0", "0.5", "1.0"]} onChange={(value) => onUpdate("mixup", Number(value))} />
            <ParamInlineSelect title="Copy-Paste" value={probabilityValue(config.copyPaste)} options={["0.0", "0.5", "1.0"]} onChange={(value) => onUpdate("copyPaste", Number(value))} />
            <ParamInlineToggle title="启用增强" checked={config.augment} onChange={(value) => onUpdate("augment", value)} />
          </div>
        </div>
      </div>

      <button className="save-config-button drawer-save" onClick={onSave} disabled={busy}>
        保存参数配置
      </button>
    </aside>
  );
}

function ClassDialog({
  dialog,
  inputRef,
  busy,
  onChange,
  onCancel,
  onSubmit
}: {
  dialog: NonNullable<ClassDialogState>;
  inputRef: RefObject<HTMLInputElement | null>;
  busy: boolean;
  onChange: (value: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const title = dialog.mode === "add" ? "添加类别" : dialog.mode === "edit" ? "编辑类别" : "删除类别";
  const confirmText = dialog.mode === "delete" ? "删除" : "保存";
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget) onCancel();
    }}>
      <section className="class-dialog" role="dialog" aria-modal="true" aria-label={title}>
        <h2>{title}</h2>
        {dialog.mode === "delete" ? (
          <p>确定删除类别“{dialog.value}”吗？</p>
        ) : (
          <input
            ref={inputRef}
            value={dialog.value}
            placeholder="输入类别名称"
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") onSubmit();
              if (event.key === "Escape") onCancel();
            }}
          />
        )}
        {dialog.error && <div className="dialog-error">{dialog.error}</div>}
        <div className="dialog-actions">
          <button type="button" onClick={onCancel} disabled={busy}>取消</button>
          <button type="button" className={dialog.mode === "delete" ? "danger-confirm" : "primary-confirm"} onClick={onSubmit} disabled={busy}>
            {confirmText}
          </button>
        </div>
      </section>
    </div>
  );
}

function cameraAccessErrorMessage(err: unknown) {
  const name = err instanceof DOMException || err instanceof Error ? err.name : "";
  switch (name) {
    case "NotAllowedError":
    case "SecurityError":
      return "摄像头权限被拒绝，请允许本应用访问摄像头。";
    case "NotFoundError":
    case "DevicesNotFoundError":
      return "未发现可用摄像头，请检查设备连接。";
    case "NotReadableError":
    case "TrackStartError":
      return "摄像头可能被其他程序占用，请关闭占用摄像头的软件后重试。";
    case "OverconstrainedError":
    case "ConstraintNotSatisfiedError":
      return "选中的摄像头不可用，请刷新列表后选择其他摄像头。";
    default:
      return err instanceof Error ? err.message : String(err);
  }
}

function normalizeProjectState(state: ProjectState): ProjectState {
  return {
    ...state,
    classes: state.classes ?? [],
    trainImages: state.trainImages ?? [],
    trainingRuns: state.trainingRuns ?? [],
    predictionRuns: state.predictionRuns ?? []
  };
}

function readStoredValue(key: string) {
  try {
    return window.localStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}

function writeStoredValue(key: string, value: string) {
  try {
    const clean = value.trim();
    if (clean) {
      window.localStorage.setItem(key, clean);
    } else {
      window.localStorage.removeItem(key);
    }
  } catch {
    // Local storage is a convenience only; the app can run without it.
  }
}

function TaskLogCard({ active, label, task, fallback, onClick }: { active: boolean; label: string; task: Task | null; fallback: string; onClick: () => void }) {
  const latestLog = task?.logs?.at(-1)?.message;
  return (
    <button className={active ? "task-log-card active" : "task-log-card"} type="button" onClick={onClick}>
      <span>{label}</span>
      <strong>{task ? taskStatusLabel(task.status) : "未开始"}</strong>
      <small>{latestLog || fallback}</small>
    </button>
  );
}

function PreflightStatus({
  report,
  errors,
  warnings,
  onCheck,
  disabled
}: {
  report: PreflightReport | null;
  errors: number;
  warnings: number;
  onCheck: () => void;
  disabled: boolean;
}) {
  const tone = !report ? "idle" : errors ? "error" : warnings ? "warning" : "ok";
  const details = report?.checks.filter((check) => check.level !== "ok").slice(0, 2) ?? [];
  return (
    <div className={`preflight-panel ${tone}`}>
      <div className="preflight-head">
        <strong>{preflightStatusText(report, errors, warnings)}</strong>
        <span>{report ? `检查项 ${report.checks.length}` : "训练前自动检查目录、模型和环境"}</span>
      </div>
      <button type="button" onClick={onCheck} disabled={disabled}>
        立即体检
      </button>
      <div className="preflight-details">
        {details.length ? (
          details.map((item) => (
            <span key={item.key}>{item.label}: {item.message}</span>
          ))
        ) : (
          <span>{report ? "可以开始训练。" : "点击体检可提前发现缺图片、缺标签、模型路径不可用等问题。"}</span>
        )}
      </div>
    </div>
  );
}

function GroupBox({
  title,
  children,
  className = "",
  layoutId
}: {
  title: string;
  children: ReactNode;
  className?: string;
  layoutId?: string;
}) {
  const cleanTitle = title.replace(/^\s*\d+\.\s*/, "");
  return (
    <section className={["group-box", className].filter(Boolean).join(" ")} data-layout-id={layoutId} data-layout-label={cleanTitle}>
      <h2>{cleanTitle}</h2>
      <div className="group-content">{children}</div>
    </section>
  );
}

function DirectoryRows({ paths, openPath }: { paths: ProjectPaths | null; openPath: (path?: string) => void }) {
  const rows = [
    ["图片目录:", paths?.imagesDir, "打开"],
    ["标注目录:", paths?.labelmeDir ?? paths?.xmlDir, "打开"],
    ["标签目录:", paths?.labelsDir, "打开"],
    ["输出目录:", paths?.runsDir, "打开"],
    ["测试目录:", paths?.predictDir, "打开"],
    ["类别文件:", paths?.classesFile, "编辑"],
    ["YAML文件:", paths?.yamlFile, "编辑"],
    ["训练清单:", paths?.trainListFile, "查看"],
    ["验证清单:", paths?.valListFile, "查看"]
  ];
  return (
    <div className="directory-grid">
      {rows.map(([label, path, action]) => (
        <div className="directory-row" key={label}>
          <label>{label}</label>
          <input value={path ?? ""} readOnly />
          <button onClick={() => openPath(path)} disabled={!path}>{action}</button>
        </div>
      ))}
    </div>
  );
}

function TrainingResultsBox({
  title = "训练结果",
  className = "",
  result,
  runs,
  selectedRunDir,
  onSelectRun,
  openPath,
  onUseModel,
  footer,
  layoutId
}: {
  title?: string;
  className?: string;
  result: TrainingResult | null;
  runs: TrainingResult[];
  selectedRunDir: string;
  onSelectRun: (runDir: string) => void;
  openPath: (path?: string) => void;
  onUseModel?: (modelPath: string) => void;
  footer?: ReactNode;
  layoutId?: string;
}) {
  const metrics = result?.metrics ?? {};
  const curves = result?.artifacts?.find((path) => /results\.(png|jpg|jpeg)$/i.test(path));
  const modelFolder = result?.modelPath ? result.modelPath.split(/[\\/]/).slice(0, -1).join("\\") : undefined;
  const manifest = result?.manifest;
  const testResult = className.includes("test-results-box");
  const compactResult = testResult || className.includes("directory-results-box");
  const showManifestRows = Boolean(manifest) && !compactResult;
  if (testResult) {
    return (
      <GroupBox title={title} className={className} layoutId={layoutId}>
        <div className="run-select-row">
          <label>模型记录:</label>
          <select value={selectedRunDir} onChange={(event) => onSelectRun(event.target.value)} disabled={!runs.length}>
            {runs.length ? (
              runs.map((run, index) => (
                <option key={run.runDir} value={run.runDir}>
                  {`${index === 0 ? "最新 - " : ""}${formatTime(run.modifiedAt)} / ${shortPath(run.modelPath || run.runDir)}`}
                </option>
              ))
            ) : (
              <option value="">暂无训练记录</option>
            )}
          </select>
        </div>
        <div className="test-result-brief">
          <span>模型</span>
          <strong>{result?.modelPath ? shortPath(result.modelPath) : "未训练"}</strong>
          <span>时间</span>
          <strong>{formatTime(result?.modifiedAt)}</strong>
        </div>
        <div className="metric-row">
          <Metric title="mAP50" value={formatMetric(metrics.map50)} />
          <Metric title="mAP50-95" value={formatMetric(metrics.map)} />
          <Metric title="精确率" value={formatPercent(metrics.precision)} />
          <Metric title="召回率" value={formatPercent(metrics.recall)} />
        </div>
        <div className="result-buttons three">
          <button title="查看训练曲线" onClick={() => openPath(curves)} disabled={!curves}>
            <span>曲线</span>
          </button>
          <button title="打开模型文件夹" onClick={() => openPath(modelFolder)} disabled={!modelFolder}>
            <span>模型</span>
          </button>
          <button title="用于模型测试" onClick={() => result?.modelPath && onUseModel?.(result.modelPath)} disabled={!result?.modelPath}>
            <span>用于测试</span>
          </button>
        </div>
      </GroupBox>
    );
  }
  return (
    <GroupBox title={title} className={className} layoutId={layoutId}>
      <div className="run-select-row">
        <label>模型记录:</label>
        <select value={selectedRunDir} onChange={(event) => onSelectRun(event.target.value)} disabled={!runs.length}>
          {runs.length ? (
            runs.map((run, index) => (
              <option key={run.runDir} value={run.runDir}>
                {`${index === 0 ? "最新 - " : ""}${formatTime(run.modifiedAt)} / ${shortPath(run.modelPath || run.runDir)}`}
              </option>
            ))
          ) : (
            <option value="">暂无训练记录</option>
          )}
        </select>
      </div>
      <div className="result-info">
        <label>最佳模型:</label>
        <strong>{result?.modelPath ? shortPath(result.modelPath) : "未训练"}</strong>
        <label>训练时间:</label>
        <span>{formatTime(result?.modifiedAt)}</span>
        <label>训练图片:</label>
        <span>{result?.imageCount ?? "-"}</span>
        <label>类别数量:</label>
        <span>{result?.classCount ?? "-"}</span>
        {showManifestRows && (
          <>
            <label>参数快照:</label>
            <span>{formatRunSnapshot(manifest)}</span>
            <label>标签文件:</label>
            <span>{manifest?.labelCount ?? "-"}</span>
            <label>验证拆分:</label>
            <span>{formatDatasetSplit(manifest)}</span>
          </>
        )}
      </div>
      <div className="metric-row">
        <Metric title="mAP50" value={formatMetric(metrics.map50)} />
        <Metric title="mAP50-95" value={formatMetric(metrics.map)} />
        <Metric title="精确率" value={formatPercent(metrics.precision)} />
        <Metric title="召回率" value={formatPercent(metrics.recall)} />
      </div>
      <div className={onUseModel ? "result-buttons three" : "result-buttons"}>
        <button title="查看训练曲线" onClick={() => openPath(curves)} disabled={!curves}>
          <span>查看训练曲线</span>
        </button>
        <button title="打开模型文件夹" onClick={() => openPath(modelFolder)} disabled={!modelFolder}>
          <span>打开模型文件夹</span>
        </button>
        {onUseModel && (
          <button title="用于模型测试" onClick={() => result?.modelPath && onUseModel(result.modelPath)} disabled={!result?.modelPath}>
            <span>用于测试</span>
          </button>
        )}
      </div>
      {footer && <div className="training-result-footer">{footer}</div>}
    </GroupBox>
  );
}

function TaskResultSplit({
  convertTask,
  validateTask,
  disabled,
  onConvert,
  onValidate
}: {
  convertTask: Task | null;
  validateTask: Task | null;
  disabled: boolean;
  onConvert: () => void | Promise<void>;
  onValidate: () => void | Promise<void>;
}) {
  return (
    <div className="task-result-split">
      <TaskActionCard
        title="标注转YOLO格式"
        task={convertTask}
        disabled={disabled}
        onClick={onConvert}
        summary={convertTaskSummary(convertTask)}
      />
      <TaskActionCard
        title="验证标签"
        task={validateTask}
        disabled={disabled}
        onClick={onValidate}
        summary={validateTaskSummary(validateTask)}
      />
    </div>
  );
}

function TaskActionCard({
  title,
  task,
  disabled,
  onClick,
  summary
}: {
  title: string;
  task: Task | null;
  disabled: boolean;
  onClick: () => void | Promise<void>;
  summary: { main: string; detail: string };
}) {
  const running = isTaskRunning(task);
  const done = task?.status === "succeeded";
  const failed = task?.status === "failed" || task?.status === "canceled";
  return (
    <div className={["task-action-card", running ? "running" : "", done ? "done" : "", failed ? "failed" : ""].filter(Boolean).join(" ")}>
      <button className="task-action-button" type="button" onClick={() => void onClick()} disabled={disabled || running}>
        <span>{running ? "正在处理" : done ? `${title} 完成` : failed ? `${title} 失败` : title}</span>
      </button>
      <div className="task-action-summary">
        <strong>{summary.main}</strong>
        <span>{summary.detail}</span>
      </div>
    </div>
  );
}

function convertTaskSummary(task: Task | null) {
  if (!task) return { main: "待执行", detail: "" };
  const report = task.result as ConvertReport | undefined;
  if (task.status === "running" || task.status === "pending") {
    return { main: taskStatusLabel(task.status), detail: `进度 ${task.progress ?? 0}%` };
  }
  if (task.status === "failed" || task.status === "canceled") {
    return { main: taskStatusLabel(task.status), detail: task.error || "请查看训练日志" };
  }
  return {
    main: `已转换 ${report?.convertedFiles ?? 0}`,
    detail: `目标框 ${report?.boxes ?? 0} / 问题 ${report?.errors?.length ?? 0}`
  };
}

function validateTaskSummary(task: Task | null) {
  if (!task) return { main: "待执行", detail: "" };
  const report = task.result as ValidationReport | undefined;
  if (task.status === "running" || task.status === "pending") {
    return { main: taskStatusLabel(task.status), detail: `进度 ${task.progress ?? 0}%` };
  }
  if (task.status === "failed" || task.status === "canceled") {
    return { main: taskStatusLabel(task.status), detail: task.error || "请查看训练日志" };
  }
  const invalidCount = report ? Object.keys(report.invalidLabels ?? {}).length : 0;
  const problemCount = (report?.missingLabels?.length ?? 0) + (report?.emptyLabels?.length ?? 0) + invalidCount;
  return {
    main: problemCount ? `问题 ${problemCount}` : "验证通过",
    detail: `有效 ${report?.validLabels ?? 0} / 目标框 ${report?.totalBoxes ?? 0}`
  };
}

function ConvertResultSummary({ task }: { task: Task }) {
  const report = task.result as ConvertReport | undefined;
  const sourceFiles = (report?.xmlFiles ?? 0) + (report?.jsonFiles ?? 0);
  const errorCount = report?.errors?.length ?? 0;
  const tone = task.status === "failed" || errorCount ? "warning" : task.status === "succeeded" ? "ok" : "running";
  const headline = task.status === "succeeded"
    ? `已生成 ${report?.convertedFiles ?? 0} 个YOLO标签，包含 ${report?.boxes ?? 0} 个目标框。`
    : task.status === "failed"
      ? task.error || "转换失败，请查看训练日志。"
      : "正在转换标注文件，请稍等。";
  return (
    <div className={`task-result-summary ${tone}`}>
      <div className="task-summary-head">
        <strong>标注转换</strong>
        <span>{taskStatusLabel(task.status)}</span>
      </div>
      <p>{headline}</p>
      <div className="task-summary-chips">
        <span>源文件 {sourceFiles}</span>
        <span>已转换 {report?.convertedFiles ?? 0}</span>
        <span>跳过目标 {report?.skippedObjects ?? 0}</span>
        <span>需处理 {errorCount}</span>
      </div>
    </div>
  );
}

function ValidationResultSummary({ task }: { task: Task }) {
  const report = task.result as ValidationReport | undefined;
  const invalidCount = report ? Object.keys(report.invalidLabels ?? {}).length : 0;
  const missingCount = report?.missingLabels?.length ?? 0;
  const emptyCount = report?.emptyLabels?.length ?? 0;
  const problemCount = missingCount + emptyCount + invalidCount;
  const tone = task.status === "failed" || problemCount ? "warning" : task.status === "succeeded" ? "ok" : "running";
  const headline = task.status === "succeeded"
    ? problemCount
      ? `发现 ${problemCount} 处需要处理：缺标签 ${missingCount}，空标签 ${emptyCount}，格式错误 ${invalidCount}。`
      : `验证通过：${report?.validLabels ?? 0} 个标签文件可用，共 ${report?.totalBoxes ?? 0} 个目标框。`
    : task.status === "failed"
      ? task.error || "验证失败，请查看训练日志。"
      : "正在检查图片和YOLO标签是否一一对应。";
  return (
    <div className={`task-result-summary ${tone}`}>
      <div className="task-summary-head">
        <strong>标签验证</strong>
        <span>{taskStatusLabel(task.status)}</span>
      </div>
      <p>{headline}</p>
      <div className="task-summary-chips">
        <span>图片 {report?.imageCount ?? 0}</span>
        <span>有效标签 {report?.validLabels ?? 0}</span>
        <span>目标框 {report?.totalBoxes ?? 0}</span>
        <span>问题 {problemCount}</span>
      </div>
    </div>
  );
}

function TrainingOverviewBox({ project, config, result }: { project: ProjectState | null; config: TrainingConfig; result: TrainingResult | null }) {
  const splitText = formatDatasetSplit(result?.manifest);
  const modelText = config.model ? shortPath(config.model) : "-";
  return (
    <GroupBox title="训练配置速览" className="training-overview-box">
      <div className="overview-metrics">
        <Metric title="训练图片" value={String(project?.sampleCount ?? 0)} />
        <Metric title="标签文件" value={String(project?.labelCount ?? 0)} />
        <Metric title="类别" value={String(project?.classes.length ?? 0)} />
        <Metric title="模型" value={modelText} />
        <Metric title="轮数" value={`${config.epochs}`} />
        <Metric title="批次" value={`${config.batchSize}`} />
        <Metric title="尺寸" value={`${config.imageSize}`} />
        <Metric title="设备" value={config.device} />
      </div>
      <div className="overview-lines">
        <span>验证集: {splitText}</span>
        <span>输出: {project?.paths.runsDir ? shortPath(project.paths.runsDir) : "-"}</span>
        <span>最新模型: {result?.modelPath ? shortPath(result.modelPath) : "未训练"}</span>
      </div>
    </GroupBox>
  );
}

function ProjectSummaryBox({ project }: { project: ProjectState | null }) {
  return (
    <GroupBox title="项目概览" className="directory-summary-box">
      <div className="summary-grid">
        <Metric title="训练图片" value={String(project?.sampleCount ?? 0)} />
        <Metric title="标签文件" value={String(project?.labelCount ?? 0)} />
        <Metric title="类别数量" value={String(project?.classes.length ?? 0)} />
        <Metric title="测试目录" value={project ? "已创建" : "-"} />
      </div>
      <div className="summary-note">
        {project ? shortPath(project.rootPath) : "载入项目后会自动创建训练、标注、输出和测试目录。"}
      </div>
    </GroupBox>
  );
}

function PredictionResultBox({
  task,
  result,
  history,
  selectedRunId,
  onChangeHistory,
  onSelectHistory,
  openPath,
  onCancel,
  onClear
}: {
  task: Task | null;
  result: PredictionResult | null;
  history: PredictionResult[];
  selectedRunId: string;
  onChangeHistory: (runId: string) => void;
  onSelectHistory: (runId: string) => void;
  openPath: (path?: string) => void;
  onCancel: () => void;
  onClear: () => void;
}) {
  const running = task?.status === "running" || task?.status === "pending";
  const detections = result?.detections ?? [];
  const topDetections = detections.slice(0, 4);
  return (
    <div className="prediction-box">
      <div className="prediction-history-row">
        <label>历史记录:</label>
        <select value={selectedRunId} onChange={(event) => onChangeHistory(event.target.value)} disabled={!history.length || running}>
          {history.length ? (
            history.map((item) => (
              <option key={item.runId || item.outputDir} value={item.runId}>
                {predictionHistoryLabel(item)}
              </option>
            ))
          ) : (
            <option value="">暂无模型测试记录</option>
          )}
        </select>
        <button type="button" onClick={() => onSelectHistory(selectedRunId)} disabled={!selectedRunId || running}>
          载入
        </button>
      </div>
      <div className="prediction-meta">
        <label>任务状态:</label>
        <strong>{task ? taskStatusLabel(task.status) : "未开始"}</strong>
        <label>检测目标:</label>
        <span>{result ? `${result.detectionCount ?? detections.length} 个` : "-"}</span>
        <label>测试模型:</label>
        <span>{result?.modelPath ? shortPath(result.modelPath) : "-"}</span>
        <label>输出目录:</label>
        <span>{result?.outputDir ? shortPath(result.outputDir) : "-"}</span>
        <label>完成时间:</label>
        <span>{formatTime(result?.ranAt)}</span>
      </div>
      <div className="detection-summary">
        {result ? (
          topDetections.length ? (
            topDetections.map((item, index) => (
              <div className="detection-chip" key={`${item.labelPath}-${index}`}>
                <strong>{item.className || item.classId}</strong>
                <span>{formatConfidence(item.confidence)}</span>
              </div>
            ))
          ) : (
            <div className="detection-empty">没有检测到目标</div>
          )
        ) : (
          <div className="detection-empty">完成测试后显示检测类别与置信度</div>
        )}
      </div>
      {running && (
        <div className="progress-bar compact">
          <div style={{ width: `${task?.progress ?? 0}%` }} />
        </div>
      )}
      <div className="result-preview">
        {result?.outputImage ? (
          <img src={fileImageUrl(result.outputImage)} alt="模型识别结果" />
        ) : (
          <div className="preview-empty">识别结果会显示在这里</div>
        )}
      </div>
      <div className="prediction-actions">
        <button className="danger-action" title="停止测试" onClick={onCancel} disabled={!running}>
          停止
        </button>
        <button title="打开结果图" onClick={() => openPath(result?.outputImage)} disabled={!result?.outputImage}>
          结果图
        </button>
        <button title="打开输出目录" onClick={() => openPath(result?.outputDir)} disabled={!result?.outputDir}>
          输出目录
        </button>
        <button title="清空结果" onClick={onClear} disabled={!task && !result}>
          清空
        </button>
      </div>
    </div>
  );
}

function Metric({ title, value }: { title: string; value: string }) {
  return (
    <div className="metric-card">
      <span>{title}</span>
      <strong>{value}</strong>
    </div>
  );
}

function ParamInlineNumber({
  title,
  value,
  min,
  max,
  step = 1,
  suffix,
  onChange
}: {
  title: string;
  value: number;
  min: number;
  max: number;
  step?: number;
  suffix?: string;
  onChange: (value: number) => void;
}) {
  return (
    <label className="param-inline-control">
      <span>{title}</span>
      <input type="number" value={value} min={min} max={max} step={step} onChange={(event) => onChange(Number(event.target.value))} />
      {suffix && <em>{suffix}</em>}
    </label>
  );
}

function ParamInlineSelect({ title, value, options, labels, onChange }: { title: string; value: string; options: string[]; labels?: string[]; onChange: (value: string) => void }) {
  const currentLabel = labels ? labels[options.indexOf(value)] ?? value : value;
  return (
    <label className="param-inline-control">
      <span>{title}</span>
      <select value={value} title={currentLabel} onChange={(event) => onChange(event.target.value)}>
        {options.map((option, i) => (
          <option key={option || i} value={option}>{labels ? labels[i] : option}</option>
        ))}
      </select>
    </label>
  );
}

function ParamInlineText({
  title,
  value,
  placeholder,
  onChange
}: {
  title: string;
  value: string;
  placeholder?: string;
  onChange: (value: string) => void;
}) {
  return (
    <label className="param-inline-control full">
      <span>{title}</span>
      <input type="text" value={value} placeholder={placeholder} onChange={(event) => onChange(event.target.value)} />
    </label>
  );
}

function ParamInlineToggle({ title, checked, onChange }: { title: string; checked: boolean; onChange: (value: boolean) => void }) {
  return (
    <label className="param-inline-control toggle">
      <span>{title}</span>
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
    </label>
  );
}

function NumberField({ label, value, min, max, step = 1, suffix, onChange }: { label: string; value: number; min: number; max: number; step?: number; suffix?: string; onChange: (value: number) => void }) {
  return (
    <>
      <label className="param-label">{label}</label>
      <div className="number-wrap">
        <input type="number" value={value} min={min} max={max} step={step} onChange={(event) => onChange(Number(event.target.value))} />
        {suffix && <span>{suffix}</span>}
      </div>
    </>
  );
}

function SelectField({ label, value, options, onChange }: { label: string; value: string; options: string[]; onChange: (value: string) => void }) {
  return (
    <>
      <label className="param-label">{label}</label>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map((option) => (
          <option key={option} value={option}>{option}</option>
        ))}
      </select>
    </>
  );
}

function sampleRecommendation(count: number) {
  if (count < 10) return "样本量过少（<10张），建议：\n- 训练轮数: 100-200\n- 批次大小: 2\n- 数据增强: 高(1.0)\n- 早停耐心值: 30";
  if (count < 30) return "小样本集（10-30张），建议：\n- 训练轮数: 80-120\n- 批次大小: 4\n- 数据增强: 中高(0.8)\n- 早停耐心值: 25";
  if (count < 100) return "中等样本集（30-100张），建议：\n- 训练轮数: 50-80\n- 批次大小: 4-8\n- 数据增强: 中(0.5)\n- 早停耐心值: 20";
  return "充足样本集（>100张），建议：\n- 训练轮数: 30-50\n- 批次大小: 8-16\n- 数据增强: 低(0.3)\n- 早停耐心值: 15";
}

function isTaskRunning(task: Task | null) {
  return task?.status === "running" || task?.status === "pending";
}

function preflightStatusText(report: PreflightReport | null, errors: number, warnings: number) {
  if (!report) return "尚未体检";
  if (errors) return `${errors} 个错误`;
  if (warnings) return `${warnings} 个提醒`;
  return "体检通过";
}

function taskStatusLabel(status: Task["status"]) {
  switch (status) {
    case "pending":
      return "等待中";
    case "running":
      return "运行中";
    case "succeeded":
      return "已完成";
    case "failed":
      return "失败";
    case "canceled":
      return "已停止";
    default:
      return status;
  }
}

function preflightSummary(checks: Array<{ label: string; message: string; hint?: string }>) {
  return checks.map((check) => `- ${check.label}: ${check.message}${check.hint ? `\n  ${check.hint}` : ""}`).join("\n");
}

function closeProtectionLabel(training: boolean, predicting: boolean) {
  if (training && predicting) return "训练和模型测试运行中";
  if (training) return "训练运行中";
  if (predicting) return "模型测试运行中";
  return "";
}

function importSummary(imported: number, skipped: string[]) {
  if (!skipped.length) {
    return `已复制 ${imported} 张图片到训练目录。`;
  }
  const details = skipped.slice(0, 2).map((item) => item.split(/[\\/]/).pop() || item).join("；");
  const more = skipped.length > 2 ? `，另有 ${skipped.length - 2} 项` : "";
  return `已复制 ${imported} 张图片，跳过 ${skipped.length} 项：${details}${more}`;
}

function probabilityValue(value: number) {
  return value.toFixed(1);
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function formatTime(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function formatMetric(value?: number) {
  return typeof value === "number" ? value.toFixed(3) : "-";
}

function formatPercent(value?: number) {
  return typeof value === "number" ? `${(value * 100).toFixed(1)}%` : "-";
}

function formatRunSnapshot(manifest?: TrainingResult["manifest"]) {
  if (!manifest) return "未记录";
  return `${manifest.config.epochs}轮 / ${manifest.config.imageSize}px / ${manifest.config.device}`;
}

function formatDatasetSplit(manifest?: TrainingResult["manifest"]) {
  if (!manifest) return "未记录";
  const train = manifest.trainingImages || manifest.imageCount || 0;
  const val = manifest.validationImages || 0;
  const suffix = manifest.validationMirrored ? " / 复用验证" : "";
  return `训练 ${train} / 验证 ${val}${suffix}`;
}

function formatConfidence(value?: number) {
  return typeof value === "number" && value > 0 ? `${(value * 100).toFixed(1)}%` : "-";
}

function predictionHistoryLabel(result: PredictionResult) {
  const count = result.detectionCount ?? result.detections?.length ?? 0;
  const confidence = typeof result.confidence === "number" && result.confidence > 0 ? ` / conf ${result.confidence.toFixed(2)}` : "";
  return `${formatTime(result.ranAt)} / ${count}个目标${confidence}`;
}

function latestTaskTime(task: Task) {
  const candidates = [task.finishedAt, task.startedAt, task.createdAt, task.logs?.at(-1)?.at].filter(Boolean) as string[];
  for (const value of candidates) {
    const time = new Date(value).getTime();
    if (!Number.isNaN(time)) return time;
  }
  return 0;
}

function readAutoAnnotateReport(task: Task | null): AutoAnnotateReport | null {
  if (!task?.result || typeof task.result !== "object") return null;
  const result = task.result as Partial<AutoAnnotateReport>;
  return {
    imageCount: typeof result.imageCount === "number" ? result.imageCount : 0,
    annotatedFiles: typeof result.annotatedFiles === "number" ? result.annotatedFiles : 0,
    skippedFiles: typeof result.skippedFiles === "number" ? result.skippedFiles : 0,
    shapeCount: typeof result.shapeCount === "number" ? result.shapeCount : 0,
    outputDir: typeof result.outputDir === "string" ? result.outputDir : "",
    errors: Array.isArray(result.errors) ? result.errors.filter((error): error is string => typeof error === "string") : []
  };
}

function describeAutoAnnotateTask(task: Task | null) {
  if (!task) return { tone: "idle", label: "未开始", detail: "" };
  if (task.status === "pending" || task.status === "running") {
    return { tone: "running", label: taskStatusLabel(task.status), detail: "自动标注正在运行。" };
  }
  if (task.status === "failed") {
    return { tone: "error", label: "失败", detail: task.error || "任务执行失败，请查看日志。" };
  }
  if (task.status === "canceled") {
    return { tone: "warning", label: "已停止", detail: "任务已由用户停止。" };
  }
  const report = readAutoAnnotateReport(task);
  if (!report) {
    return { tone: "warning", label: "结果未知", detail: "任务没有返回自动标注报告。" };
  }
  const firstError = report.errors[0] ?? "";
  if (report.annotatedFiles === 0 && report.errors.length) {
    return { tone: "error", label: "失败（0 输出）", detail: `${report.errors.length} 个错误；${firstError}` };
  }
  if (report.errors.length) {
    return { tone: "warning", label: `有警告（${report.errors.length}）`, detail: firstError };
  }
  if (report.annotatedFiles === 0) {
    return { tone: "warning", label: "0 输出", detail: `没有生成 JSON，跳过 ${report.skippedFiles} 张图片。` };
  }
  return { tone: "ok", label: `成功（${report.annotatedFiles}）`, detail: `生成 ${report.annotatedFiles} 个 JSON、${report.shapeCount} 个目标。` };
}

function taskExitCode(task: Task | null) {
  if (!task) return "-";
  if (task.status === "succeeded") return "0";
  if (task.status === "failed") return "1";
  if (task.status === "canceled") return "已停止";
  return "-";
}

function taskDuration(task: Task | null) {
  if (!task?.startedAt) return "-";
  const start = new Date(task.startedAt).getTime();
  const end = task.finishedAt ? new Date(task.finishedAt).getTime() : Date.now();
  if (Number.isNaN(start) || Number.isNaN(end) || end < start) return "-";
  return formatDuration(end - start);
}

function formatDuration(ms: number) {
  const totalSeconds = Math.max(0, Math.round(ms / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

function taskLogs(task: Task | null) {
  if (!task) return [];
  return (task.logs ?? []).map((log) => ({
    at: log.at,
    message: `[${taskTypeLabel(task.type)}] ${log.message}`
  }));
}

function taskErrorHeadline(error: string | undefined, fallback: string) {
  const firstLine = error?.split(/\r?\n/).map((line) => line.trim()).find(Boolean);
  if (!firstLine) return fallback;
  return firstLine.length > 240 ? `${firstLine.slice(0, 237)}...` : firstLine;
}

function taskTypeLabel(type: string) {
  switch (type) {
    case "train":
      return "训练";
    case "predict":
      return "模型测试";
    case "auto-annotate":
      return "自动标注";
    case "convert-voc":
      return "标注转换";
    case "validate-labels":
      return "标签验证";
    default:
      return type;
  }
}

function shortPath(path: string) {
  const parts = path.split(/[\\/]/);
  return parts.slice(-3).join("/");
}

function fileImageUrl(path: string) {
  return `/api/files/image?${query({ path })}`;
}

function LayoutInspector({ onClose }: { onClose: () => void }) {
  const [rects, setRects] = useState<LayoutRect[]>([]);
  const [overrides, setOverrides] = useState<Record<string, LayoutRect>>({});
  const [activeId, setActiveId] = useState("");
  const dragRef = useRef<DragState | null>(null);

  useEffect(() => {
    const refresh = () => {
      const next = Array.from(document.querySelectorAll<HTMLElement>("[data-layout-id]"))
        .filter((element) => element.offsetParent !== null)
        .map((element) => {
          const box = element.getBoundingClientRect();
          const id = element.dataset.layoutId || "";
          return {
            id,
            label: element.dataset.layoutLabel || id,
            x: Math.round(box.left),
            y: Math.round(box.top),
            width: Math.round(box.width),
            height: Math.round(box.height)
          };
        })
        .filter((item) => item.id);
      setRects(next);
      setOverrides((current) => {
        const valid = new Set(next.map((item) => item.id));
        return Object.fromEntries(Object.entries(current).filter(([id]) => valid.has(id)));
      });
    };
    refresh();
    const timer = window.setInterval(refresh, 500);
    window.addEventListener("resize", refresh);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("resize", refresh);
    };
  }, []);

  useEffect(() => {
    const handlePointerMove = (event: PointerEvent) => {
      const drag = dragRef.current;
      if (!drag) return;
      const dx = event.clientX - drag.startX;
      const dy = event.clientY - drag.startY;
      setOverrides((current) => {
        const base = drag.startRect;
        const next = drag.mode === "move"
          ? { ...base, x: Math.round(base.x + dx), y: Math.round(base.y + dy) }
          : { ...base, width: Math.max(24, Math.round(base.width + dx)), height: Math.max(24, Math.round(base.height + dy)) };
        return { ...current, [drag.id]: next };
      });
    };
    const handlePointerUp = () => {
      dragRef.current = null;
    };
    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp);
    return () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
    };
  }, []);

  const merged = rects.map((rect) => overrides[rect.id] ?? rect);
  const output = merged
    .map((rect) => `${rect.id} (${rect.label}): x=${rect.x}, y=${rect.y}, w=${rect.width}, h=${rect.height}`)
    .join("\n");

  function beginDrag(event: ReactPointerEvent, rect: LayoutRect, mode: DragState["mode"]) {
    event.preventDefault();
    event.stopPropagation();
    setActiveId(rect.id);
    dragRef.current = {
      id: rect.id,
      mode,
      startX: event.clientX,
      startY: event.clientY,
      startRect: rect
    };
  }

  async function copyOutput() {
    const text = `布局调试坐标\n${output}`;
    await navigator.clipboard?.writeText(text);
  }

  return (
    <div className="layout-inspector" aria-label="布局调试模式">
      <div className="layout-inspector-toolbar">
        <strong>布局调试模式</strong>
        <span>拖动框移动，拖右下角缩放。Ctrl+Space 关闭。</span>
        <button type="button" onClick={() => setOverrides({})}>重置</button>
        <button type="button" onClick={() => void copyOutput()}>复制坐标</button>
        <button type="button" onClick={onClose}>关闭</button>
      </div>
      <div className="layout-inspector-stage">
        {merged.map((rect) => (
          <div
            key={rect.id}
            className={activeId === rect.id ? "layout-frame active" : "layout-frame"}
            style={{ left: rect.x, top: rect.y, width: rect.width, height: rect.height }}
            onPointerDown={(event) => beginDrag(event, rect, "move")}
          >
            <span>{rect.label}</span>
            <small>{rect.x}, {rect.y} / {rect.width} × {rect.height}</small>
            <button
              type="button"
              aria-label={`${rect.label} 缩放`}
              className="layout-resize-handle"
              onPointerDown={(event) => beginDrag(event, rect, "resize")}
            />
          </div>
        ))}
      </div>
      <textarea className="layout-inspector-output" value={output} readOnly />
    </div>
  );
}
