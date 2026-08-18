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

var patchSpecs = []patchSpec{
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

func BuildRowsEnvironment(rows []config.PickerRow) (string, error) {
	bytes, err := json.Marshal(rows)
	return string(bytes), err
}

func ValidatePatchBytes(disk []byte, image Image) error {
	_, err := buildPatchPlan(disk, image)
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

func buildPatchPlan(disk []byte, image Image) ([]patchPlanEntry, error) {
	if err := validateImagePatchRange(disk, image); err != nil {
		return nil, err
	}
	entries := make([]patchPlanEntry, 0, len(patchSpecs))
	for _, spec := range patchSpecs {
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

func Patch(process *Process, executable string, disk []byte, image Image) error {
	if !samePath(process.ImagePath, executable) {
		return fmt.Errorf("Claude child 映像不一致：%s", process.ImagePath)
	}
	plan, err := buildPatchPlan(disk, image)
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

func ResolveAndRead(configured string) (Discovery, []byte, Image, error) {
	discovery, err := Discover(configured)
	if err != nil {
		return Discovery{}, nil, Image{}, err
	}
	disk, image, err := ReadImage(discovery.ExecutablePath)
	if err != nil {
		return Discovery{}, nil, Image{}, err
	}
	if err := ValidatePatchBytes(disk, image); err != nil {
		return Discovery{}, nil, Image{}, err
	}
	return discovery, disk, image, nil
}

func Probe(configured string) (Discovery, error) {
	discovery, disk, image, err := ResolveAndRead(configured)
	if err != nil {
		return Discovery{}, err
	}
	process, err := CreateSuspended(discovery.ExecutablePath, []string{"--version"}, filepath.Dir(discovery.ExecutablePath), nil)
	if err != nil {
		return Discovery{}, err
	}
	defer process.Close()
	defer process.Terminate(1)
	if !samePath(process.ImagePath, discovery.ExecutablePath) {
		return Discovery{}, fmt.Errorf("Claude child 映像路径不一致：%s", process.ImagePath)
	}
	imageBase, err := process.FindImageBase(disk[:image.SizeOfHeaders], image.ImageBaseOffset)
	if err != nil {
		return Discovery{}, err
	}
	if err := verifyMappedImage(process, imageBase, disk, image); err != nil {
		return Discovery{}, err
	}
	if process.Resumed() {
		return Discovery{}, errors.New("只读 probe 不得 resume child")
	}
	return discovery, nil
}
