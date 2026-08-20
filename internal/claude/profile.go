package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type compatibilityProfile struct {
	version          string
	packageNames     []string
	executableSHA256 string
	patchSpecs       []patchSpec
}

func newCompatibilityProfile(version string, packageNames []string, executableSHA256 string, specs []patchSpec) compatibilityProfile {
	return compatibilityProfile{
		version:          version,
		packageNames:     append([]string(nil), packageNames...),
		executableSHA256: strings.ToLower(executableSHA256),
		patchSpecs:       append([]patchSpec(nil), specs...),
	}
}

func (profile compatibilityProfile) matchesPackage(name, version string) bool {
	if profile.version != version {
		return false
	}
	for _, candidate := range profile.packageNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func (profile compatibilityProfile) validateDisk(disk []byte) error {
	if profile.version == "" || len(profile.packageNames) == 0 || len(profile.patchSpecs) == 0 {
		return fmt.Errorf("兼容 profile 无效")
	}
	if profile.executableSHA256 == "" {
		return fmt.Errorf("Claude Code %s 缺少 EXE 指纹", profile.version)
	}
	if len(profile.executableSHA256) != sha256.Size*2 {
		return fmt.Errorf("Claude Code %s EXE 指纹长度无效", profile.version)
	}
	if _, err := hex.DecodeString(profile.executableSHA256); err != nil {
		return fmt.Errorf("Claude Code %s EXE 指纹格式无效", profile.version)
	}
	actual := sha256.Sum256(disk)
	if hex.EncodeToString(actual[:]) != profile.executableSHA256 {
		return fmt.Errorf("Claude Code %s EXE 指纹不匹配", profile.version)
	}
	return nil
}

func supportedVersions() string {
	versions := make([]string, 0, len(compatibilityProfiles))
	for _, profile := range compatibilityProfiles {
		versions = append(versions, profile.version)
	}
	sort.Strings(versions)
	return strings.Join(versions, "、")
}

func selectProfile(name, version string) (compatibilityProfile, error) {
	return selectProfileFrom(compatibilityProfiles, name, version)
}

func selectProfileFrom(profiles []compatibilityProfile, name, version string) (compatibilityProfile, error) {
	for _, profile := range profiles {
		if profile.matchesPackage(name, version) {
			return profile, nil
		}
	}
	return compatibilityProfile{}, fmt.Errorf("仅支持 Claude Code %s，检测到包 %q 版本 %q", supportedVersions(), name, version)
}
