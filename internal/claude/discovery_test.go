package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestProfileRegistry(t *testing.T) {
	profiles := map[string]compatibilityProfile{}
	for _, profile := range compatibilityProfiles {
		if _, exists := profiles[profile.version]; exists {
			t.Fatalf("重复 profile：%s", profile.version)
		}
		profiles[profile.version] = profile
		if len(profile.patchSpecs) != 13 {
			t.Fatalf("%s patch 点数量错误：%d", profile.version, len(profile.patchSpecs))
		}
		for _, spec := range profile.patchSpecs {
			if len(spec.original) == 0 || len(spec.replacement) > len(spec.original) {
				t.Fatalf("%s/%s 的资产无效：%d/%d", profile.version, spec.label, len(spec.replacement), len(spec.original))
			}
		}
	}
	if len(profiles) != 2 {
		t.Fatalf("兼容 profile 数量错误：%d", len(profiles))
	}
	for _, expected := range []string{"2.1.233", "2.1.237"} {
		if _, exists := profiles[expected]; !exists {
			t.Fatalf("缺少 %s profile", expected)
		}
	}
	if profiles["2.1.233"].executableSHA256 == profiles["2.1.237"].executableSHA256 {
		t.Fatal("不同版本不得共用 EXE 指纹")
	}
	if bytes.Equal(profiles["2.1.233"].patchSpecs[0].original, profiles["2.1.237"].patchSpecs[0].original) {
		t.Fatal("不同版本不得共用 picker 原体")
	}
}

func TestProfileSelectionRequiresExactPackageIdentity(t *testing.T) {
	profiles := compatibilityProfiles
	for _, candidate := range []struct {
		name, version, profile string
	}{
		{"@anthropic-ai/claude-code", "2.1.233", "2.1.233"},
		{"@anthropic-ai/claude-code", "2.1.237", "2.1.237"},
		{"@anthropic-ai/claude-code-win32-x64", "2.1.237", "2.1.237"},
	} {
		profile, err := selectProfileFrom(profiles, candidate.name, candidate.version)
		if err != nil {
			t.Fatalf("%s@%s 不应被拒绝：%v", candidate.name, candidate.version, err)
		}
		if profile.version != candidate.profile {
			t.Fatalf("%s@%s 选择了 %s", candidate.name, candidate.version, profile.version)
		}
	}
	for _, candidate := range []struct{ name, version string }{
		{"@anthropic-ai/claude-code-win32-x64", "2.1.233"},
		{"@anthropic-ai/other", "2.1.237"},
		{"@anthropic-ai/claude-code", "2.1.236"},
		{"@anthropic-ai/claude-code", "2.1.237-dev"},
		{"", "2.1.237"},
	} {
		if _, err := selectProfileFrom(profiles, candidate.name, candidate.version); err == nil {
			t.Fatalf("%s@%s 未被拒绝", candidate.name, candidate.version)
		}
	}
}

func TestProfileFingerprintRejectsWrongDisk(t *testing.T) {
	disk := []byte("official binary")
	profile := profileForTest([]patchSpec{{label: "marker", original: []byte("official"), replacement: []byte("patched ")}}, disk)
	if err := profile.validateDisk(disk); err != nil {
		t.Fatalf("正确 EXE 指纹被拒绝：%v", err)
	}
	if err := profile.validateDisk([]byte("different binary")); err == nil {
		t.Fatal("错误 EXE 指纹未被拒绝")
	}
	invalid := profile
	invalid.executableSHA256 = "not-a-sha256"
	if err := invalid.validateDisk(disk); err == nil {
		t.Fatal("格式错误的 EXE 指纹未被拒绝")
	}
}
func TestParseBunx(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, ".bun", "bin")
	target := filepath.Join(root, ".bun", "install", "global", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	relative := `install\global\node_modules\@anthropic-ai\claude-code\bin\claude.exe`
	units := utf16.Encode([]rune(relative))
	bytes := make([]byte, len(units)*2+4)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(bytes[index*2:], unit)
	}
	path := filepath.Join(bin, "claude.bunx")
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := ParseBunx(path)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(actual, target) {
		t.Fatalf("Bun target 错误：%s != %s", actual, target)
	}
}

