//go:build !windows

package app

import "errors"

type ManagementInstance struct{}

func AcquireManagementInstance(bool) (*ManagementInstance, bool, error) {
	return nil, false, errors.New("仅支持 Windows")
}

func (*ManagementInstance) Close() error { return nil }
