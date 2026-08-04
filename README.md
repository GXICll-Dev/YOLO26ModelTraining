# ModelTraining Go+TS

这是对原 Python/PyQt 训练工具的 Go + TypeScript 重构版本，当前默认适配 Ultralytics YOLO26。原项目目录不会被修改；新项目把职责拆成：

应用更新、Windows 安装包和独立 Runtime 仓库的完整维护流程见 [`docs/RELEASE_AND_RUNTIME_GUIDE.md`](docs/RELEASE_AND_RUNTIME_GUIDE.md)。AI 工具应从根目录 [`CLAUDE.md`](CLAUDE.md) 读取并通过 `@` 引用加载相关上下文。

- Go 本地服务：项目目录、类别文件、`mydata.yaml`、VOC XML 转 YOLO、标签校验、训练任务、日志流、授权状态。
- TypeScript 前端：目录管理、类别编辑、图片导入、工具调用、训练参数、任务日志和结果展示。
- YOLO26 训练：桌面发行包固定使用随包附带的 Python、Ultralytics 和 CUDA Torch；有兼容 NVIDIA GPU 时自动使用 CUDA，否则回退 CPU。Go 负责结构化编排和日志追踪，源码开发模式仍可通过环境变量显式指定运行时。

## 运行

后端：

```powershell
cd D:\v8源码\ModelTrainingDist_encrypted\modeltraining-go-ts
go run .\cmd\server
```

前端开发服务：

```powershell
cd D:\v8源码\ModelTrainingDist_encrypted\modeltraining-go-ts\web
npm install
npm run dev
```

打开 `http://127.0.0.1:5173`。前端会把 `/api` 代理到 Go 服务的 `http://127.0.0.1:8080`。

构建前端后，Go 服务也可以直接托管静态页面：

```powershell
cd D:\v8源码\ModelTrainingDist_encrypted\modeltraining-go-ts\web
npm run build
cd ..
go run .\cmd\server
```

然后打开 `http://127.0.0.1:8080`。

## 便携训练环境（Windows x64 CUDA + CPU 自动回退）

桌面发行包使用自包含的 Windows x64 CUDA 运行时。兼容的 Windows 10/11 x64 目标电脑不需要安装 Python、Conda、CUDA Toolkit、PyTorch 或 Ultralytics，也不依赖 `PATH`；有兼容 NVIDIA 显卡和驱动时自动使用 CUDA 0，否则同一运行时自动回退 CPU。锁定版本和下载校验值在 `scripts/runtime-win-x64-cuda.lock.json`：

- 官方 CPython 3.11.9 embeddable x64；
- `torch==2.7.1+cu126` 和 `torchvision==0.22.1+cu126` 官方 Windows CUDA 12.6 wheel；
- 锁定并随包部署 Microsoft Visual C++ app-local runtime，目标电脑不需要另装 VC++ Redistributable；
- `opencv-python-headless==4.11.0.86` 以及精确锁定的训练依赖；
- 从本地 `third_party/ultralytics-8.4.10` 只安装约 8 MB 的 Python 包，不复制其中的数据集、历史 runs 和权重集合；
- 默认模型固定为项目根目录的 `yolo26n.pt`。

训练、图片预测、自动标注和摄像头识别固定使用内置 Python，发行版不会枚举或调用目标电脑的 Conda/本机 Python。默认设备均为 `auto`：每个实际任务进程都会先执行一个 CUDA 张量运算，成功才使用 NVIDIA GPU 0，失败则显式使用 CPU。因此即使驱动能识别显卡、但当前 Torch 不支持该 GPU 架构（例如返回 `no kernel image`），也不会在训练中途崩溃。仍可在界面中显式选择“仅 CPU”或“NVIDIA CUDA 0”；显式 CUDA 未通过张量测试时会在预检阶段阻止。旧版保存的隐式 CPU 默认会一次性迁移为 `auto`，之后用户显式保存的 CPU 选择会保留。

首次打包前准备并验证运行时：

```powershell
npm run runtime:prepare
npm run runtime:verify
```

下载文件和 wheel 缓存在 `.cache/portable-runtime`。配置和源码未变化时，`runtime:prepare` 会校验现有运行时并增量跳过；需要完全重建时运行：

```powershell
npm run runtime:rebuild
```

成功后生成的目录契约是：

```text
runtime/
  python/python.exe
  models/yolo26n.pt
  runtime-manifest.json
```

`runtime-manifest.json` 会记录 CPython、模型、本地 Ultralytics 源码、app-local VC++ runtime、安装包和 wheel 的 SHA-256。准备脚本只在临时副本中安装 Ultralytics，并把字体检查改为离线本机字体回退；没有中文字体时会回退到 Matplotlib 自带的 DejaVu Sans，训练不会因无法访问 GitHub 的 Arial 字体而中断，但结果图中的中文可能缺字。验证脚本还会检查 `MSVCP140.dll` 的实际加载路径必须位于内置 Python 目录，避免在开发机上误用系统 VC++ runtime 造成假通过。

Ultralytics 的 AMP 自检固定使用随包模型，不会在用户项目中重复下载权重；LabelMe 的在线 SAM2 AI 模型默认关闭，应用内的 YOLO 自动标注仍完全离线可用。打包阶段会把锁定的 `CONCRT140.dll` 同时放入内置 Python 和 LabelMe 目录，避免手工标注工具依赖目标电脑已安装的 VC++ runtime。

准备阶段还会确定性移除 PyTorch 的 C/C++ 链接开发文件（`torch/lib/*.lib`、`torch/include`、`protoc.exe`）和可再生的 `__pycache__`。运行所需 DLL、Python 源码和包元数据均保留；裁剪规则及节省字节数会写入 manifest。

正常发行命令会自动准备、验证并打包运行时：

