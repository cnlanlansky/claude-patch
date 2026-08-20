package claude

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/cnlanlansky/claude-patch/internal/config"
	"golang.org/x/sys/windows"
)

const (
	ModelsEnv = "CLAUDE_ROUTER_MODELS"
	OriginEnv = "CLAUDE_ROUTER_ORIGIN"
	TokenEnv  = "CLAUDE_ROUTER_TOKEN"
)

type Session struct {
	ID        string
	Process   *Process
	Discovery Discovery

	done     chan struct{}
	stopOnce sync.Once
	code     uint32
	waitErr  error
	stopErr  error
}

func StartSession(value config.Config, origin, token string, args []string) (*Session, error) {
	if origin == "" || token == "" {
		return nil, fmt.Errorf("Claude session 路由凭据无效")
	}
	loaded, err := resolveAndRead(value.Claude.Executable)
	if err != nil {
		return nil, err
	}
	discovery := loaded.Discovery
	rows, err := BuildRowsEnvironment(config.BuildRows(value))
	if err != nil {
		return nil, err
	}
	workingDirectory := value.Claude.WorkingDirectory
	if workingDirectory == "" {
		workingDirectory, _ = filepath.Abs(".")
	}
	process, err := CreateSuspended(discovery.ExecutablePath, args, workingDirectory, map[string]string{
		ModelsEnv: rows,
		OriginEnv: origin,
		TokenEnv:  token,
	})
	if err != nil {
		return nil, err
	}
	failed := func(cause error) (*Session, error) {
		_ = process.Terminate(1)
		_ = process.Close()
		return nil, cause
	}
	if err := Patch(loaded.profile, process, discovery.ExecutablePath, loaded.disk, loaded.image); err != nil {
		return failed(err)
	}
	if err := process.Resume(); err != nil {
		return failed(err)
	}
	id, err := randomID()
	if err != nil {
		return failed(err)
	}
	session := &Session{ID: id, Process: process, Discovery: discovery, done: make(chan struct{})}
	go session.wait()
	return session, nil
}

func (session *Session) wait() {
	session.code, _, session.waitErr = session.Process.Wait(windows.INFINITE)
	close(session.done)
}

func (session *Session) Wait() (uint32, error) {
	<-session.done
	return session.code, session.waitErr
}

func (session *Session) Stop() error {
	session.stopOnce.Do(func() { session.stopErr = session.Process.Terminate(1) })
	<-session.done
	return session.stopErr
}

func (session *Session) Close() error {
	<-session.done
	return session.Process.Close()
}

func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
