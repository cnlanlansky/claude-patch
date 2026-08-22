package claude

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

const remoteScanChunkBytes = 4 * 1024 * 1024

var (
	//go:embed patches/fyn-original.bin
	fynOriginal []byte
	//go:embed patches/fyn-reader.bin
	fynReader []byte
	//go:embed patches/fast-supported-original.bin
	fastSupportedOriginal []byte
	//go:embed patches/fast-supported-reader.bin
	fastSupportedReader []byte
	//go:embed patches/fast-wire-state-original.bin
	fastWireStateOriginal []byte
	//go:embed patches/fast-wire-state-reader.bin
	fastWireStateReader []byte
	//go:embed patches/fast-wire-speed-original.bin
	fastWireSpeedOriginal []byte
	//go:embed patches/fast-wire-speed-reader.bin
	fastWireSpeedReader []byte
	//go:embed patches/context-lookup-original.bin
	contextOriginal []byte
	//go:embed patches/context-lookup-reader.bin
	contextReader []byte
	//go:embed patches/project-client-original.bin
	clientOriginal []byte
	//go:embed patches/project-client-reader.bin
	clientReader []byte
	//go:embed patches/zde-original.bin
	zdeOriginal []byte
	//go:embed patches/zde-inherit.bin
	zdeInherit []byte
	//go:embed patches/ief-original.bin
	iefOriginal []byte
	//go:embed patches/ief-inherit.bin
	iefInherit []byte
	//go:embed patches/sq-effort-original.bin
	sqEffortOriginal []byte
	//go:embed patches/sq-effort-inherit.bin
	sqEffortInherit []byte
	//go:embed patches/sq-model-original.bin
	sqModelOriginal []byte
	//go:embed patches/sq-model-inherit.bin
	sqModelInherit []byte
	//go:embed patches/sq-effort-request-original.bin
	sqEffortRequestOriginal []byte
	//go:embed patches/sq-effort-request-inherit.bin
	sqEffortRequestInherit []byte
	//go:embed patches/team-model-original.bin
	teamModelOriginal []byte
	//go:embed patches/team-model-inherit.bin
	teamModelInherit []byte
	//go:embed patches/team-effort-original.bin
	teamEffortOriginal []byte
	//go:embed patches/team-effort-inherit.bin
	teamEffortInherit []byte

	//go:embed patches/fyn-237-original.bin
	fyn237Original []byte
	//go:embed patches/fyn-237-reader.bin
	fyn237Reader []byte
	//go:embed patches/fast-supported-237-original.bin
	fastSupported237Original []byte
	//go:embed patches/fast-supported-237-reader.bin
	fastSupported237Reader []byte
	//go:embed patches/fast-wire-state-237-original.bin
	fastWireState237Original []byte
	//go:embed patches/fast-wire-state-237-reader.bin
	fastWireState237Reader []byte
	//go:embed patches/fast-wire-speed-237-original.bin
	fastWireSpeed237Original []byte
	//go:embed patches/fast-wire-speed-237-reader.bin
	fastWireSpeed237Reader []byte
	//go:embed patches/context-lookup-237-original.bin
	context237Original []byte
	//go:embed patches/context-lookup-237-reader.bin
	context237Reader []byte
	//go:embed patches/project-client-237-original.bin
	client237Original []byte
	//go:embed patches/project-client-237-reader.bin
	client237Reader []byte
	//go:embed patches/zde-237-original.bin
	zde237Original []byte
	//go:embed patches/zde-237-inherit.bin
	zde237Inherit []byte
	//go:embed patches/ief-237-original.bin
	ief237Original []byte
	//go:embed patches/ief-237-inherit.bin
	ief237Inherit []byte
	//go:embed patches/sq-model-237-original.bin
	sqModel237Original []byte
	//go:embed patches/sq-model-237-inherit.bin
	sqModel237Inherit []byte
	//go:embed patches/sq-effort-237-original.bin
	sqEffort237Original []byte
	//go:embed patches/sq-effort-237-inherit.bin
	sqEffort237Inherit []byte
	//go:embed patches/sq-effort-request-237-original.bin
	sqEffortRequest237Original []byte
	//go:embed patches/sq-effort-request-237-inherit.bin
	sqEffortRequest237Inherit []byte
	//go:embed patches/team-model-237-original.bin
	teamModel237Original []byte
	//go:embed patches/team-model-237-inherit.bin
	teamModel237Inherit []byte
	//go:embed patches/team-effort-237-original.bin
	teamEffort237Original []byte
	//go:embed patches/team-effort-237-inherit.bin
	teamEffort237Inherit []byte

	//go:embed patches/fyn-239-original.bin
	fyn239Original []byte
	//go:embed patches/fyn-239-reader.bin
	fyn239Reader []byte
	//go:embed patches/context-lookup-239-original.bin
	context239Original []byte
	//go:embed patches/context-lookup-239-reader.bin
	context239Reader []byte
	//go:embed patches/fast-supported-239-original.bin
	fastSupported239Original []byte
	//go:embed patches/fast-supported-239-reader.bin
	fastSupported239Reader []byte
	//go:embed patches/fast-wire-state-239-original.bin
	fastWireState239Original []byte
	//go:embed patches/fast-wire-state-239-reader.bin
	fastWireState239Reader []byte
	//go:embed patches/fast-wire-speed-239-original.bin
	fastWireSpeed239Original []byte
	//go:embed patches/fast-wire-speed-239-reader.bin
	fastWireSpeed239Reader []byte
	//go:embed patches/project-client-239-original.bin
	client239Original []byte
	//go:embed patches/project-client-239-reader.bin
	client239Reader []byte
	//go:embed patches/zde-239-original.bin
	zde239Original []byte
	//go:embed patches/zde-239-inherit.bin
	zde239Inherit []byte
	//go:embed patches/ief-239-original.bin
	ief239Original []byte
	//go:embed patches/ief-239-inherit.bin
	ief239Inherit []byte
	//go:embed patches/sq-model-239-original.bin
	sqModel239Original []byte
	//go:embed patches/sq-model-239-inherit.bin
	sqModel239Inherit []byte
	//go:embed patches/sq-effort-239-original.bin
	sqEffort239Original []byte
	//go:embed patches/sq-effort-239-inherit.bin
	sqEffort239Inherit []byte
	//go:embed patches/sq-effort-request-239-original.bin
	sqEffortRequest239Original []byte
	//go:embed patches/sq-effort-request-239-inherit.bin
	sqEffortRequest239Inherit []byte
	//go:embed patches/team-model-239-original.bin
	teamModel239Original []byte
	//go:embed patches/team-model-239-inherit.bin
	teamModel239Inherit []byte
	//go:embed patches/team-effort-239-original.bin
	teamEffort239Original []byte
	//go:embed patches/team-effort-239-inherit.bin
	teamEffort239Inherit []byte
)

