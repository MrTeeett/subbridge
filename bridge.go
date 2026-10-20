package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cacheData struct {
	Version     int       `json:"version"`
	Fingerprint string    `json:"source_fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
	Links       []string  `json:"links"`
	Warnings    []string  `json:"warnings"`
}

// bridge owns its immutable configuration and synchronizes cache snapshots.
// HTTP reads never wait for the source; only a refresh changes the cache.
type bridge struct {
	config           config
	token            string
	sourceToken      string
	panelToken       string
	client           *http.Client
	mu               sync.RWMutex
	refreshMu        sync.Mutex
	cache            cacheData
	lastError        string
	identity         string
	lastAttempts     int
	activity         string
	sources          []*bridge
	groupRefreshMu   sync.Mutex
	syncIssue        string
	lastSync         time.Time
	syncedNodes      int
	syncedClients    int
	panelPresence    *panelPresence
	panelProbe       chan struct{}
	selectionChanges *[]configurationChange
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return safeFailure("не удалось создать каталог данных", err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return errors.New("каталог данных должен иметь права 700")
	}
	return nil
}

func atomicWrite(path string, body []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".subbridge-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err = f.Write(body); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func privateFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("файлу с секретами нужны права 600")
	}
	return readBounded(f, limit)
}

func loadToken(directory string) (string, error) {
	path := filepath.Join(directory, "token")
	b, err := privateFile(path, 128)
	if errors.Is(err, os.ErrNotExist) {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return "", safeFailure("не удалось создать токен", err)
		}
		b = []byte(base64.RawURLEncoding.EncodeToString(random))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			b, err = privateFile(path, 128)
		} else if err == nil {
			_, writeErr := f.Write(b)
			syncErr := f.Sync()
			closeErr := f.Close()
			err = errors.Join(writeErr, syncErr, closeErr)
		}
		if err != nil {
			return "", safeFailure("не удалось сохранить токен", err)
		}
	} else if err != nil {
		return "", safeFailure("не удалось прочитать приватный токен", err)
	}
	token := strings.TrimSpace(string(b))
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || len(token) != 43 {
		return "", errors.New("повреждён файл токена; автоматическая смена адреса подписки запрещена")
	}
	return token, nil
}

func newBridge(c config) (*bridge, error) {
	b, err := newSourceBridge(c)
	if err != nil {
		return nil, err
	}
	primaryDirectory := filepath.Join(c.DataDir, "sources", "default")
	if err := privateDir(primaryDirectory); err != nil {
		return nil, err
	}
	b.sourceToken, err = loadToken(primaryDirectory)
	if err != nil {
		return nil, err
	}
	panelDirectory := filepath.Join(c.DataDir, "panel")
	if err := privateDir(panelDirectory); err != nil {
		return nil, err
	}
	b.panelToken, err = loadToken(panelDirectory)
	if err != nil {
		return nil, err
	}
	for _, source := range c.Sources {
		child := c
		child.Source, child.SourceName, child.Sources = source.Source, source.Name, nil
		child.DataDir = filepath.Join(c.DataDir, "sources", source.ID)
		child.Panel = nil
		member, err := newSourceBridge(child)
		if err != nil {
			return nil, err
		}
		b.sources = append(b.sources, member)
	}
	b.migrateConfigurationSelection()
	return b, nil
}

func newSourceBridge(c config) (*bridge, error) {
	if err := privateDir(c.DataDir); err != nil {
		return nil, err
	}
	token, err := loadToken(c.DataDir)
	if err != nil {
		return nil, err
	}
	identity, err := sourceIdentity(c)
	if err != nil {
		return nil, err
	}
	effective := c.Source
	effective.HWID = identity
	b := &bridge{config: c, token: token, identity: identity, client: sourceClient(), cache: cacheData{Version: 1, Fingerprint: sourceFingerprint(effective)}, panelProbe: make(chan struct{}, 1)}
	data, err := privateFile(filepath.Join(c.DataDir, "cache.json"), maxBytes*2)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return nil, safeFailure("не удалось прочитать приватный кеш", err)
	}
	var cached cacheData
	if json.Unmarshal(data, &cached) != nil {
		b.lastError = "повреждён кеш; ожидается обновление"
		return b, nil
	}
	if cached.Version != 1 || cached.Fingerprint != b.cache.Fingerprint || cached.UpdatedAt.IsZero() || cached.UpdatedAt.After(time.Now().Add(time.Minute)) {
		return b, nil
	}
	parsed, err := parseSubscription(strings.Join(cached.Links, "\n"))
	if err != nil || len(parsed.Links) != len(cached.Links) {
		b.lastError = "некорректный кеш; ожидается обновление"
		return b, nil
	}
	b.cache = cached
	return b, nil
}

func (b *bridge) sourceSnapshot() (cacheData, string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	copy := b.cache
	copy.Links = append([]string(nil), copy.Links...)
	copy.Warnings = append([]string(nil), copy.Warnings...)
	return copy, b.lastError
}

func (b *bridge) refreshSource(ctx context.Context) (err error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	defer func() {
		if err != nil {
			b.mu.Lock()
			b.lastError = err.Error()
			b.mu.Unlock()
		}
	}()
	parsed, used, err := b.resolveWithIdentity(ctx)
	if err != nil {
		return err
	}
	if panelEnabled(b.config.Panel) && len(strings.Join(exportLinks(parsed.Links, b.config.ExportProfile, "all"), "\n"))+1 > 2<<20 {
		return errors.New("выдача превышает лимит внешней подписки 3x-ui (2 МиБ)")
	}
	cached, _ := b.sourceSnapshot()
	cached.Fingerprint = sourceFingerprint(used)
	cached.UpdatedAt = time.Now().UTC()
	cached.Links = parsed.Links
	cached.Warnings = parsed.Warnings
	encoded, err := json.Marshal(cached)
	if err != nil {
		return errors.New("не удалось сериализовать кеш")
	}
	if err := atomicWrite(filepath.Join(b.config.DataDir, "cache.json"), encoded); err != nil {
		return safeFailure("не удалось сохранить кеш; предыдущая версия сохранена", err)
	}
	b.mu.Lock()
	b.cache = cached
	b.lastError = ""
	b.mu.Unlock()
	return nil
}

func (b *bridge) subscriptionURL() string { return b.config.PublicURL + "/sub/" + b.token }

func (b *bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	base, _ := url.Parse(b.config.PublicURL)
	prefix := strings.TrimRight(base.Path, "/")
	if r.URL.Path == prefix+"/health" {
		cached, issue := b.snapshot()
		status := "ok"
		code := http.StatusOK
		if len(cached.Links) == 0 {
			status = "unavailable"
			code = 503
		} else if issue != "" || time.Since(cached.UpdatedAt) > time.Duration(b.config.RefreshSeconds)*time.Second {
			status = "stale"
		}
		body, _ := json.Marshal(map[string]any{"status": status, "nodes": len(cached.Links), "last_success": cached.UpdatedAt})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	var scoped *bridge
	panelOnly := b.panelToken != "" && subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(prefix+"/sub/"+b.panelToken)) == 1
	if !panelOnly && subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(prefix+"/sub/"+b.token)) != 1 {
		for _, member := range b.subscriptions() {
			token := member.Bridge.token
			if member.Bridge == b {
				token = b.sourceToken
			}
			if subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(prefix+"/sub/"+token)) == 1 {
				scoped = member.Bridge
				break
			}
		}
		if scoped == nil {
			http.NotFound(w, r)
			return
		}
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query", 400)
		return
	}
	for key, values := range query {
		if len(values) != 1 || (key != "profile" && key != "format" && key != "protocol" && key != "source") {
			http.Error(w, "invalid query", 400)
			return
		}
	}
	mode := first(query.Get("profile"), b.config.ExportProfile)
	format := first(query.Get("format"), "raw")
	protocol := first(query.Get("protocol"), "all")
	if !validProfile(mode) || (format != "raw" && format != "base64") || (protocol != "all" && protocol != "vless") {
		http.Error(w, "invalid query", 400)
		return
	}
	cached, issue := b.snapshot()
	if scoped != nil {
		cached, issue = scoped.sourceSnapshot()
	}
	if panelOnly {
		cached, issue = b.panelSnapshot()
	}
	if id := query.Get("source"); id != "" {
		member := b.sourceByID(id)
		if panelOnly || member == nil || (scoped != nil && scoped != member) {
			http.NotFound(w, r)
			return
		}
		cached, issue = member.sourceSnapshot()
	}
	links := exportLinks(cached.Links, mode, protocol)
	emptySelection := panelOnly && b.config.Panel != nil && b.config.Panel.ConfigSelection != nil
	if len(links) == 0 && !emptySelection {
		http.Error(w, "subscription unavailable", 503)
		return
	}
	body := []byte(strings.Join(links, "\n") + "\n")
	if format == "base64" {
		body = []byte(base64.StdEncoding.EncodeToString(body))
	}
	if issue != "" || time.Since(cached.UpdatedAt) > time.Duration(b.config.RefreshSeconds)*time.Second {
		w.Header().Set("X-SubBridge-Stale", "true")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("profile-update-interval", "1")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
