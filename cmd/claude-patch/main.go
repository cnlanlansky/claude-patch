package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cnlanlansky/claude-patch/internal/app"
	"github.com/cnlanlansky/claude-patch/internal/claude"
	"github.com/cnlanlansky/claude-patch/internal/config"
)

func usage() string {
	return `Claude Patch：Claude Code v2.1.233 本地模型补丁与管理网关

用法：
  claude-patch.exe
  claude-patch.exe --background
  claude-patch.exe claude [Claude 参数...]
  claude-patch.exe --self-check
  claude-patch.exe --help`
}

func run(args []string) (uint32, error) {
	executable, err := app.ExecutablePath()
	if err != nil {
		return 1, err
	}
	background := false
	if len(args) > 0 {
		if args[0] != "--background" {
			if err := app.AttachParentConsole(); err != nil {
				return 1, err
			}
		}
		switch args[0] {
		case "--help", "-h", "help":
			fmt.Println(usage())
			return 0, nil
		case "--background":
			if len(args) != 1 {
				return 1, fmt.Errorf("--background 不接受其他参数")
			}
			background = true
		case "--self-check":
			loaded, err := config.Load(executable)
			if err != nil {
				return 1, err
			}
			discovery, err := claude.Probe(loaded.Config.Claude.Executable)
			if err != nil {
				return 1, err
			}
			bytes, _ := json.MarshalIndent(discovery, "", "  ")
			fmt.Println(string(bytes))
			return 0, nil
		case "claude":
			return runClaude(executable, args[1:])
		default:
			return 1, fmt.Errorf("未知命令：%s", args[0])
		}
	}
	instance, acquired, err := app.AcquireManagementInstance(background)
	if err != nil {
		return 1, err
	}
	if !acquired {
		return 0, nil
	}
	defer instance.Close()
	runtime, err := app.StartRuntime(executable)
	if err != nil {
		return 1, err
	}
	if err := app.RunGUI(runtime, background); err != nil {
		_ = runtime.Stop()
		return 1, err
	}
	return 0, nil
}

func runClaude(executable string, args []string) (uint32, error) {
	runtime, err := app.StartRuntime(executable)
	if err != nil {
		return 1, err
	}
	defer runtime.Stop()
	session, err := runtime.StartClaude(args)
	if err != nil {
		return 1, err
	}
	return runtime.WaitClaude(session)
}

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		app.ShowError(err)
	}
	os.Exit(int(code))
}