type patchSpec struct {
	label       string
	original    []byte
	replacement []byte
}

type patchPlanEntry struct {
	label        string
	original     []byte
	replacement  []byte
	diskOffset   int
	remoteOffset int
	address      uintptr
}

var patchSpecs233 = []patchSpec{
	{"picker", fynOriginal, fynReader},
	{"context lookup", contextOriginal, contextReader},
	{"fast predicate", fastSupportedOriginal, fastSupportedReader},
	{"fast wire state", fastWireStateOriginal, fastWireStateReader},
	{"fast wire speed", fastWireSpeedOriginal, fastWireSpeedReader},
	{"project client", clientOriginal, clientReader},
	{"subagent model", zdeOriginal, zdeInherit},
	{"subagent model telemetry", iefOriginal, iefInherit},
	{"subagent request model", sqModelOriginal, sqModelInherit},
	{"subagent effort", sqEffortOriginal, sqEffortInherit},
	{"subagent request effort", sqEffortRequestOriginal, sqEffortRequestInherit},
	{"team model", teamModelOriginal, teamModelInherit},
	{"team effort", teamEffortOriginal, teamEffortInherit},
}

var patchSpecs237 = []patchSpec{
	{"picker", fyn237Original, fyn237Reader},
	{"context lookup", context237Original, context237Reader},
	{"fast predicate", fastSupported237Original, fastSupported237Reader},
	{"fast wire state", fastWireState237Original, fastWireState237Reader},
	{"fast wire speed", fastWireSpeed237Original, fastWireSpeed237Reader},
	{"project client", client237Original, client237Reader},
	{"subagent model", zde237Original, zde237Inherit},
	{"subagent model telemetry", ief237Original, ief237Inherit},
	{"subagent request model", sqModel237Original, sqModel237Inherit},
	{"subagent effort", sqEffort237Original, sqEffort237Inherit},
	{"subagent request effort", sqEffortRequest237Original, sqEffortRequest237Inherit},
	{"team model", teamModel237Original, teamModel237Inherit},
	{"team effort", teamEffort237Original, teamEffort237Inherit},
}

