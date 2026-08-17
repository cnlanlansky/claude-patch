//go:build windows

package app

import "testing"

func TestStartupOwnershipAndHealth(t *testing.T) {
	executable := `D:\Tools\Claude Patch\claude-patch.exe`
	value := startupCommand(executable)
	if !startupOwned(value) || !startupHealthy(value, executable) {
		t.Fatalf("当前启动项未识别：%q", value)
	}
	if startupHealthy(startupCommand(`D:\Old\claude-patch.exe`), executable) {
		t.Fatal("旧路径被误判为健康")
	}
	for _, foreign := range []string{
		`"D:\Tools\other.exe" --background`,
		`"D:\Tools\claude-patch.exe" claude`,
		`cmd.exe /c claude-patch.exe --background`,
		`claude-patch.exe --background`,
	} {
		if startupOwned(foreign) || requireOwnedStartup(foreign, true) == nil {
			t.Fatalf("陌生启动项未拒绝：%q", foreign)
		}
	}
	if err := requireOwnedStartup("", false); err != nil {
		t.Fatal(err)
	}
}
