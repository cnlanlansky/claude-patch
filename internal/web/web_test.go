package web

import (
	"strings"
	"testing"
)

func TestRenderEscapesPathsAndRemovesLaunchSurface(t *testing.T) {
	output := Render(Paths{Executable: `D:\Claude & Patch\claude-patch.exe`, Command: `D:\Claude & Patch\claude.cmd`, Config: `D:\Claude & Patch\config.json`})
	for _, expected := range []string{`D:\Claude &amp; Patch\claude-patch.exe`, `D:\Claude &amp; Patch\claude.cmd`, `D:\Claude &amp; Patch\config.json`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("管理页缺少转义路径：%s", expected)
		}
	}
	for _, forbidden := range []string{"/api/claude/start", "start-claude"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("管理页仍包含 Web 启动入口：%s", forbidden)
		}
	}
	for _, expected := range []string{"data-provider-delete", "请先删除或改绑该 Provider 下的模型", "provider.configured"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("管理页缺少 Provider 删除闭环：%s", expected)
		}
	}
}
