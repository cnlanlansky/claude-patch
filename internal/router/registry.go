package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type RegistryRecord struct {
	ID        string `json:"id"`
	ProcessID uint32 `json:"processId"`
	StartedAt string `json:"startedAt"`
	RouterPID int    `json:"routerPid"`
	RouterID  string `json:"routerId"`
}

type Registry struct {
	directory string
	routerID  string
	owned     map[string]struct{}
	mu        sync.Mutex
}

func NewRegistry() *Registry {
	return newRegistry(filepath.Join(os.TempDir(), "claude-router-sessions"))
}

func newRegistry(directory string) *Registry {
	return &Registry{directory: directory, routerID: randomHex(16), owned: make(map[string]struct{})}
}

func (registry *Registry) path(id string) string {
	safe := strings.NewReplacer("/", "%2F", "\\", "%5C", ":", "%3A").Replace(id)
	return filepath.Join(registry.directory, registry.routerID+"-"+safe+".json")
}

func (registry *Registry) Register(record RegistryRecord) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := os.MkdirAll(registry.directory, 0o700); err != nil {
		return err
	}
	record.RouterPID = os.Getpid()
	record.RouterID = registry.routerID
	bytes, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := registry.path(record.ID)
	temporary := fmt.Sprintf("%s.%d.%s.tmp", path, os.Getpid(), randomHex(8))
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, append(bytes, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(path)
		if err = os.Rename(temporary, path); err != nil {
			return err
		}
	}
	registry.owned[record.ID] = struct{}{}
	return nil
}

func (registry *Registry) Remove(id string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.owned, id)
	err := os.Remove(registry.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (registry *Registry) List() ([]RegistryRecord, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := os.MkdirAll(registry.directory, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(registry.directory)
	if err != nil {
		return nil, err
	}
	var records []RegistryRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(registry.directory, entry.Name())
		bytes, err := os.ReadFile(path)
		var record RegistryRecord
		if err != nil || json.Unmarshal(bytes, &record) != nil || record.ID == "" || record.ProcessID == 0 || record.RouterPID <= 0 || record.RouterID == "" || !processAlive(uint32(record.RouterPID)) {
			_ = os.Remove(path)
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].StartedAt == records[right].StartedAt {
			return records[left].ID < records[right].ID
		}
		return records[left].StartedAt < records[right].StartedAt
	})
	return records, nil
}

func (registry *Registry) Close() error {
	registry.mu.Lock()
	ids := make([]string, 0, len(registry.owned))
	for id := range registry.owned {
		ids = append(ids, id)
	}
	registry.mu.Unlock()
	var errs []error
	for _, id := range ids {
		errs = append(errs, registry.Remove(id))
	}
	return errors.Join(errs...)
}

func nowISO() string { return time.Now().UTC().Format(time.RFC3339Nano) }
