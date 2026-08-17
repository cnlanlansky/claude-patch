package claude

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

type Image struct {
	Machine          uint16
	ImageBase        uint64
	ImageBaseOffset  uint32
	SizeOfImage      uint32
	SizeOfHeaders    uint32
	SectionAlignment uint32
	FileAlignment    uint32
	Bun              Section
}

type Section struct {
	VirtualAddress uint32
	VirtualSize    uint32
	RawOffset      uint32
	RawSize        uint32
}

func ParseImage(data []byte) (Image, error) {
	if len(data) < 0x40 || binary.LittleEndian.Uint16(data[:2]) != 0x5a4d {
		return Image{}, errors.New("缺少 MZ 签名")
	}
	peOffset := int(binary.LittleEndian.Uint32(data[0x3c:]))
	if peOffset < 0 || peOffset+24 > len(data) || binary.LittleEndian.Uint32(data[peOffset:]) != 0x00004550 {
		return Image{}, errors.New("缺少 PE 签名")
	}
	machine := binary.LittleEndian.Uint16(data[peOffset+4:])
	sectionCount := int(binary.LittleEndian.Uint16(data[peOffset+6:]))
	optionalSize := int(binary.LittleEndian.Uint16(data[peOffset+20:]))
	optional := peOffset + 24
	if machine != pe.IMAGE_FILE_MACHINE_AMD64 || sectionCount <= 0 || sectionCount > 96 || optionalSize < 112 || optional+optionalSize > len(data) {
		return Image{}, errors.New("不是有效 Windows AMD64 PE32+")
	}
	if binary.LittleEndian.Uint16(data[optional:]) != 0x20b {
		return Image{}, errors.New("只支持 PE32+")
	}
	image := Image{
		Machine:          machine,
		ImageBase:        binary.LittleEndian.Uint64(data[optional+24:]),
		ImageBaseOffset:  uint32(optional + 24),
		SectionAlignment: binary.LittleEndian.Uint32(data[optional+32:]),
		FileAlignment:    binary.LittleEndian.Uint32(data[optional+36:]),
		SizeOfImage:      binary.LittleEndian.Uint32(data[optional+56:]),
		SizeOfHeaders:    binary.LittleEndian.Uint32(data[optional+60:]),
	}
	if image.SizeOfHeaders == 0 || int(image.SizeOfHeaders) > len(data) || image.SizeOfImage < image.SizeOfHeaders {
		return Image{}, errors.New("PE headers 范围无效")
	}
	sectionTable := optional + optionalSize
	var found int
	for index := 0; index < sectionCount; index++ {
		offset := sectionTable + index*40
		if offset+40 > len(data) || offset+40 > int(image.SizeOfHeaders) {
			return Image{}, errors.New("section table 超出范围")
		}
		nameBytes := data[offset : offset+8]
		nameEnd := bytes.IndexByte(nameBytes, 0)
		if nameEnd < 0 {
			nameEnd = len(nameBytes)
		}
		name := string(nameBytes[:nameEnd])
		virtualSize := binary.LittleEndian.Uint32(data[offset+8:])
		virtualAddress := binary.LittleEndian.Uint32(data[offset+12:])
		rawSize := binary.LittleEndian.Uint32(data[offset+16:])
		rawOffset := binary.LittleEndian.Uint32(data[offset+20:])
		if uint64(virtualAddress)+uint64(virtualSize) > uint64(image.SizeOfImage) || uint64(rawOffset)+uint64(rawSize) > uint64(len(data)) {
			return Image{}, fmt.Errorf("section %s 超出映像范围", name)
		}
		if name == ".bun" {
			found++
			image.Bun = Section{VirtualAddress: virtualAddress, VirtualSize: virtualSize, RawOffset: rawOffset, RawSize: rawSize}
		}
	}
	if found != 1 || image.Bun.RawSize < image.Bun.VirtualSize {
		return Image{}, fmt.Errorf(".bun section 应唯一且 raw >= virtual，实际 %d", found)
	}
	return image, nil
}

func ReadImage(path string) ([]byte, Image, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, Image{}, err
	}
	image, err := ParseImage(bytes)
	return bytes, image, err
}

func byteOffsets(haystack, needle []byte) []int {
	if len(needle) == 0 {
		return nil
	}
	var offsets []int
	for base := 0; base <= len(haystack)-len(needle); {
		offset := bytes.Index(haystack[base:], needle)
		if offset < 0 {
			break
		}
		offsets = append(offsets, base+offset)
		base += offset + 1
	}
	return offsets
}
