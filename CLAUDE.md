# Claude Patch 项目规则

## 产品边界

- 仅支持 Windows x64 与 Claude Code v2.1.233；版本、PE、`.bun` 或 marker 不匹配时必须在 child resume 前失败关闭。
- 运行时保持纯 Go；不得重新引入 Bun、Node、Electron、WebView2 或其他 GUI runtime。
- 无参数启动原生管理 GUI；`--background` 进入托盘；只有 `claude [...]` 创建 suspended Claude child。
- 绿色目录固定为 `claude-patch.exe`、`config.json` 和用户点击安装后生成的 `claude.cmd`。只提供安装、卸载，不恢复修复功能或旧目录兼容。
- 管理页仅绑定 loopback，直接访问且不设登录；Claude `/v1/*` 始终使用每 session 随机 token。
- `opencode-free` 是唯一免 Key Provider。其他 Provider 必须同时具备有效 HTTP(S) URL 和非占位 Key，其模型才可显示和路由。

## 安全红线

- 不附加、暂停、patch、重启或终止工具启动前已经存在的 Claude/Router，尤其不得触碰当前开发会话。
- 自动测试只使用随机 loopback、临时配置、fake Provider 和本工具新建的 suspended/`--version` child；不得发送真实 Provider 请求。
- 不用自动化修改真实用户 PATH、开机启动或桌面偏好；这些写入只由用户在 GUI 中显式触发。
- 不扫描、迁移或清理旧 `%LOCALAPPDATA%\ClaudePatch`。

## 开发与验证

```powershell
# Console 调试版；输出到 dist，因此读取 dist\config.json
.\scripts\debug.ps1
.\scripts\debug.ps1 claude --version

# 正式 Windows GUI 版
.\scripts\build.ps1

# 提交前验证
go test ./... -timeout=90s
go vet ./...
git diff --check
```

- 测试命令最长 90 秒；不运行常驻后台服务。
- `scripts/debug.ps1` 会覆盖 `dist\claude-patch.exe` 为 Console 版，交付前必须再执行 `scripts/build.ps1`。
- 代码风格跟随相邻 Go 文件，优先标准库和现有 helper，不为单一实现新增抽象。

## 文档职责

| 文件 | 内容 |
|---|---|
| `README.md` | 用户安装、构建、使用和排障 |
| `docs/architecture.md` | 当前代码实际架构与安全边界 |
| `docs/multi-model-claude-cli-requirements.md` | 冻结需求与尚需真实环境验收的项目 |
| `tools/icon/README.md` | 图标资源再生成方式 |
