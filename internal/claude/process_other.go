//go:build !windows

package claude

import "errors"

type Process struct{}

func CreateSuspended(string, []string, string, map[string]string) (*Process, error) {
	return nil, errors.New("仅支持 Windows")
}
