# YOLO26ModelTraining AI 项目入口

处理本仓库的构建、版本、安装包、GitHub Release 或 Runtime 任务前，必须先读取以下文件：

@README.md
@docs/RELEASE_AND_RUNTIME_GUIDE.md
@RELEASE_NOTES.md
@package.json

发布相关脚本是实际行为的最终依据：

@scripts/publish-app-release.ps1
@scripts/publish-runtime-release.ps1
@scripts/build-runtime-release.ps1

关键约束：

- 应用仓库和 Runtime 仓库是两个独立 Git 仓库。
- 普通 UI、Electron 或 Go 更新只发布应用 Release，不重新上传 Runtime。
- 公开后的 Runtime 分包和清单哈希不可原地覆盖；发生变化必须提升 Runtime 版本。
- 执行任何发布前先检查两个仓库的工作区和远程状态，不能覆盖用户未提交的修改。
- 版本、文件名、远程 Release 和 SHA-256 必须在发布后在线复核。
