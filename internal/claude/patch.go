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
)

type patchSpec struct {
	label       string
	original    []byte
	replacement []byte
}

var patchSpecs = []patchSpec{
	{"picker", fynOriginal, fynReader},
	{"context lookup", contextOriginal, contextReader},
	{"fast predicate", fastSupportedOriginal, fastSupportedReader},
	{"fast wire state", fastWireStateOriginal, fastWireStateReader},
	{"fast wire speed", fastWireSpeedOriginal, fastWireSpeedReader},
	{"project client", clientOriginal, clientReader},
}

func BuildRowsEnvironment(rows []config.PickerRow) (string, error) {
	bytes, err := json.Marshal(rows)
	return string(bytes), err
}

func ValidatePatchBytes(disk []byte, image Image) error {
	for _, spec := range patchSpecs {
		if len(spec.replacement) > len(spec.original) {
			return fmt.Errorf("%s 超出字节预算", spec.label)
		}
		offsets := byteOffsets(disk, spec.original)
		if len(offsets) != 1 {
			return fmt.Errorf("%s 磁盘原体不唯一：%d", spec.label, len(offsets))
		}
		sectionOffset := offsets[0] - int(image.Bun.RawOffset)
		if sectionOffset < 0 || sectionOffset+len(spec.original) > int(image.Bun.VirtualSize) {
			return fmt.Errorf("%s 不在 .bun 范围", spec.label)
		}
	}
	return nil
}

func Patch(process *Process, executable string, disk []byte, image Image) error {
	if process.Resumed() {
		return errors.New("只能 patch suspended child")
	}
	if !samePath(process.ImagePath, executable) {
		return fmt.Errorf("Claude child 映像不一致：%s", process.ImagePath)
	}
	if err := ValidatePatchBytes(disk, image); err != nil {
		return err
	}
	imageBase, err := process.FindImageBase(disk[:image.SizeOfHeaders], image.ImageBaseOffset)
	if err != nil {
		return err
	}
	bunAddress := imageBase + uintptr(image.Bun.VirtualAddress)
	for _, spec := range patchSpecs {
		diskOffset := byteOffsets(disk, spec.original)[0] - int(image.Bun.RawOffset)
		remoteOffsets, err := remoteByteOffsets(process, bunAddress, int(image.Bun.VirtualSize), spec.original)
		if err != nil {
			return fmt.Errorf("%s remote scan: %w", spec.label, err)
		}
		if len(remoteOffsets) != 1 || remoteOffsets[0] != diskOffset {
			return fmt.Errorf("%s mapped 原体不一致：%v，磁盘 offset=%d", spec.label, remoteOffsets, diskOffset)
		}
		replacement := make([]byte, len(spec.original))
		for index := range replacement {
			replacement[index] = 0x20
		}
		copy(replacement, spec.replacement)
		if err := process.PatchData(bunAddress+uintptr(remoteOffsets[0]), spec.original, replacement); err != nil {
			return fmt.Errorf("%s: %w", spec.label, err)
		}
	}
	return nil
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
	for compared := 0; compared < int(image.Bun.VirtualSize); {
		size := min(remoteScanChunkBytes, int(image.Bun.VirtualSize)-compared)
		remote, err := process.ReadMemory(imageBase+uintptr(image.Bun.VirtualAddress)+uintptr(compared), size)
		if err != nil {
			return Discovery{}, err
		}
		diskChunk := disk[int(image.Bun.RawOffset)+compared : int(image.Bun.RawOffset)+compared+size]
		if !bytes.Equal(remote, diskChunk) {
			return Discovery{}, fmt.Errorf("mapped .bun 与磁盘 raw data 不一致，chunk=%d", compared)
		}
		compared += size
	}
	if process.Resumed() {
		return Discovery{}, errors.New("只读 probe 不得 resume child")
	}
	return discovery, nil
}
