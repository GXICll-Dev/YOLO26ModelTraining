# v0.3.4

更新内容弹窗现在支持 **GitHub 风格 Markdown**。

## 支持的格式

- 标题和分级标题
- 有序列表、无序列表及任务列表
- **粗体**、*斜体*、~~删除线~~
- 行内 `code` 和代码块
- 链接、引用、分隔线和图片
- GitHub 风格表格

| 项目 | 处理方式 |
| --- | --- |
| Markdown | 使用 `react-markdown` 渲染 |
| GFM 扩展 | 使用 `remark-gfm` 支持 |
| 原始 HTML | 默认禁用，避免 Release 内容注入界面 |

同时保留 `v0.3.3` 新增的真实 Windows `Uninstall.exe`。
