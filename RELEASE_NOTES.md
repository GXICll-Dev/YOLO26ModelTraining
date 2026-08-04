# v0.3.5

统一程序目录，并提供完整卸载。

## 目录调整

主程序、Runtime、下载缓存、软件更新文件、日志和内部状态现在统一放在：

```text
%LOCALAPPDATA%\YOLO26ModelTraining
```

- 已安装 Runtime：`runtime\windows-x64-cuda126-py311`
- Runtime 临时下载：`downloads\runtime`
- 软件更新安装包：`updates`
- 日志、状态和 Electron 数据：`user-data`

升级后会自动迁移旧版 `%LOCALAPPDATA%\YOLO26ModelTrainingData` 和旧 Electron 用户数据。

## 自动清理

- Runtime 分包仍支持断点续传。
- Runtime 完成解压、校验和部署后，会立即删除全部已下载分包及下载目录。
- 校验失败的分包同样会删除。

## 完整卸载

使用 `Uninstall.exe` 或 Windows 已安装应用中的卸载入口，会删除整个 `%LOCALAPPDATA%\YOLO26ModelTraining`，包括主程序、Runtime、缓存、更新文件、日志和内部状态；同时清理旧版本遗留目录。

用户自行选择的训练项目目录、训练图片和输出结果不属于程序安装目录，不会被误删。
