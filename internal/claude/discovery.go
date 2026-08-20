package claude

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

type packageMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string
}

type Discovery struct {
	RequestedPath  string `json:"requestedPath"`
	ExecutablePath string `json:"executablePath"`
	WrapperPath    string `json:"wrapperPath,omitempty"`
	PackageName    string `json:"packageName,omitempty"`
	PackageVersion string `json:"packageVersion,omitempty"`
	ProfileVersion string `json:"profileVersion,omitempty"`
	Source         string `json:"source"`
}

func Discover(configured string) (Discovery, error) {
	discovery, _, err := discoverProfile(configured)
	return discovery, err
}

func discoverProfile(configured string) (Discovery, compatibilityProfile, error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return Discovery{}, compatibilityProfile{}, errors.New("仅支持 Windows x64")
	}
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	localAppData := os.Getenv("LOCALAPPDATA")
	candidates := []struct {
		path, source string
	}{
		{configured, "config"},
		{filepath.Join(home, ".bun", "bin", "claude.exe"), "fixed-path"},
		{filepath.Join(appData, "npm", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"), "fixed-path"},
		{filepath.Join(appData, "npm", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe"), "fixed-path"},
		{filepath.Join(appData, "npm", "node_modules", "@anthropic-ai", "claude-code", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe"), "fixed-path"},
		{filepath.Join(appData, "npm", "claude.exe"), "fixed-path"},
		{filepath.Join(appData, "npm", "claude.cmd"), "fixed-path"},
		{filepath.Join(localAppData, "Programs", "Claude", "claude.exe"), "fixed-path"},
		{filepath.Join(home, ".local", "bin", "claude.exe"), "fixed-path"},
	}
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		for _, name := range []string{"claude.exe", "claude.cmd", "claude"} {
			candidates = append(candidates, struct{ path, source string }{filepath.Join(strings.Trim(directory, `"`), name), "path"})
		}
	}
	seen := make(map[string]struct{})
	var diagnostics []string
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.path) == "" {
			continue
		}
		absolute, err := filepath.Abs(candidate.path)
		if err != nil {
			continue
		}
		key := strings.ToLower(filepath.Clean(absolute))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result, profile, err := resolveProfile(absolute)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				diagnostics = append(diagnostics, fmt.Sprintf("%s: %v", absolute, err))
			}
			continue
		}
		result.Source = candidate.source
		return result, profile, nil
	}
	if len(diagnostics) > 0 {
		return Discovery{}, compatibilityProfile{}, fmt.Errorf("未找到可 patch 的 Claude Code %s：%s", supportedVersions(), strings.Join(diagnostics, "; "))
	}
	return Discovery{}, compatibilityProfile{}, fmt.Errorf("未找到 Claude Code %s", supportedVersions())
}

func Resolve(requested string) (Discovery, error) {
	discovery, _, err := resolveProfile(requested)
	return discovery, err
}

func resolveProfile(requested string) (Discovery, compatibilityProfile, error) {
	requested = filepath.Clean(requested)
	info, err := os.Stat(requested)
	if err != nil {
		return Discovery{}, compatibilityProfile{}, err
	}
	if info.IsDir() || info.Size() == 0 {
		return Discovery{}, compatibilityProfile{}, fmt.Errorf("不是有效文件")
	}
	if isOwnedShim(requested) {
		return Discovery{}, compatibilityProfile{}, errors.New("跳过 Claude Patch 命令代理")
	}

	result := Discovery{RequestedPath: requested, ExecutablePath: requested}
	if strings.EqualFold(filepath.Ext(requested), ".exe") {
		bunx := strings.TrimSuffix(requested, filepath.Ext(requested)) + ".bunx"
		if target, err := ParseBunx(bunx); err == nil {
			result.ExecutablePath = target
			result.WrapperPath = requested
		}
	}
	if strings.EqualFold(filepath.Ext(requested), ".cmd") || strings.EqualFold(filepath.Ext(requested), ".bat") {
		target, err := resolveNPMShim(requested)
		if err != nil {
			return Discovery{}, compatibilityProfile{}, err
		}
		result.ExecutablePath = target
		result.WrapperPath = requested
	}

	if strings.EqualFold(filepath.Ext(result.ExecutablePath), ".cmd") || strings.EqualFold(filepath.Ext(result.ExecutablePath), ".bat") {
		return Discovery{}, compatibilityProfile{}, errors.New("命令脚本不能直接 patch")
	}
	if err := validateNativePE(result.ExecutablePath); err != nil {
		return Discovery{}, compatibilityProfile{}, err
	}
	metadata := packageMetadataBeside(result.ExecutablePath)
	profile, err := selectProfile(metadata.Name, metadata.Version)
	if err != nil {
		return Discovery{}, compatibilityProfile{}, err
	}
	result.PackageName = metadata.Name
	result.PackageVersion = metadata.Version
	result.ProfileVersion = profile.version
	return result, profile, nil
}

func ParseBunx(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(bytes) <= 4 || len(bytes)%2 != 0 {
		return "", errors.New("Bun wrapper metadata 格式无效")
	}
	payload := bytes[:len(bytes)-4]
	units := make([]uint16, len(payload)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(payload[index*2:])
	}
	relative := strings.TrimRight(string(utf16.Decode(units)), "\x00\t\r\n\" ")
	if relative == "" || filepath.IsAbs(relative) {
		return "", errors.New("Bun wrapper metadata 目标不安全")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("Bun wrapper metadata 目标越界")
	}
	target := filepath.Clean(filepath.Join(filepath.Dir(filepath.Dir(path)), clean))
	if _, err := os.Stat(target); err != nil {
		return "", err
	}
	return target, nil
}

func resolveNPMShim(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(string(content))
	if !strings.Contains(lower, "@anthropic-ai") || !strings.Contains(lower, "claude-code") {
		return "", errors.New("命令脚本不是 Claude Code npm shim")
	}
	directory := filepath.Dir(path)
	candidates := []string{
		filepath.Join(directory, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"),
		filepath.Join(directory, "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe"),
		filepath.Join(directory, "node_modules", "@anthropic-ai", "claude-code", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe"),
	}
	for _, candidate := range candidates {
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Size() > 0 {
			return candidate, nil
		}
	}
	return "", errors.New("npm shim 未找到 Claude Code native binary")
}

func packageMetadataBeside(executable string) packageMetadata {
	directory := filepath.Dir(executable)
	paths := []string{
		filepath.Join(directory, "package.json"),
		filepath.Join(filepath.Dir(directory), "package.json"),
		filepath.Join(directory, "..", "package.json"),
	}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		bytes, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var metadata packageMetadata
		if json.Unmarshal(bytes, &metadata) == nil && metadata.Name != "" && metadata.Version != "" {
			metadata.Path = path
			return metadata
		}
	}
	return packageMetadata{}
}

func packageVersionBeside(executable string) string {
	return packageMetadataBeside(executable).Version
}

func validateNativePE(path string) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("不是 Windows PE：%w", err)
	}
	defer file.Close()
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("只支持 AMD64 PE，machine=0x%x", file.Machine)
	}
	section := file.Section(".bun")
	if section == nil || section.VirtualSize == 0 || section.Size < section.VirtualSize {
		return errors.New("Claude PE 缺少有效 .bun section")
	}
	return nil
}

func isOwnedShim(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".cmd" && extension != ".bat" && extension != "" {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	content, _ := io.ReadAll(io.LimitReader(file, 4096))
	lower := bytes.ToLower(content)
	return bytes.Contains(lower, []byte("claude-patch.exe")) && bytes.Contains(lower, []byte(" claude"))
}
