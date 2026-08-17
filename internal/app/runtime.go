package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cnlanlansky/claude-patch/internal/claude"
	"github.com/cnlanlansky/claude-patch/internal/config"
	"github.com/cnlanlansky/claude-patch/internal/router"
)

type Runtime struct {
	Executable string
	Loaded     config.Loaded
	Router     *router.Server

	mu       sync.Mutex
	sessions map[string]*claude.Session
	stopping bool
	stopOnce sync.Once
	stopErr  error
}

func StartRuntime(executable string) (*Runtime, error) {
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return nil, err
	}
	loaded, err := config.Load(absolute)
	if err != nil {
		return nil, err
	}
	if discovery, discoverErr := claude.Discover(loaded.Config.Claude.Executable); discoverErr == nil {
		loaded.Config.Claude.Executable = discovery.RequestedPath
	}
	runtime := &Runtime{Executable: absolute, Loaded: loaded, sessions: make(map[string]*claude.Session)}
	status, _ := CommandState(absolute)
	server, err := router.Start(router.Options{
		ConfigPath: loaded.Path, ExecutablePath: absolute,
		CommandPath:     status.Command,
		InitialConfig:   loaded.Config,
		OnConfigChanged: func(value config.Config) { runtime.mu.Lock(); runtime.Loaded.Config = value; runtime.mu.Unlock() },
		OnStopClaude:    runtime.stopSession,
	})
	if err != nil {
		return nil, err
	}
	runtime.Router = server
	return runtime, nil
}

func (runtime *Runtime) StartClaude(args []string) (*claude.Session, error) {
	runtime.mu.Lock()
	stopping := runtime.stopping
	runtime.mu.Unlock()
	if stopping {
		return nil, errors.New("Claude Patch 正在停止")
	}
	token, err := claudeToken()
	if err != nil {
		return nil, err
	}
	session, err := claude.StartSession(runtime.Router.Config(), runtime.Router.Origin, token, args)
	if err != nil {
		return nil, err
	}
	runtime.mu.Lock()
	if runtime.stopping {
		runtime.mu.Unlock()
		_ = session.Stop()
		_ = session.Close()
		return nil, errors.New("Claude Patch 正在停止")
	}
	runtime.sessions[session.ID] = session
	runtime.mu.Unlock()
	if err := runtime.Router.Register(router.Session{ID: session.ID, Token: token, ProcessID: session.Process.ProcessID, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Child: session}); err != nil {
		runtime.mu.Lock()
		delete(runtime.sessions, session.ID)
		runtime.mu.Unlock()
		_ = session.Stop()
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func (runtime *Runtime) WaitClaude(session *claude.Session) (uint32, error) {
	code, waitErr := session.Wait()
	runtime.mu.Lock()
	delete(runtime.sessions, session.ID)
	runtime.mu.Unlock()
	runtime.Router.Remove(session.ID)
	return code, errors.Join(waitErr, session.Close())
}

func (runtime *Runtime) stopSession(id string) error {
	runtime.mu.Lock()
	session := runtime.sessions[id]
	delete(runtime.sessions, id)
	runtime.mu.Unlock()
	if session == nil {
		return nil
	}
	return errors.Join(session.Stop(), session.Close())
}

func (runtime *Runtime) Stop() error {
	runtime.stopOnce.Do(func() {
		runtime.mu.Lock()
		runtime.stopping = true
		sessions := make([]*claude.Session, 0, len(runtime.sessions))
		for _, session := range runtime.sessions {
			sessions = append(sessions, session)
		}
		runtime.sessions = make(map[string]*claude.Session)
		runtime.mu.Unlock()

		var errs []error
		for _, session := range sessions {
			errs = append(errs, session.Stop(), session.Close())
		}
		if runtime.Router != nil {
			errs = append(errs, runtime.Router.Stop())
		}
		runtime.stopErr = errors.Join(errs...)
	})
	return runtime.stopErr
}

func ExecutablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

func claudeToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("生成 Claude session token：%w", err)
	}
	id := hex.EncodeToString(bytes)
	if id == "" {
		return "", errors.New("生成 Claude session token 失败")
	}
	return id, nil
}
