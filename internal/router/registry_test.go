package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrySharesMetadataWithoutTokenAndCleansOwnedRecords(t *testing.T) {
	directory := t.TempDir()
	first := newRegistry(directory)
	second := newRegistry(directory)
	if err := first.Register(RegistryRecord{ID: "registry-session", ProcessID: uint32(os.Getpid()), StartedAt: "now"}); err != nil {
		t.Fatal(err)
	}
	records, err := second.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "registry-session" || records[0].ProcessID != uint32(os.Getpid()) {
		t.Fatalf("跨 Router registry 异常：%+v", records)
	}
	encoded, _ := json.Marshal(records)
	if strings.Contains(strings.ToLower(string(encoded)), "token") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("registry 泄露 token：%s", encoded)
	}
	path := first.path("registry-session")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Close 未清理 owned record：%v", err)
	}
}

func TestRegistryRemovesDeadRouterRecord(t *testing.T) {
	registry := newRegistry(t.TempDir())
	record := RegistryRecord{ID: "dead", ProcessID: 123, StartedAt: "now", RouterPID: 0x7fffffff, RouterID: "dead-router"}
	bytes, _ := json.Marshal(record)
	path := filepath.Join(registry.directory, "dead.json")
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("死亡 Router record 未清理：%+v", records)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("死亡 Router 文件仍存在：%v", err)
	}
}
