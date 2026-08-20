//go:build windows

package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

func TestCurrentClaude237CommandPathSmoke(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_LIVE_ACCEPTANCE") != "1" {
		t.Skip("设置 CLAUDE_PATCH_LIVE_ACCEPTANCE=1 才运行隔离 2.1.237 命令路径 smoke；这不是原生交互验收")
	}
	root := os.Getenv("CLAUDE_PATCH_LIVE_ACCEPTANCE_ROOT")
	if root == "" {
		t.Fatal("缺少 CLAUDE_PATCH_LIVE_ACCEPTANCE_ROOT")
	}
	binary := filepath.Join(root, "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe")
	profile, err := selectProfile("@anthropic-ai/claude-code-win32-x64", "2.1.237")
	if err != nil {
		t.Fatal(err)
	}
	disk, image, err := ReadImage(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePatchBytes(profile, disk, image); err != nil {
		t.Fatal(err)
	}
	context := 200000
	fast := true
	rows, err := json.Marshal([]config.PickerRow{
		{Value: "claude-router/fake/fake-model", Label: "Fake", Description: "验收假模型", Context: &context},
		{Value: "claude-router/fake/fast-model", Label: "Fake Fast", Description: "验收假模型", Context: &context, Fast: &fast},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct {
		name string
		args []string
	}{
		{name: "model", args: []string{"--model", "claude-router/fake/fake-model", "--bare", "--print", "/model"}},
		{name: "fast", args: []string{"--model", "claude-router/fake/fast-model", "--bare", "--print", "/fast"}},
		{name: "effort", args: []string{"--model", "claude-router/fake/fake-model", "--bare", "--print", "/effort high"}},
		{name: "context", args: []string{"--model", "claude-router/fake/fake-model", "--bare", "--print", "/context"}},
	} {
		t.Run(command.name, func(t *testing.T) {
			process, err := CreateSuspended(binary, command.args, root, map[string]string{ModelsEnv: string(rows), OriginEnv: "http://127.0.0.1:1", TokenEnv: "acceptance-token"})
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			defer process.Terminate(1)
			if err := Patch(profile, process, binary, disk, image); err != nil {
				t.Fatal(err)
			}
			if err := process.Resume(); err != nil {
				t.Fatal(err)
			}
			if _, exited, err := process.Wait(uint32((30 * time.Second).Milliseconds())); err != nil || !exited {
				t.Fatalf("%s 未在时限内退出：exited=%v err=%v", command.name, exited, err)
			}
		})
	}
}