```powershell
npm run electron:package
# 或生成经过完整解压复验的便携 ZIP
npm run electron:make
```

`electron:package` 和 `electron:make` 都会固定构建 `win32/x64`，并在完成后自动检查成品资源、使用成品目录中的 Python 导入全部运行库并实际加载默认模型。便携目录输出到 `out/YOLO26ModelTraining-win32-x64`，`electron:make` 生成的 ZIP 位于 `out/make`，并会将 ZIP 完整解压后再次运行同一套模型/CUDA 校验。CUDA 运行时会让成品超过旧 Squirrel/NuGet 的可靠体积范围，因此 CUDA 版默认只发布便携 ZIP，不生成 Squirrel 安装器。

Forge 最终将资源放在应用的外部 `resources` 目录，而不是 `app.asar`：

```text
resources/python/python.exe
resources/python/MSVCP140.dll
resources/models/yolo26n.pt
resources/runtime-manifest.json
resources/tools/labelme/labelme.exe
resources/tools/labelme/_internal/CONCRT140.dll
```

因此 `third_party` 的约 9 GB 内容不会进入发行包，LabelMe 也不会再被封进 `app.asar`。当前锁面向标准版 Windows 10/11 x64，GPU 加速仅支持兼容的 NVIDIA CUDA 显卡；AMD/Intel GPU、Windows ARM、Linux 和 macOS 不在此发行包范围。Windows N/KN 版缺少 OpenCV/Electron 所需的 Media Foundation，需要先启用微软 Media Feature Pack；Server Core 不在支持范围。GPU 模式仍需要目标电脑安装兼容的 NVIDIA 驱动（CUDA 12.6 建议 Windows 驱动 560.76 或更新），但不需要单独安装 CUDA Toolkit、Python、Conda 或 VC++ Redistributable。直接启动 Go 后端进行源码开发时，可用环境变量指定其他运行时：

```powershell
$env:MT_PYTHON_CMD = "C:\path\to\python.exe"
$env:YOLO_MODEL_PATH = "C:\path\to\model.pt"
$env:LABELME_PATH = "C:\path\to\labelme.exe"
```

## 项目目录约定

载入项目根目录时，Go 服务会创建并维护：

```text
images/train/
images/val/
labelimgxml/
labelmejson/
labels/train/
labels/val/
runs/
predictImages/
classes.txt
mydata.yaml
```

`classes.txt` 是类别的唯一来源，保存类别后会自动重建 `mydata.yaml`。这修复了旧项目中 `classes.txt`、`mydata.yaml` 和标签类别不一致的问题。

## 已迁移能力

- 创建项目目录结构
- 类别 CRUD 和 `classes.txt` 保存
- `mydata.yaml` 生成
- 图片复制导入到 `images/train`
- Pascal VOC XML / LabelMe JSON 转 YOLO TXT
- YOLO 标签校验，包括类别范围和边界检查
- 训练任务启动、停止、日志 SSE 流、进度解析
- 最新训练结果、模型路径和指标读取
- LabelMe 启动入口，默认使用项目内 `tools\labelme\labelme.exe`
- 开发模式授权状态和兼容旧格式的离线激活校验

## 授权说明

默认没有设置 `MT_LICENSE_SECRET` 时，服务以开发模式运行，不阻止使用。若要启用旧格式离线激活校验，设置：

```powershell
$env:MT_LICENSE_SECRET = "<private-secret>"
```

生产版本建议改成非对称签名 license：客户端只内置公钥，签发端持有私钥，不再把密钥放入本地客户端。

## 样例数据

`data/samples/yolo` 里保留了 3 组最小样例图片和标签，覆盖 class `0`、class `1` 和双框场景。大模型文件、训练输出、授权文件和缓存不进入新项目。

## 安装版、独立运行环境与 GitHub 更新

从 `v0.3.0` 开始，Windows 安装版只包含 Electron、Go 后端和 Web 前端；约 4.6 GiB 的 Python/PyTorch/CUDA/LabelMe 运行环境独立发布，普通软件更新不再重复下载运行环境。

首次启动时 Electron 主进程读取：

```text
https://raw.githubusercontent.com/GXICll-Dev/YOLO26ModelTraining-Runtime/main/runtime/latest.json
```

缺少运行环境时，界面左下角显示红色提示。用户点击后，程序会逐包下载 GitHub Release 附件、断点续传、验证 SHA-256、解压到临时目录、执行 Python/PyTorch/YOLO 冒烟测试，并在全部通过后原子切换到：

```text
%LOCALAPPDATA%\YOLO26ModelTraining\runtime\windows-x64-cuda126-py311
```

主程序、Runtime、下载缓存、软件更新文件、日志和内部状态都位于 `%LOCALAPPDATA%\YOLO26ModelTraining`。Runtime 分包在成功部署后会自动删除；卸载程序会删除整个应用目录和旧版遗留数据，但不会删除用户自行选择的训练项目目录。

生成并验证 Runtime Release：

```powershell
npm run runtime:release
npm run runtime:release:verify
```

生成带卸载入口的 Windows 安装包：

```powershell
npm run electron:installer
```

安装包输出到：

```text
out/installer/win32/x64/YOLO26ModelTraining-Setup-<version>.exe
```

发布前编辑 `RELEASE_NOTES.md`，然后执行：

```powershell
npm run release:app
```

运行环境版本独立发布：

```powershell
npm run release:runtime -- -RuntimeVersion 1.0.0
```

软件每次启动都会后台检查 `GXICll-Dev/YOLO26ModelTraining` 的最新 GitHub Release。发现高于当前版本的标签时，左下角版本号显示红点；用户可以在弹窗中查看 Release 内容、断点下载安装包、校验 SHA-256，然后重启安装。
