# 架构

```text
claude-patch.exe
├── 无参数：单实例 Win32 GUI + 托盘 + loopback Web 管理 Router
├── --background：单实例托盘 + loopback Web 管理 Router
├── claude [...]
│   ├── per-session loopback Router
│   └── Claude Code v2.1.233 suspended child
│       └── PE/.bun 校验 → 内存 patch → resume → wait
└── --self-check：suspended/no-resume probe
```

程序由纯 Go 构建，只使用标准库和 `golang.org/x/sys/windows`。不嵌入 Bun、Node、WebView2 或 GUI framework。

## 入口边界

- 双击无参数入口只打开管理程序，不创建 Claude child；重复启动只显示已有窗口；
- `--background` 是当前用户开机启动入口，只创建隐藏到托盘的管理程序；
- GUI 的安装、卸载按钮和两个桌面开关均由用户显式触发，启动时只检测状态；
- `claude [参数...]` 由命令代理调用，创建一个独立 Router 和一个新 Claude child，不受管理入口单实例限制；命令入口启动时加入固定名称的本工具 Windows Job Object，管理入口不加入该 Job；
- `--self-check` 始终在 resume 前终止候选 child。

## 请求边界

```text
Claude 原生模型 ─────────────────▶ Anthropic
claude-router/... 项目模型 ──────▶ 当前 session 的 127.0.0.1 Router ──────▶ Provider
```

Router 用完整模型 ID 查找模型与 Provider。`enabled: false` 或 Provider 未配置时，模型同时从 picker、`/v1/models` 和消息路由移除。除 `opencode-free` 可在 `auth: none` 下免 Key 外，Provider 必须同时具备有效 HTTP(S) URL 和非占位 Key 才算已配置。协议适配支持 Anthropic Messages、OpenAI Chat Completions、OpenAI Responses，并保留 tool call/result、SSE、usage、stop reason、Fast 和 server Web Search 语义。

Provider 非 2xx 状态与正文不自动重试、翻译、换模型或降级。Anthropic 原样响应逐块转发并传播请求取消；需要协议聚合或 server Web Search 的响应才会在 16 MiB 上限内缓冲。`count_tokens` 透明转发给对应 Provider，不伪造 token 数。Hop-by-hop 与编码/长度响应头由本地 HTTP server 重建，其余上游响应头透传。

## 访问与配置

- Router 只监听 `127.0.0.1:0`，并拒绝非 loopback Host；
- 本机管理页无需登录，可直接访问 Router 根地址；`GET /`、`GET /api/state`、`PUT /api/config` 和本 Router 拥有的 session stop 接口属于管理面；
- `GET /v1/models`、`POST /v1/messages/count_tokens` 和 `POST /v1/messages` 属于 Claude 会话面，每个 Claude child 使用独立 32 字节随机 session token；
- session token 比较使用 constant-time compare；
- 请求体上限 16 MiB，JSON 只允许单一对象；
- `config.json` 位于 `claude-patch.exe` 旁边，严格拒绝未知字段；
- 配置保存使用同目录临时文件和 Windows replace-existing rename；
- 管理 API 只暴露 `hasApiKey` 和派生的 `configured`，不回显 Provider key；Provider key 也不进入 Claude child。

## Claude 发现

候选顺序包括：

1. `config.json` 中的路径；
2. Bun 固定目录及 `.bunx` UTF-16 metadata；
3. npm global wrapper package 与 `claude.cmd`；
4. npm optional `@anthropic-ai/claude-code-win32-x64`；
5. 官方 native 目录与 PATH。

最终目标必须是 Windows AMD64 PE32+、包含唯一有效 `.bun` section，并能从相邻 package metadata 读取精确版本 `2.1.233`。本工具自己的 `claude.cmd` ownership marker 会被跳过，避免递归启动。

## Child 安全边界

创建流程：

1. `CreateProcessW(CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT)`；
2. child 继承调用终端标准句柄；
3. 加入带 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 的 Job；
4. `QueryFullProcessImageNameW` 确认实际映像；
5. 解析 PE headers 并找到 mapped image base；
6. 验证六组 patch marker 在磁盘和 mapped `.bun` 中均唯一、offset 一致；
7. `VirtualProtectEx` → `WriteProcessMemory` → 写后回读 → 恢复并复查保护；
8. 全部成功后才 `ResumeThread`。

任何路径、版本、PE、marker、mapped bytes、内存区域、原字节、写入或页保护不匹配，都只终止本次新建 child。不会枚举、附加或操作既有 Claude 进程。

## Session 生命周期

每个命令会话持有：

- 一个 Go Router；
- 一个 Claude child handle；
- 一个 kill-on-close Job；
- 一份内存中的 token 映射。

Runtime 真正退出时，以内存中的 session 所有权快照为准，先幂等停止并关闭本工具创建的全部 Claude child，再停止 Router；管理入口随后终止固定名称 Job Object 中的全部本工具命令进程。命令入口正常退出只关闭自己的 Job 句柄，不广播或终止兄弟会话。Job 成员关系不依赖 EXE 文件名，因此用户改名仍可清理；Router 注册竞态、命令正常结束、GUI 关闭和托盘明确退出都不能遗漏本工具自有 child。托盘图标不可用时 GUI 不隐藏窗口，避免失去退出入口。不会按进程名、PID 猜测或枚举清理外部 Claude/Router。

`%TEMP%\claude-router-sessions` 只保存 session ID、Claude PID、startedAt、Router PID 和 Router ID，不保存 token、API key、prompt 或 tool result。其他 Router 可查看活跃元数据，但不能调用或停止不属于自己的 session。

## 命令代理

GUI 只管理程序旁边带 ownership marker 的 `claude.cmd`，它通过 `%~dp0claude-patch.exe claude %*` 调用同目录主程序。

安装把程序目录放到 HKCU `Environment\Path` 首位并广播 `WM_SETTINGCHANGE`。卸载只删除本工具拥有的 `claude.cmd` 和精确匹配的 PATH 项；检测到同名非本工具文件时失败关闭，不覆盖也不删除。

## 桌面生命周期

- 主窗口使用纯 Win32/GDI 自绘，应用图标以多尺寸 ICO 资源嵌入同一个 EXE；不加载外部图片或 GUI runtime；窗口标题使用 `internal/version.Current`，正式 tag 构建由 linker 注入 `vX.Y.Z`，本地构建默认显示 `dev`；
- 主窗口“检查更新”按钮只在用户点击时启动后台 HTTPS 请求，固定读取 GitHub `releases/latest` API，严格校验 `vX.Y.Z` 并比较版本；发现更新时只打开固定仓库的 Release 页面，不自动下载、替换或重启 EXE；请求失败只更新本地状态，不影响 Router 或 Claude child；
- “登录 Windows 后启动”默认关闭，开启后精确管理 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\ClaudePatch`，命令为当前 EXE 的 `--background`；遇到同名陌生值不覆盖；
- “关闭窗口后隐藏到托盘”默认开启，偏好保存在 `HKCU\Software\ClaudePatch\CloseToTray`；托盘“退出”绕过隐藏行为并调用现有 `Runtime.Stop()`；
- 托盘双击显示窗口，右键菜单提供显示、打开 Web 管理和退出；响应 `TaskbarCreated` 以恢复 Explorer 重启后丢失的图标；
- 管理入口用当前用户命名 mutex 保证单实例，`claude [...]` 命令入口仍允许多会话并发；
- 这不是 Windows 服务；不开启桌面开关时不会常驻或自动启动，所有注册表写入都来自用户点击。
