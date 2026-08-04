# v0.3.0

首次提供安装版、独立运行环境部署和 GitHub 更新功能。

- 左下角显示当前软件版本；发现新版本时显示红色更新提示。
- 每次启动后台检查 GitHub Release，点击版本号可查看更新内容、下载并安装。
- 新增标准 Windows 安装程序和卸载入口。
- Python、PyTorch、CUDA、Ultralytics、YOLO26 模型和 LabelMe 改为独立运行环境。
- 首次启动检测运行环境；缺失时可一键下载、断点续传、SHA-256 校验并自动部署。
- 运行环境使用临时目录验证和原子切换，部署失败不会覆盖可用旧环境。
- 保留原有完整便携版构建方式。
- Python stdout、stderr 和完整 Traceback 继续保留在详细日志中。
