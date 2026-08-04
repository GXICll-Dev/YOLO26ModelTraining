const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("electronWindow", {
  minimize: () => ipcRenderer.invoke("window:minimize"),
  close: () => ipcRenderer.invoke("window:close"),
  chooseDirectory: () => ipcRenderer.invoke("dialog:choose-directory"),
  chooseModelFile: () => ipcRenderer.invoke("dialog:choose-model-file"),
  chooseImageFiles: () => ipcRenderer.invoke("dialog:choose-image-files"),
  onToggleLayoutInspector: (callback) => {
    const listener = () => callback();
    ipcRenderer.on("layout-inspector:toggle", listener);
    return () => ipcRenderer.removeListener("layout-inspector:toggle", listener);
  }
});
