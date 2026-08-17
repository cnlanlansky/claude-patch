# 应用图标

`main.go` 只使用 Go 标准库，确定性生成 `assets/claude-patch.ico` 的 16、20、24、32、48、64、128、256 px 图层：

```powershell
go run ./tools/icon
```

该命令只更新 ICO，不会重建 `cmd/claude-patch/rsrc_windows_amd64.syso`。仓库提交的 `.syso` 是 Windows AMD64 COFF 资源，Go linker 会自动嵌入最终 EXE；当前仓库没有提供从 ICO 重建 `.syso` 的脚本，因此修改图标时必须同步更新该资源对象，不能只提交 ICO。

资源完整性验证：

```powershell
go test ./cmd/claude-patch -run TestWindowsResourceObjectContainsIconSection
```

最终用户不需要图标工具或额外运行时文件。
