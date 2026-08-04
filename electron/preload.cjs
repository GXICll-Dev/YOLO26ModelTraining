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

function subscribe(channel, callback) {
  const listener = (_event, payload) => callback(payload);
  ipcRenderer.on(channel, listener);
  return () => ipcRenderer.removeListener(channel, listener);
}

contextBridge.exposeInMainWorld("electronDesktop", {
  getAppVersion: () => ipcRenderer.invoke("app:get-version"),
  runtime: {
    getStatus: () => ipcRenderer.invoke("runtime:get-status"),
    refresh: () => ipcRenderer.invoke("runtime:refresh"),
    install: () => ipcRenderer.invoke("runtime:install"),
    cancel: () => ipcRenderer.invoke("runtime:cancel"),
    onStatus: (callback) => subscribe("runtime:status", callback)
  },
  update: {
    getStatus: () => ipcRenderer.invoke("update:get-status"),
    check: () => ipcRenderer.invoke("update:check"),
    download: () => ipcRenderer.invoke("update:download"),
    cancel: () => ipcRenderer.invoke("update:cancel"),
    install: () => ipcRenderer.invoke("update:install"),
    onStatus: (callback) => subscribe("update:status", callback)
  }
});
