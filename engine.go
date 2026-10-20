package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type logBuffer struct {
	mu     sync.Mutex
	lines  []string
	output io.Writer
}

func (r *logBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		r.lines = append(r.lines, line)
	}
	if len(r.lines) > 100 {
		r.lines = append([]string(nil), r.lines[len(r.lines)-100:]...)
	}
	return r.output.Write(p)
}
func (r *logBuffer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

type engine struct {
	ctx      context.Context
	path     string
	current  atomic.Pointer[bridge]
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	commands chan string
	server   *http.Server
	logger   *log.Logger
	logs     *logBuffer
	errors   chan error
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
}
func newEngine(ctx context.Context, path string, c config, output io.Writer) (*engine, error) {
	e := &engine{ctx: ctx, path: path, commands: make(chan string, 1), errors: make(chan error, 4)}
	e.logs = &logBuffer{output: output}
	e.logger = log.New(e.logs, "subbridge: ", log.LstdFlags)
	if err := e.apply(c, false); err != nil {
		return nil, err
	}
	return e, nil
}
func (e *engine) ServeHTTP(w http.ResponseWriter, r *http.Request) { e.current.Load().ServeHTTP(w, r) }
func (e *engine) apply(c config, persist bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyLocked(c, persist, false, nil)
}

// Applying the picker saves only its selection and requests one immediate sync.
// Automatic synchronization keeps its previous enabled/disabled setting.
func (e *engine) applySelection(selection *configurationSelection, changes *[]configurationChange) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.current.Load().config
	if c.Panel == nil || c.Panel.URL == "" {
		return errors.New("сначала сохраните подключение и получателей 3x-ui")
	}
	c.Sources = slices.Clone(c.Sources)
	p := *c.Panel
	p.ConfigSelection = selection
	c.Panel = &p
	previous := p.Enabled
	enabled := true
	p.Enabled = &enabled
	if err := c.validate(filepath.Dir(e.path)); err != nil {
		return err
	}
	if _, err := newPanel(p); err != nil {
		return err
	}
	p.Enabled = previous
	if changes != nil {
		refs := make([]configurationReference, 0, len(*changes))
		for _, change := range *changes {
			if change.Selected != selection.contains(change.configurationReference) {
				return errors.New("JSON: изменения не совпадают с выбором конфигов")
			}
			refs = append(refs, change.configurationReference)
		}
		if err := (&configurationSelection{Nodes: refs}).validate(); err != nil {
			return err
		}
	}
	return e.applyLocked(c, true, true, changes)
}

// The caller holds e.mu throughout configuration replacement and worker handoff.
func (e *engine) applyLocked(c config, persist, syncSelection bool, changes *[]configurationChange) error {
	if err := c.validate(filepath.Dir(e.path)); err != nil {
		return err
	}
	old := e.current.Load()
	if old != nil && old.config.DataDir != c.DataDir {
		return errors.New("каталог данных изменяется только через конфигурационный файл")
	}
	var err error
	if persist && panelEnabled(c.Panel) {
		_, err = newPanel(*c.Panel)
		if err != nil {
			return err
		}
	}
	var listener net.Listener
	if old == nil || old.config.Listen != c.Listen {
		listener, err = net.Listen("tcp", c.Listen)
		if err != nil {
			return safeFailure("адрес подписки недоступен: порт занят или IP не назначен", err)
		}
	}
	if listener != nil {
		defer func() {
			if listener != nil {
				_ = listener.Close()
			}
		}()
	}
	// Finish the previous worker before opening state for its successor.
	if e.cancel != nil {
		e.cancel()
		<-e.done
		e.cancel = nil
	}
	b, err := newBridge(c)
	if err != nil {
		if old != nil {
			e.startWorker(old)
		}
		return err
	}
	if changes != nil {
		clone := slices.Clone(*changes)
		b.selectionChanges = &clone
	}
	migratedSelection := c.Panel != nil && c.Panel.ConfigSelection != nil && !slices.Equal(c.Panel.ConfigSelection.Nodes, b.config.Panel.ConfigSelection.Nodes)
	if persist || migratedSelection {
		if err := writePrivateJSON(e.path, b.config); err != nil {
			if old != nil {
				e.startWorker(old)
			}
			return err
		}
	}
	e.current.Store(b)
	e.startWorkerWithSelectionSync(b, syncSelection)
	if listener != nil {
		previous := e.server
		e.server = newHTTPServer(e)
		current := e.server
		bound := listener
		listener = nil
		go func() {
			err := current.Serve(bound)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				select {
				case e.errors <- safeFailure("сервер подписок остановлен", err):
				default:
				}
			}
		}()
		if previous != nil {
			go closeHTTP(previous)
		}
	}
	return nil
}
func closeHTTP(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if server.Shutdown(ctx) != nil {
		_ = server.Close()
	}
}
func (e *engine) startWorker(b *bridge) {
	e.startWorkerWithSelectionSync(b, false)
}

func (e *engine) startWorkerWithSelectionSync(b *bridge, syncSelection bool) {
	ctx, cancel := context.WithCancel(e.ctx)
	e.cancel = cancel
	e.done = make(chan struct{})
	done := e.done
	var panel *panelClient
	if panelEnabled(b.config.Panel) || syncSelection {
		var err error
		panel, err = newPanel(*b.config.Panel)
		if err != nil {
			b.mu.Lock()
			b.syncIssue = err.Error()
			b.mu.Unlock()
			e.logger.Printf("синхронизация: %v", err)
		}
	}
	go func() { defer close(done); worker(ctx, b, panel, e.logger, e.commands, syncSelection) }()
}
func (e *engine) trigger(action string) bool {
	select {
	case e.commands <- action:
		return true
	default:
		return false
	}
}
func (e *engine) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
		<-e.done
	}
	if e.server != nil {
		closeHTTP(e.server)
	}
}