var patchSpecs239 = []patchSpec{
	{"picker", fyn239Original, fyn239Reader},
	{"context lookup", context239Original, context239Reader},
	{"fast predicate", fastSupported239Original, fastSupported239Reader},
	{"fast wire state", fastWireState239Original, fastWireState239Reader},
	{"fast wire speed", fastWireSpeed239Original, fastWireSpeed239Reader},
	{"project client", client239Original, client239Reader},
	{"subagent model", zde239Original, zde239Inherit},
	{"subagent model telemetry", ief239Original, ief239Inherit},
	{"subagent request model", sqModel239Original, sqModel239Inherit},
	{"subagent effort", sqEffort239Original, sqEffort239Inherit},
	{"subagent request effort", sqEffortRequest239Original, sqEffortRequest239Inherit},
	{"team model", teamModel239Original, teamModel239Inherit},
	{"team effort", teamEffort239Original, teamEffort239Inherit},
}

var profile237 = newCompatibilityProfile("2.1.237", []string{"@anthropic-ai/claude-code", "@anthropic-ai/claude-code-win32-x64"}, "406167231b3636e55a01d0ce93567256c61e7973489e645883302f14808ae668", patchSpecs237)

var profile239 = newCompatibilityProfile("2.1.239", []string{"@anthropic-ai/claude-code", "@anthropic-ai/claude-code-win32-x64"}, "0bc1304c7847c317cc550007e7561f9bf270eaa68a0e85a3f381afb18ee20a2b", patchSpecs239)

var compatibilityProfiles = []compatibilityProfile{
	newCompatibilityProfile("2.1.233", []string{"@anthropic-ai/claude-code"}, "8ae35d41252b02a7b747097ececf368b6872fab93ca104832b99a8ec5942fabd", patchSpecs233),
	profile237,
	profile239,
}

func BuildRowsEnvironment(rows []config.PickerRow) (string, error) {
	bytes, err := json.Marshal(rows)
	return string(bytes), err
}

func ValidatePatchBytes(profile compatibilityProfile, disk []byte, image Image) error {
	_, err := buildPatchPlan(profile, disk, image)
	return err
}

func validateImagePatchRange(disk []byte, image Image) error {
	if image.SizeOfHeaders == 0 || uint64(image.SizeOfHeaders) > uint64(len(disk)) ||
		image.SizeOfImage < image.SizeOfHeaders || image.Bun.VirtualSize == 0 ||
		image.Bun.RawSize < image.Bun.VirtualSize ||
		uint64(image.Bun.RawOffset)+uint64(image.Bun.RawSize) > uint64(len(disk)) ||
		uint64(image.Bun.RawOffset)+uint64(image.Bun.VirtualSize) > uint64(len(disk)) ||
		uint64(image.Bun.VirtualAddress)+uint64(image.Bun.VirtualSize) > uint64(image.SizeOfImage) {
		return errors.New("映像或 .bun section 范围无效")
	}
	return nil
}

