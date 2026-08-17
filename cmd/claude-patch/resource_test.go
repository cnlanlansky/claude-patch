package main

import (
	"debug/pe"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsResourceObjectContainsIconSection(t *testing.T) {
	path := filepath.Join("rsrc_windows_amd64.syso")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	file, err := pe.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	section := file.Section(".rsrc")
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || section == nil || section.Size == 0 || len(section.Relocs) < 9 {
		t.Fatalf("Windows 图标资源无效：machine=0x%x section=%v", file.Machine, section)
	}
}
