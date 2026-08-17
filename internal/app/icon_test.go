package app

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

type icoTestEntry struct {
	Width, Height, Colors, Reserved byte
	Planes, Bits                    uint16
	Size, Offset                    uint32
}

func TestApplicationIconContainsExpectedSizes(t *testing.T) {
	path := filepath.Join("..", "..", "assets", "claude-patch.ico")
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes) < 6 || binary.LittleEndian.Uint16(bytes[0:2]) != 0 || binary.LittleEndian.Uint16(bytes[2:4]) != 1 {
		t.Fatal("应用图标不是有效 ICO")
	}
	count := int(binary.LittleEndian.Uint16(bytes[4:6]))
	if count != 8 || len(bytes) < 6+count*16 {
		t.Fatalf("应用图标尺寸目录错误：%d", count)
	}
	expected := []int{16, 20, 24, 32, 48, 64, 128, 256}
	for index, size := range expected {
		offset := 6 + index*16
		var entry icoTestEntry
		entry.Width = bytes[offset]
		entry.Height = bytes[offset+1]
		entry.Planes = binary.LittleEndian.Uint16(bytes[offset+4:])
		entry.Bits = binary.LittleEndian.Uint16(bytes[offset+6:])
		entry.Size = binary.LittleEndian.Uint32(bytes[offset+8:])
		entry.Offset = binary.LittleEndian.Uint32(bytes[offset+12:])
		width := int(entry.Width)
		if width == 0 {
			width = 256
		}
		if width != size || entry.Planes != 1 || entry.Bits != 32 || entry.Size == 0 || int(entry.Offset+entry.Size) > len(bytes) {
			t.Fatalf("ICO 图层 %d 无效：%+v", size, entry)
		}
	}
}