func TestResolveNPMShim(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("native"), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(root, "claude.cmd")
	content := `@"%~dp0\node_modules\@anthropic-ai\claude-code\bin\claude.exe" %*`
	if err := os.WriteFile(shim, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveNPMShim(shim)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(resolved, target) {
		t.Fatalf("npm shim target 错误：%s", resolved)
	}
}

func TestPackageMetadataBesidePrefersNativePackage(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "node_modules", "@anthropic-ai", "claude-code")
	native := filepath.Join(outer, "node_modules", "@anthropic-ai", "claude-code-win32-x64")
	executable := filepath.Join(native, "claude.exe")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("native"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "package.json"), []byte(`{"name":"@anthropic-ai/claude-code","version":"2.1.237"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "package.json"), []byte(`{"name":"@anthropic-ai/claude-code-win32-x64","version":"2.1.237"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := packageMetadataBeside(executable)
	if metadata.Name != "@anthropic-ai/claude-code-win32-x64" || metadata.Version != "2.1.237" {
		t.Fatalf("native package metadata 错误：%+v", metadata)
	}
}
func TestResolveNPMShimNestedOptionalPackage(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "node_modules", "@anthropic-ai", "claude-code", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("native"), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(root, "claude.cmd")
	if err := os.WriteFile(shim, []byte(`@node "%~dp0\node_modules\@anthropic-ai\claude-code\cli-wrapper.cjs" %*`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveNPMShim(shim)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(resolved, target) {
		t.Fatalf("嵌套 npm optional package target 错误：%s", resolved)
	}
}

func TestProfileAssetsDoNotCrossMatch(t *testing.T) {
	for _, target := range compatibilityProfiles {
		for _, other := range compatibilityProfiles {
			if target.version == other.version {
				continue
			}
			for _, targetSpec := range target.patchSpecs {
				for _, otherSpec := range other.patchSpecs {
					if bytes.Equal(targetSpec.original, otherSpec.original) {
						t.Fatalf("%s/%s 与 %s/%s 共用原体", target.version, targetSpec.label, other.version, otherSpec.label)
					}
				}
			}
		}
	}
}
func TestParseImageAndPatchByteBudget(t *testing.T) {
	data := make([]byte, 0x600)
	binary.LittleEndian.PutUint16(data[0:], 0x5a4d)
	binary.LittleEndian.PutUint32(data[0x3c:], 0x80)
	binary.LittleEndian.PutUint32(data[0x80:], 0x00004550)
	binary.LittleEndian.PutUint16(data[0x84:], 0x8664)
	binary.LittleEndian.PutUint16(data[0x86:], 1)
	binary.LittleEndian.PutUint16(data[0x94:], 0xf0)
	optional := 0x98
	binary.LittleEndian.PutUint16(data[optional:], 0x20b)
	binary.LittleEndian.PutUint64(data[optional+24:], 0x140000000)
	binary.LittleEndian.PutUint32(data[optional+32:], 0x1000)
	binary.LittleEndian.PutUint32(data[optional+36:], 0x200)
	binary.LittleEndian.PutUint32(data[optional+56:], 0x2000)
	binary.LittleEndian.PutUint32(data[optional+60:], 0x200)
	section := optional + 0xf0
	copy(data[section:], ".bun")
	binary.LittleEndian.PutUint32(data[section+8:], 0x300)
	binary.LittleEndian.PutUint32(data[section+12:], 0x1000)
	binary.LittleEndian.PutUint32(data[section+16:], 0x400)
	binary.LittleEndian.PutUint32(data[section+20:], 0x200)
	image, err := ParseImage(data)
	if err != nil || image.Bun.RawOffset != 0x200 || image.Bun.VirtualSize != 0x300 {
		t.Fatalf("PE 解析错误：%+v %v", image, err)
	}
	for _, profile := range compatibilityProfiles {
		for _, spec := range profile.patchSpecs {
			if len(spec.replacement) > len(spec.original) {
				t.Fatalf("%s/%s 超出 patch 字节预算：%d/%d", profile.version, spec.label, len(spec.replacement), len(spec.original))
			}
		}
	}
	fast := patchSpecs233[2]
	if bytes.HasSuffix(fast.original, []byte("}")) || bytes.HasSuffix(fast.replacement, []byte("}")) {
		t.Fatal("fast predicate marker 的闭合花括号位于 marker 外，不得在 original 或 replacement 中重复")
	}
	broken := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(broken[section+20:], uint32(len(broken)))
	if _, err := ParseImage(broken); err == nil {
		t.Fatal("越界 .bun section 未被拒绝")
	}
}

func TestEnvironmentBlockValidation(t *testing.T) {
	block, err := environmentBlock(map[string]string{"CLAUDE_PATCH_TEST": "值"})
	if err != nil || len(block) < 2 || block[len(block)-1] != 0 || block[len(block)-2] != 0 {
		t.Fatalf("环境块错误：%v %v", err, block)
	}
	if _, err := environmentBlock(map[string]string{"BAD=NAME": "value"}); err == nil {
		t.Fatal("非法环境变量名未被拒绝")
	}
}

func TestCurrentClaudeMarkers(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_LIVE_PROBE") == "" {
		t.Skip("设置 CLAUDE_PATCH_LIVE_PROBE=1 才检查本机 Claude")
	}
	discovery, disk, image, err := ResolveAndRead("")
	if err != nil {
		t.Fatal(err)
	}
	if discovery.PackageVersion != discovery.ProfileVersion {
		t.Fatalf("版本与 profile 不一致：%s/%s", discovery.PackageVersion, discovery.ProfileVersion)
	}
	if image.Bun.VirtualSize == 0 || len(disk) < 100_000_000 {
		t.Fatal("未解析到真实 Claude native binary")
	}
}

func testPatchImage() Image {
	return Image{
		SizeOfImage:   0x1000,
		SizeOfHeaders: 0x200,
		Bun:           Section{VirtualAddress: 0x400, VirtualSize: 0x400, RawOffset: 0x200, RawSize: 0x400},
	}
}

func testPatchDisk(markers ...[]byte) []byte {
	disk := make([]byte, 0x600)
	offset := 0x200
	for _, marker := range markers {
		copy(disk[offset:], marker)
		offset += len(marker) + 1
	}
	return disk
}

func TestPatchPlanRejectsWrongFingerprint(t *testing.T) {
	image := testPatchImage()
	marker := []byte("fingerprint-marker")
	disk := testPatchDisk(marker)
	profile := profileForTest([]patchSpec{{label: "fingerprint", original: marker, replacement: marker}}, disk)
	profile.executableSHA256 = strings.Repeat("0", sha256.Size*2)
	if _, err := buildPatchPlan(profile, disk, image); err == nil {
		t.Fatal("错误 EXE 指纹未被拒绝")
	}
}
func TestPatchPlanRejectsMissingMarker(t *testing.T) {
	image := testPatchImage()
	disk := testPatchDisk()
	profile := profileForTest([]patchSpec{{label: "missing", original: []byte("missing-marker"), replacement: []byte("x")}}, disk)
	if _, err := buildPatchPlan(profile, disk, image); err == nil {
		t.Fatal("缺失 marker 未被拒绝")
	}
}

func TestPatchPlanRejectsDuplicateMarker(t *testing.T) {
	image := testPatchImage()
	marker := []byte("duplicate-marker")
	disk := testPatchDisk(marker, marker)
	profile := profileForTest([]patchSpec{{label: "duplicate", original: marker, replacement: marker}}, disk)
	if _, err := buildPatchPlan(profile, disk, image); err == nil {
		t.Fatal("重复 marker 未被拒绝")
	}
}

func TestPatchPlanRejectsMarkerOutsideBun(t *testing.T) {
	image := testPatchImage()
	specs := []patchSpec{{label: "outside", original: []byte("outside-marker"), replacement: []byte("patched")}}
	disk := make([]byte, 0x700)
	copy(disk[0x100:], specs[0].original)
	profile := profileForTest(specs, disk)
	if _, err := buildPatchPlan(profile, disk, image); err == nil {
		t.Fatal(".bun 外 marker 未被拒绝")
	}
}

func TestPatchPlanRejectsOverlap(t *testing.T) {
	image := testPatchImage()
	first := []byte("first-marker")
	second := []byte("second-marker")
	disk := make([]byte, 0x600)
	copy(disk[0x200:], first)
	copy(disk[0x200+len(first)-2:], second)
	profile := profileForTest([]patchSpec{{label: "first", original: first, replacement: first}, {label: "second", original: second, replacement: second}}, disk)
	if _, err := buildPatchPlan(profile, disk, image); err == nil {
		t.Fatal("重叠 marker 未被拒绝")
	}
}

func TestBindPatchPlanRejectsMappedMismatch(t *testing.T) {
	image := testPatchImage()
	entries := []patchPlanEntry{{label: "marker", original: []byte("marker"), diskOffset: 0}}
	if _, err := bindPatchPlan(image, 0x140000000, []byte("changed"), entries); err == nil {
		t.Fatal("mapped marker 不一致未被拒绝")
	}
}

func TestPatchPlanPadsReplacementToOriginalLength(t *testing.T) {
	spec := patchSpec{original: []byte("original"), replacement: []byte("new")}
	replacement := paddedReplacement(spec)
	if len(replacement) != len(spec.original) || !bytes.Equal(replacement[:3], []byte("new")) || replacement[3] != 0x20 {
		t.Fatalf("replacement padding 错误：%v", replacement)
	}
}

func Test237AssetSemantics(t *testing.T) {
	assertExactPatchReplacement(t, "2.1.237/subagent model", zde237Inherit, `function Nfe(e,t){return t}`)
	assertExactPatchReplacement(t, "2.1.237/subagent model telemetry", ief237Inherit, `function yjp(e,t,r,n,o,i){return Nfe(e,t,r,n,o)}`)
	assertExactPatchReplacement(t, "2.1.237/subagent request model", sqModel237Inherit, `options:{async getToolPermissionContext(){return mn(Q)},model:Jt,`)
	assertExactPatchReplacement(t, "2.1.237/subagent effort", sqEffort237Inherit, `dt=[{kind:"model",mainLoopModel:ie},{kind:"effort",effort:Hv(Q)}],`)
	assertExactPatchReplacement(t, "2.1.237/subagent request effort", sqEffortRequest237Inherit, `effortValue:Hv(Q),`)
	assertExactPatchReplacement(t, "2.1.237/team model", teamModel237Inherit, `function $Rf(e,t){if(typeof t!=="string"||t==="")throw Error("parent model unavailable");return t}`)
	assertExactPatchReplacement(t, "2.1.237/team effort", teamEffort237Inherit, "if(!i)throw Error(\"effort\");t.push(`--effort ${i}`);")
	if !bytes.HasSuffix(fyn237Original, []byte("r}")) || bytes.HasSuffix(fyn237Original, []byte("async ")) {
		t.Fatalf("picker 原体边界错误：%q", fyn237Original)
	}
	if !bytes.Equal(fyn237Reader, []byte(`function nCn(){try{let e=JSON.parse(process.env.CLAUDE_ROUTER_MODELS||"[]");return Array.isArray(e)?e:[]}catch{return[]}}`)) {
		t.Fatalf("picker replacement 错误：%q", fyn237Reader)
	}
	if !bytes.Equal(teamEffort237Original, []byte("if(typeof i===\"string\"&&z8o())t.push(`--effort ${i}`);")) {
		t.Fatalf("Team effort 原体必须只覆盖函数内语句：%q", teamEffort237Original)
	}
	if !bytes.Contains(client237Reader, []byte(`p["x-anthropic-additional-protection"]="true";if(r.startsWith("claude-router/"))`)) {
		t.Fatal("project client 未保留 additional protection")
	}
	for _, value := range [][]byte{
		[]byte("let E=process.env,"),
		[]byte("E.CLAUDE_ROUTER_TOKEN"),
		[]byte("E.CLAUDE_ROUTER_ORIGIN"),
		[]byte("E.ANTHROPIC_CUSTOM_HEADERS=\"\""),
		[]byte("timeout:3600000"),
	} {
		if !bytes.Contains(client237Reader, value) {
			t.Fatalf("project client 缺少 %q", value)
		}
	}
}

func assertPatchReplacementPrefix(t *testing.T, label string, actual []byte, expected string) {
	t.Helper()
	if !bytes.HasPrefix(actual, []byte(expected)) || !bytes.Equal(bytes.TrimRight(actual[len(expected):], " "), nil) {
		t.Fatalf("%s replacement 错误：%q", label, actual)
	}
}

func assertExactPatchReplacement(t *testing.T, label string, actual []byte, expected string) {
	t.Helper()
	if !bytes.Equal(actual, []byte(expected)) {
		t.Fatalf("%s replacement 错误：%q", label, actual)
	}
}

func TestSubagentInheritancePatchSemantics(t *testing.T) {
	for _, test := range []struct {
		label, old, current    string
		oldBytes, currentBytes []byte
		padded                 bool
	}{
		{"普通 Agent model resolver", `function Zde(e,t){return t}`, `function Nfe(e,t){return t}`, zdeInherit, zde237Inherit, true},
		{"普通 Agent telemetry resolver", `function iEf(e,t,r,n,o,i){return Zde(e,t,r,n,o)}`, `function yjp(e,t,r,n,o,i){return Nfe(e,t,r,n,o)}`, iefInherit, ief237Inherit, true},
		{"普通 Agent request model", `options:{async getToolPermissionContext(){return yn(re)},model:M,`, `options:{async getToolPermissionContext(){return mn(Q)},model:Jt,`, sqModelInherit, sqModel237Inherit, true},
		{"普通 Agent effort", `Et=[{kind:"model",mainLoopModel:ne},{kind:"effort",effort:Wb(r)}],`, `dt=[{kind:"model",mainLoopModel:ie},{kind:"effort",effort:Hv(Q)}],`, sqEffortInherit, sqEffort237Inherit, true},
		{"普通 Agent request effort", `effortValue:Wb(re),`, `effortValue:Hv(Q),`, sqEffortRequestInherit, sqEffortRequest237Inherit, false},
		{"Team model resolver", `function B4f(e,t){if(typeof t!=="string"||t==="")throw Error("parent model unavailable");return t}`, `function $Rf(e,t){if(typeof t!=="string"||t==="")throw Error("parent model unavailable");return t}`, teamModelInherit, teamModel237Inherit, false},
		{"Team effort launcher", "if(!i)throw Error(\"effort\");t.push(`--effort ${i}`);", "if(!i)throw Error(\"effort\");t.push(`--effort ${i}`);", teamEffortInherit, teamEffort237Inherit, false},
	} {
		t.Run(test.label, func(t *testing.T) {
			if test.padded {
				assertPatchReplacementPrefix(t, "2.1.233/"+test.label, test.oldBytes, test.old)
				assertPatchReplacementPrefix(t, "2.1.237/"+test.label, test.currentBytes, test.current)
				return
			}
			assertExactPatchReplacement(t, "2.1.233/"+test.label, test.oldBytes, test.old)
			assertExactPatchReplacement(t, "2.1.237/"+test.label, test.currentBytes, test.current)
		})
	}
}
