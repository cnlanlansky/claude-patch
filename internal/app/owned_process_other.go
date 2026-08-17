//go:build !windows

package app

type OwnedProcessGroup struct{}

func JoinOwnedProcessGroup() (*OwnedProcessGroup, error)         { return nil, nil }
func OpenOrCreateOwnedProcessGroup() (*OwnedProcessGroup, error) { return nil, nil }
func (*OwnedProcessGroup) Terminate() error                      { return nil }
func (*OwnedProcessGroup) Close() error                          { return nil }
