package claude

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

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
	for _, spec := range patchSpecs {
		if len(spec.replacement) > len(spec.original) {
			t.Fatalf("%s 超出 patch 字节预算：%d/%d", spec.label, len(spec.replacement), len(spec.original))
		}
	}
	fast := patchSpecs[2]
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

func TestCurrentClaude233Markers(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_LIVE_PROBE") == "" {
		t.Skip("设置 CLAUDE_PATCH_LIVE_PROBE=1 才检查本机 Claude")
	}
	discovery, disk, image, err := ResolveAndRead("")
	if err != nil {
		t.Fatal(err)
	}
	if discovery.PackageVersion != SupportedVersion {
		t.Fatalf("版本错误：%s", discovery.PackageVersion)
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

func TestPatchPlanRejectsMissingMarker(t *testing.T) {
	image := testPatchImage()
	disk := testPatchDisk()
	old := patchSpecs
	patchSpecs = []patchSpec{{label: "missing", original: []byte("missing-marker"), replacement: []byte("x")}}
	t.Cleanup(func() { patchSpecs = old })
	if _, err := buildPatchPlan(disk, image); err == nil {
		t.Fatal("缺失 marker 未被拒绝")
	}
}

func TestPatchPlanRejectsDuplicateMarker(t *testing.T) {
	image := testPatchImage()
	marker := []byte("duplicate-marker")
	old := patchSpecs
	patchSpecs = []patchSpec{{label: "duplicate", original: marker, replacement: marker}}
	t.Cleanup(func() { patchSpecs = old })
	disk := testPatchDisk(marker, marker)
	if _, err := buildPatchPlan(disk, image); err == nil {
		t.Fatal("重复 marker 未被拒绝")
	}
}

func TestPatchPlanRejectsMarkerOutsideBun(t *testing.T) {
	image := testPatchImage()
	disk := make([]byte, 0x700)
	copy(disk[0x100:], patchSpecs[0].original)
	for offset, spec := range patchSpecs[1:] {
		copy(disk[0x200+offset*0x40:], spec.original)
	}
	if _, err := buildPatchPlan(disk, image); err == nil {
		t.Fatal(".bun 外 marker 未被拒绝")
	}
}

func TestPatchPlanRejectsOverlap(t *testing.T) {
	image := testPatchImage()
	first := []byte("first-marker")
	second := []byte("second-marker")
	old := patchSpecs
	patchSpecs = []patchSpec{{label: "first", original: first, replacement: first}, {label: "second", original: second, replacement: second}}
	t.Cleanup(func() { patchSpecs = old })
	disk := make([]byte, 0x600)
	copy(disk[0x200:], first)
	copy(disk[0x200+len(first)-2:], second)
	if _, err := buildPatchPlan(disk, image); err == nil {
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

func TestSubagentRequestEffortKeepsToolContext(t *testing.T) {
	if !bytes.Equal(sqEffortRequestInherit, []byte("effortValue:Wb(re),")) {
		t.Fatalf("request effort 必须从 tool context 读取：%q", sqEffortRequestInherit)
	}
}
