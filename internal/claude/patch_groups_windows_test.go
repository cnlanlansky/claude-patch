//go:build windows

package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentClaude237PatchGroupsSmoke(t *testing.T) {
	testCurrentClaudePatchGroupsSmoke(t, "2.1.237")
}

func TestCurrentClaude239PatchGroupsSmoke(t *testing.T) {
	testCurrentClaudePatchGroupsSmoke(t, "2.1.239")
}

func testCurrentClaudePatchGroupsSmoke(t *testing.T, version string) {
	if os.Getenv("CLAUDE_PATCH_LIVE_ACCEPTANCE") != "1" {
		t.Skip("设置 CLAUDE_PATCH_LIVE_ACCEPTANCE=1 才运行隔离 patch group smoke")
	}
	root := os.Getenv("CLAUDE_PATCH_LIVE_ACCEPTANCE_ROOT")
	if root == "" {
		t.Fatal("缺少 CLAUDE_PATCH_LIVE_ACCEPTANCE_ROOT")
	}
	binary := filepath.Join(root, "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe")
	profile, err := selectProfile("@anthropic-ai/claude-code-win32-x64", version)
	if err != nil {
		t.Fatal(err)
	}
	disk, image, err := ReadImage(binary)
	if err != nil {
		t.Fatal(err)
	}
	groups := []struct {
		name  string
		specs []patchSpec
	}{
		{name: "picker-context-fast", specs: profile.patchSpecs[:5]},
		{name: "project-client", specs: profile.patchSpecs[5:6]},
		{name: "subagent", specs: profile.patchSpecs[6:11]},
		{name: "team", specs: profile.patchSpecs[11:]},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			process, err := CreateSuspended(binary, []string{"--version"}, root, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			defer process.Terminate(1)
			groupProfile := profile
			groupProfile.patchSpecs = group.specs
			if err := Patch(groupProfile, process, binary, disk, image); err != nil {
				t.Fatal(err)
			}
			if err := process.Resume(); err != nil {
				t.Fatal(err)
			}
			if _, exited, err := process.Wait(30_000); err != nil || !exited {
				t.Fatalf("未在时限内退出：exited=%v err=%v", exited, err)
			}
		})
	}
}
