# fx Desktop

[English](README.md) | 简体中文

面向 [fx](https://fx.sh/) 的社区桌面客户端。它使用 [MyGo](https://github.com/egoist/mygo) 构建原生界面，通过 ACP 连接 **fx** agent。对话、工具输出和权限请求均在原生界面中呈现；非交互式 fx 命令通过原生表单运行，仅交互式认证（`fx login`、`setup`、`teams`）使用窗口内控制台。

本项目并非 Vercel Labs 官方发布。

**支持平台：**macOS 13 及以上版本和 Linux，与 fx CLI 一致。

## 功能

- **原生对话**：流式 Markdown 回复、可展开的工具结果、复制、取消和对话中切换模型。
- **ACP 会话与历史**：浏览、搜索 fx 保存的会话，并在项目窗口中恢复；工具和历史记录由 fx 管理。
- **输入区**：使用 `@` 引用工作区文件；连接的 agent 支持图片时可添加图片附件。
- **内联权限请求**：等待用户明确选择；操作提示显示在对话之外。
- **Quick Ask**：通过原生表单使用 `fx ask`，支持模型、推理强度、访问权限、系统提示词、图片、恢复会话、供应商顺序、上下文限制和额外目录。
- **工作区工具**：原生 Account、MCP、Folders、Details、Sessions、Launch、Models、Rules、Skills、Slack 和 GitHub 页面。
- **模型与启动设置**：供应商顺序、推理强度、Fast／Ultra、上下文限制、额外目录和模型选择器。
- **允许规则编辑器**：添加、删除或重置 `~/.fx/settings.json` 中的用户或工作区权限规则。
- **文件标签页**：在右侧栏浏览工作区、展开文件夹、使用默认应用打开文件，或右键在文本编辑器中打开。
- **变更面板**：实时查看 `git status` 和 `git diff`，并快速提交。
- **常用操作**：命令面板、最近项目、打开文件夹、恢复上次项目和在 Finder 中显示。
- **fx 管理**：安装或升级 fx、运行 `fx doctor`、打开 `~/.fx`。
- **偏好设置**：默认项目目录、主题、终端字体、托盘和更新行为。
- **系统集成**：菜单栏托盘、关闭到托盘、单实例和 `fx-desktop://` 深层链接。
- **主题**：跟随系统、浅色和深色；支持实时预览，保存后全局应用。

## 环境要求

- Go 1.27.3 或更高版本。
- MyGo v0.7.1，已在 `go.mod` 中固定；可运行 `go tool mygo doctor` 检查环境。
- Linux：MyGo 运行环境需要 GTK 3 和 WebKitGTK 4.1。
- 可选：预先[安装 fx](https://fx.sh/docs/getting-started/installation)，也可使用应用内安装器。

## 开发

```bash
go tool mygo dev
```

直接打开项目：

```bash
go tool mygo dev /path/to/project
```

使用指定的 fx 可执行文件：

```bash
FX_DESKTOP_FX_PATH=/path/to/fx go tool mygo dev
```

## 构建

```bash
go tool mygo build
```

打包后的应用和安装程序位于 `outputs/`（不纳入 Git）。本地构建时如不需要 DMG：

```bash
go tool mygo build -skip-dmg
```

## 测试

```bash
go test ./...
```

测试通过 `internal/testutil` 隔离 `HOME` 和 `PATH`；安装器测试使用 `httptest`，不会访问 GitHub。

## 项目结构

```text
main.go                 程序入口
internal/app/           应用协调、窗口和会话
internal/app/chrome/    使用 MyGo 构建的原生界面
internal/app/presenter/ 界面展示适配
internal/acp/           ACP JSON-RPC 客户端及进程生命周期
internal/conversation/  对话模型和文本处理
internal/history/       已保存会话的历史记录
internal/management/    fx 管理操作
internal/workspace/     工作区文件、引用和变更
internal/fxcli/         fx 命令执行
internal/fxbin/         fx 可执行文件的安装和升级
internal/fxsessions/    已保存会话的查看和删除
internal/projects/      最近项目及持久化
internal/prefs/         应用偏好设置
internal/configdir/     各平台配置目录
internal/sessionstats/  每个会话的用量记录
resources/icon.png      MyGo 打包使用的应用图标
```

## 深层链接

```text
fx-desktop://open?path=/Users/you/myproject
```

## 许可证

[MIT](LICENSE) © 2026 Lu Weiqing
