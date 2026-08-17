package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathTransformAndCommandOwnership(t *testing.T) {
	directory := filepath.Join(`C:\Users\Boss`, "Claude Patch")
	original := strings.Join([]string{`C:\Windows`, directory, `C:\Tools`, directory + `\`}, ";")
	first := pathFirst(original, directory)
	parts := splitPath(first)
	if len(parts) != 3 || !sameExpandedPath(parts[0], directory) || parts[1] != `C:\Windows` || parts[2] != `C:\Tools` {
		t.Fatalf("PATH 首位和去重错误：%q", first)
	}
	if removed := pathWithout(first, directory); removed != `C:\Windows;C:\Tools` {
		t.Fatalf("PATH 精确卸载错误：%q", removed)
	}

	root := t.TempDir()
	path := filepath.Join(root, "claude.cmd")
	if err := os.WriteFile(path, []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOwnedFile(path, commandContent(), 0o600); err == nil {
		t.Fatal("覆盖非本工具文件未被拒绝")
	}
	if err := os.WriteFile(path, []byte("rem "+ownershipMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOwnedFile(path, commandContent(), 0o600); err != nil {
		t.Fatal(err)
	}
	bytes, _ := os.ReadFile(path)
	if !strings.Contains(string(bytes), `"%~dp0claude-patch.exe" claude %*`) {
		t.Fatalf("cmd 内容错误：%s", bytes)
	}
}

func TestExpandedWindowsPath(t *testing.T) {
	local := t.TempDir()
	t.Setenv("CLAUDE_PATCH_PATH_TEST", local)
	directory := filepath.Join(local, "Claude Patch")
	path := `%CLAUDE_PATCH_PATH_TEST%\Claude Patch;C:\Windows;` + directory
	first := pathFirst(path, directory)
	parts := splitPath(first)
	if len(parts) != 2 || !sameExpandedPath(parts[0], directory) || parts[1] != `C:\Windows` {
		t.Fatalf("Windows 环境变量 PATH 展开错误：%q", first)
	}
}

func TestCommandOwnershipPreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.cmd")
	if err := os.WriteFile(path, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireOwnedOrMissing(path); err == nil {
		t.Fatal("非本工具文件未被拒绝")
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "foreign" {
		t.Fatalf("拒绝后修改了原文件：%v %q", err, bytes)
	}
}

func TestInspectCommandUsesTemporaryInputsOnly(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "claude-patch.exe")
	if err := os.WriteFile(filepath.Join(directory, "claude.cmd"), []byte(commandContent()), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := inspectCommand(executable, directory+`;C:\Windows`)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Healthy || !status.InPath {
		t.Fatalf("命令状态错误：%+v", status)
	}
}