func buildPatchPlan(profile compatibilityProfile, disk []byte, image Image) ([]patchPlanEntry, error) {
	if err := profile.validateDisk(disk); err != nil {
		return nil, err
	}
	if err := validateImagePatchRange(disk, image); err != nil {
		return nil, err
	}
	entries := make([]patchPlanEntry, 0, len(profile.patchSpecs))
	for _, spec := range profile.patchSpecs {
		if len(spec.original) == 0 {
			return nil, fmt.Errorf("%s 原体为空", spec.label)
		}
		if len(spec.replacement) > len(spec.original) {
			return nil, fmt.Errorf("%s 超出字节预算", spec.label)
		}
		offsets := byteOffsets(disk, spec.original)
		if len(offsets) != 1 {
			return nil, fmt.Errorf("%s 磁盘原体不唯一：%d", spec.label, len(offsets))
		}
		diskOffset := offsets[0] - int(image.Bun.RawOffset)
		if diskOffset < 0 || diskOffset+len(spec.original) > int(image.Bun.VirtualSize) {
			return nil, fmt.Errorf("%s 不在 .bun 范围", spec.label)
		}
		entry := patchPlanEntry{
			label:        spec.label,
			original:     append([]byte(nil), spec.original...),
			replacement:  paddedReplacement(spec),
			diskOffset:   diskOffset,
			remoteOffset: diskOffset,
		}
		for _, previous := range entries {
			if entry.diskOffset < previous.diskOffset+len(previous.original) && previous.diskOffset < entry.diskOffset+len(entry.original) {
				return nil, fmt.Errorf("%s 与 %s marker 重叠", entry.label, previous.label)
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func bindPatchPlan(image Image, imageBase uintptr, snapshot []byte, entries []patchPlanEntry) ([]patchPlanEntry, error) {
	if len(snapshot) != int(image.Bun.VirtualSize) {
		return nil, errors.New("mapped .bun snapshot 长度无效")
	}
	for index := range entries {
		entry := &entries[index]
		remoteOffsets := byteOffsets(snapshot, entry.original)
		if len(remoteOffsets) != 1 || remoteOffsets[0] != entry.diskOffset {
			return nil, fmt.Errorf("%s mapped 原体不一致：%v，磁盘 offset=%d", entry.label, remoteOffsets, entry.diskOffset)
		}
		entry.remoteOffset = remoteOffsets[0]
		entry.address = imageBase + uintptr(image.Bun.VirtualAddress) + uintptr(entry.remoteOffset)
		if entry.address < imageBase {
			return nil, fmt.Errorf("%s mapped 地址溢出", entry.label)
		}
	}
	return entries, nil
}

func paddedReplacement(spec patchSpec) []byte {
	replacement := bytes.Repeat([]byte{0x20}, len(spec.original))
	copy(replacement, spec.replacement)
	return replacement
}

func Patch(profile compatibilityProfile, process *Process, executable string, disk []byte, image Image) error {
	if !samePath(process.ImagePath, executable) {
		return fmt.Errorf("Claude child 映像不一致：%s", process.ImagePath)
	}
	plan, err := buildPatchPlan(profile, disk, image)
	if err != nil {
		return err
	}
	if err := process.beginPatch(); err != nil {
		return err
	}
	defer process.endPatch()
	imageBase, err := process.FindImageBase(disk[:image.SizeOfHeaders], image.ImageBaseOffset)
	if err != nil {
		process.markTainted()
		return err
	}
	snapshot, err := readMappedBun(process, imageBase, disk, image)
	if err != nil {
		process.markTainted()
		return err
	}
	plan, err = bindPatchPlan(image, imageBase, snapshot, plan)
	if err != nil {
		process.markTainted()
		return err
	}
	applied := make([]patchPlanEntry, 0, len(plan))
	for _, entry := range plan {
		written, err := process.writeDataLocked(entry.address, entry.original, entry.replacement, true)
		if err != nil {
			var restoreErr error
			if written {
				_, restoreErr = process.writeDataLocked(entry.address, nil, entry.original, false)
			}
			rollbackErr := rollbackPatchesLocked(process, applied)
			process.markTainted()
			if restoreErr != nil || rollbackErr != nil {
				return fmt.Errorf("%s: %w；恢复当前=%v；回滚=%v", entry.label, err, restoreErr, rollbackErr)
			}
			return fmt.Errorf("%s: %w", entry.label, err)
		}
		applied = append(applied, entry)
	}
	return nil
}

func rollbackPatchesLocked(process *Process, applied []patchPlanEntry) error {
	var errs []error
	for index := len(applied) - 1; index >= 0; index-- {
		entry := applied[index]
		if _, err := process.writeDataLocked(entry.address, nil, entry.original, false); err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", entry.label, err))
		}
	}
	return errors.Join(errs...)
}

func readMappedBun(process *Process, imageBase uintptr, disk []byte, image Image) ([]byte, error) {
	if err := validateImagePatchRange(disk, image); err != nil {
		return nil, err
	}
	snapshot := make([]byte, 0, int(image.Bun.VirtualSize))
	for compared := 0; compared < int(image.Bun.VirtualSize); {
		size := min(remoteScanChunkBytes, int(image.Bun.VirtualSize)-compared)
		chunk, err := process.ReadMemory(imageBase+uintptr(image.Bun.VirtualAddress)+uintptr(compared), size)
		if err != nil {
			return nil, fmt.Errorf("mapped .bun 读取失败：%w", err)
		}
		diskChunk := disk[int(image.Bun.RawOffset)+compared : int(image.Bun.RawOffset)+compared+size]
		if !bytes.Equal(chunk, diskChunk) {
			return nil, fmt.Errorf("mapped .bun 与磁盘 raw data 不一致，chunk=%d", compared)
		}
		snapshot = append(snapshot, chunk...)
		compared += size
	}
	return snapshot, nil
}

func verifyMappedImage(process *Process, imageBase uintptr, disk []byte, image Image) error {
	_, err := readMappedBun(process, imageBase, disk, image)
	return err
}

func remoteByteOffsets(process *Process, address uintptr, size int, needle []byte) ([]int, error) {
	seen := make(map[int]struct{})
	carry := []byte(nil)
	for consumed := 0; consumed < size; {
		chunkSize := min(remoteScanChunkBytes, size-consumed)
		chunk, err := process.ReadMemory(address+uintptr(consumed), chunkSize)
		if err != nil {
			return nil, err
		}
		combined := append(append([]byte(nil), carry...), chunk...)
		base := consumed - len(carry)
		for _, offset := range byteOffsets(combined, needle) {
			mapped := base + offset
			if mapped >= 0 && mapped+len(needle) <= size {
				seen[mapped] = struct{}{}
			}
		}
		keep := min(len(combined), len(needle)-1)
		carry = append(carry[:0], combined[len(combined)-keep:]...)
		consumed += chunkSize
	}
	offsets := make([]int, 0, len(seen))
	for offset := range seen {
		offsets = append(offsets, offset)
	}
	for left := 0; left < len(offsets); left++ {
		for right := left + 1; right < len(offsets); right++ {
			if offsets[right] < offsets[left] {
				offsets[left], offsets[right] = offsets[right], offsets[left]
			}
		}
	}
	return offsets, nil
}

type loadedClaude struct {
	Discovery Discovery
	profile   compatibilityProfile
	disk      []byte
	image     Image
}

func resolveAndRead(configured string) (loadedClaude, error) {
	discovery, profile, err := discoverProfile(configured)
	if err != nil {
		return loadedClaude{}, err
	}
	disk, image, err := ReadImage(discovery.ExecutablePath)
	if err != nil {
		return loadedClaude{}, err
	}
	if err := ValidatePatchBytes(profile, disk, image); err != nil {
		return loadedClaude{}, err
	}
	return loadedClaude{Discovery: discovery, profile: profile, disk: disk, image: image}, nil
}

func ResolveAndRead(configured string) (Discovery, []byte, Image, error) {
	loaded, err := resolveAndRead(configured)
	if err != nil {
		return Discovery{}, nil, Image{}, err
	}
	return loaded.Discovery, loaded.disk, loaded.image, nil
}

func Probe(configured string) (Discovery, error) {
	loaded, err := resolveAndRead(configured)
	if err != nil {
		return Discovery{}, err
	}
	process, err := CreateSuspended(loaded.Discovery.ExecutablePath, []string{"--version"}, filepath.Dir(loaded.Discovery.ExecutablePath), nil)
	if err != nil {
		return Discovery{}, err
	}
	defer process.Close()
	defer process.Terminate(1)
	if !samePath(process.ImagePath, loaded.Discovery.ExecutablePath) {
		return Discovery{}, fmt.Errorf("Claude child 映像路径不一致：%s", process.ImagePath)
	}
	imageBase, err := process.FindImageBase(loaded.disk[:loaded.image.SizeOfHeaders], loaded.image.ImageBaseOffset)
	if err != nil {
		return Discovery{}, err
	}
	if err := verifyMappedImage(process, imageBase, loaded.disk, loaded.image); err != nil {
		return Discovery{}, err
	}
	if process.Resumed() {
		return Discovery{}, errors.New("只读 probe 不得 resume child")
	}
	return loaded.Discovery, nil
}
