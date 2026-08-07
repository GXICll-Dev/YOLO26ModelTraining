export type TaskStatus = "pending" | "running" | "succeeded" | "failed" | "canceled";

export interface TrainingConfig {
  version?: number;
  projectRoot?: string;
  epochs: number;
  batchSize: number;
  imageSize: number;
  device: string;
  learningRate: number;
  momentum: number;
  weightDecay: number;
  patience: number;
  workers: number;
  fliplr: number;
  flipud: number;
  mosaic: number;
  mixup: number;
  copyPaste: number;
  augment: boolean;
  model: string;
}

export interface ProjectPaths {
  imagesDir: string;
  valImagesDir: string;
  xmlDir: string;
  labelmeDir: string;
  labelsDir: string;
  valLabelsDir: string;
  runsDir: string;
  predictDir: string;
  classesFile: string;
  yamlFile: string;
  trainListFile: string;
  valListFile: string;
}

export interface ImageInfo {
  name: string;
  path: string;
  size: number;
  modified: string;
}

export interface TrainingResult {
  runDir: string;
  modelPath: string;
  metrics: Record<string, number>;
  artifacts: string[];
  modifiedAt: string;
  imageCount: number;
  classCount: number;
  manifest?: TrainingManifest;
}

export interface TrainingManifest {
  version: number;
  runDir: string;
  modelPath: string;
  resolvedModel: string;
  config: TrainingConfig;
  classes: string[];
  imageCount: number;
  classCount: number;
  labelCount: number;
  trainingImages: number;
  validationImages: number;
  validationMirrored: boolean;
  command: string;
  commandArgs: string[];
  yoloCommand: string;
  yoloCommandArgs: string[];
  ultralyticsDir?: string;
  startedAt: string;
  finishedAt: string;
  status: string;
  error?: string;
}

export interface PredictionConfig {
  projectRoot?: string;
  imagePath: string;
  modelPath: string;
  device: string;
  imageSize: number;
  confidence: number;
}

export interface PredictionResult {
  runId: string;
  modelPath: string;
  sourceImage: string;
  outputDir: string;
  outputImage: string;
  artifacts: string[];
  detections: PredictionDetection[];
  detectionCount: number;
  ranAt: string;
  device?: string;
  imageSize?: number;
  confidence?: number;
}

export interface PredictionDetection {
  classId: number;
  className: string;
  confidence: number;
  xCenter: number;
  yCenter: number;
  width: number;
  height: number;
  labelPath: string;
}

export interface PreflightCheck {
  key: string;
  label: string;
  level: "ok" | "warning" | "error";
  message: string;
  hint?: string;
}

export interface PreflightReport {
  mode: string;
  ready: boolean;
  errors: number;
  warnings: number;
  checks: PreflightCheck[];
}

export interface ProjectState {
  rootPath: string;
  paths: ProjectPaths;
  classes: string[];
  sampleCount: number;
  labelCount: number;
  trainImages: ImageInfo[];
  latestRun?: TrainingResult | null;
  trainingRuns: TrainingResult[];
  predictionRuns: PredictionResult[];
}

export interface LogEntry {
  at: string;
  message: string;
}

export interface Task {
  id: string;
  type: string;
  projectRoot: string;
  status: TaskStatus;
  progress: number;
  logs: LogEntry[];
  result?: unknown;
  error?: string;
  pid?: number;
  createdAt?: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface AutoAnnotateReport {
  imageCount: number;
  annotatedFiles: number;
  skippedFiles: number;
  shapeCount: number;
  outputDir: string;
  errors: string[];
}

export interface ConvertReport {
  xmlFiles: number;
  jsonFiles: number;
  convertedFiles: number;
  boxes: number;
  skippedObjects: number;
  errors: string[];
}

export interface ValidationReport {
  imageCount: number;
  labelCount: number;
  validLabels: number;
  totalBoxes: number;
  missingLabels: string[];
  emptyLabels: string[];
  invalidLabels: Record<string, string[]>;
  classCounts: Record<string, number>;
}

export interface LicenseStatus {
  valid: boolean;
  message: string;
  daysLeft: number;
  machineCode: string;
  mode: string;
}

export interface RuntimeStatus {
  phase: "checking" | "missing" | "downloading" | "verifying" | "installing" | "validating" | "ready" | "failed";
  ready: boolean;
  source?: "managed" | "bundled" | "development" | string;
  runtimeId: string;
  runtimeFlavor?: "cpu" | "cuda";
  recommendedFlavor?: "cpu" | "cuda";
  hardwareChecked?: boolean;
  hasNvidiaGPU?: boolean | null;
  gpuNames?: string[];
  recommendedDevice?: "auto" | "cpu";
  localVersion: string;
  availableVersion: string;
  runtimeRoot: string;
  manifestURL: string;
  downloadedBytes: number;
  totalBytes: number;
  percent: number;
  currentPackage: string;
  updateAvailable?: boolean;
  switchAvailable?: boolean;
  message: string;
  error: string;
}

export interface AppUpdateStatus {
  phase: "idle" | "checking" | "available" | "downloading" | "verifying" | "downloaded" | "installing" | "failed";
  currentVersion: string;
  latestVersion: string;
  updateAvailable: boolean;
  releaseName: string;
  releaseNotes: string;
  publishedAt: string;
  installerSize: number;
  downloadedBytes: number;
  percent: number;
  message: string;
  error: string;
}
