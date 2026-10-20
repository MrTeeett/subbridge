package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui/*
var webFiles embed.FS

var httpStatusPattern = regexp.MustCompile(`HTTP [0-9]{3}`)

type adminSession struct {
	CSRF    string
	Expires time.Time
}
type loginLimit struct {
	Count   int
	Expires time.Time
}
type adminServer struct {
	engine   *engine
	path     string
	mu       sync.Mutex
	config   managementConfig
	server   *http.Server
	sessions map[string]adminSession
	limits   map[string]loginLimit
}

func startAdmin(e *engine, path string, m managementConfig) (*adminServer, error) {
	a := &adminServer{engine: e, path: path, config: m, sessions: map[string]adminSession{}, limits: map[string]loginLimit{}}
	if err := a.apply(m, false); err != nil {
		return nil, err
	}
	return a, nil
}
func (a *adminServer) configuration() managementConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config
}
func (a *adminServer) apply(m managementConfig, persist bool) error {
	if err := m.validate(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.config
	rebind := a.server == nil || old.IP != m.IP || old.Port != m.Port || old.TLSCert != m.TLSCert || old.TLSKey != m.TLSKey || old.Enabled != m.Enabled
	var listener net.Listener
	if rebind && m.Enabled {
		var err error
		listener, err = net.Listen("tcp", net.JoinHostPort(m.IP, strconv.Itoa(m.Port)))
		if err != nil {
			return safeFailure("IP панели недоступен или порт уже занят", err)
		}
		if m.TLSCert != "" {
			pair, err := tls.LoadX509KeyPair(m.TLSCert, m.TLSKey)
			if err != nil {
				_ = listener.Close()
				return safeFailure("не удалось загрузить сертификат TLS", err)
			}
			listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})
		}
	}
	if persist {
		if err := writePrivateJSON(a.path, m); err != nil {
			if listener != nil {
				_ = listener.Close()
			}
			return err
		}
	}
	if old.PasswordHash != m.PasswordHash || old.Username != m.Username {
		a.sessions = map[string]adminSession{}
	}
	a.config = m
	if rebind {
		previous := a.server
		a.server = nil
		if listener != nil {
			current := newHTTPServer(a)
			a.server = current
			go func() {
				if err := current.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					select {
					case a.engine.errors <- safeFailure("панель управления остановлена", err):
					default:
					}
				}
			}()
		}
		if previous != nil {
			go closeHTTP(previous)
		}
	}
	return nil
}
func (a *adminServer) close() {
	a.mu.Lock()
	s := a.server
	a.server = nil
	a.mu.Unlock()
	if s != nil {
		closeHTTP(s)
	}
}
func sessionKey(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
func (a *adminServer) authenticate(r *http.Request) (adminSession, bool) {
	cookie, err := r.Cookie("subbridge_session")
	if err != nil {
		return adminSession{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := sessionKey(cookie.Value)
	session, ok := a.sessions[key]
	if !ok || time.Now().After(session.Expires) {
		delete(a.sessions, key)
		return adminSession{}, false
	}
	return session, true
}
func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	return readJSONLimit(w, r, value, 65536)
}
func readJSONLimit(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("некорректные поля JSON или превышен размер запроса")
	}
	return nil
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host && (u.Scheme == "https" || u.Scheme == "http")
}
func englishRequest(r *http.Request) bool {
	return !strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "ru")
}
func publicIssue(r *http.Request, message string) string {
	if !englishRequest(r) || message == "" {
		return message
	}
	switch {
	case strings.Contains(message, "попыток входа"):
		return "Too many sign-in attempts. Try again in five minutes."
	case strings.Contains(message, "HTTP"):
		status := httpStatusPattern.FindString(message)
		return "The subscription source returned " + status + ". The last good cache is preserved."
	case strings.Contains(message, "HWID"):
		return "Device identity was rejected. Check the provider's device binding or enter a working HWID."
	case strings.Contains(message, "порт") || strings.Contains(message, "IP"):
		return "The IP address is unavailable or the port is in use. Check the network settings."
	case strings.Contains(message, "пароль") || strings.Contains(message, "логин"):
		return "Check the username and password. Management passwords must contain 8–1024 characters."
	case strings.Contains(message, "интеграция выключена"):
		return "The 3x-ui integration is disabled. Enable it and save the integration before synchronizing."
	case strings.Contains(message, "сначала сохраните"):
		return "Save the 3x-ui connection and recipients before applying a selection."
	case strings.Contains(message, "выбранные конфиги недоступны"):
		return "Selected configurations are unavailable. Reload the list and check your selection. Existing panel entries were preserved."
	case strings.Contains(message, "3x-ui") || strings.Contains(message, "панели"):
		return "Check the 3x-ui connection settings, credentials and selected clients."
	case strings.Contains(message, "JSON"):
		return "Invalid JSON fields or request exceeds its size limit."
	case strings.Contains(message, "выбранных конфигов"):
		return "Choose up to 4096 individual configurations, or select whole subscriptions."
	case strings.Contains(message, "1–20"):
		return "HWID attempts must be between 1 and 20."
	default:
		return "The operation failed. Check the settings and source response. The last good cache is preserved."
	}
}
func apiError(w http.ResponseWriter, r *http.Request, status int, err error) {
	jsonResponse(w, status, map[string]any{"error": publicIssue(r, err.Error())})
}

func (a *adminServer) login(w http.ResponseWriter, r *http.Request, m managementConfig) {
	if r.Method != http.MethodPost || !sameOrigin(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	now := time.Now()
	for key, bucket := range a.limits {
		if now.After(bucket.Expires) {
			delete(a.limits, key)
		}
	}
	bucket := a.limits[host]
	if now.After(bucket.Expires) {
		bucket = loginLimit{Expires: now.Add(5 * time.Minute)}
	}
	if bucket.Count >= 10 || len(a.limits) >= 1024 {
		a.mu.Unlock()
		apiError(w, r, 429, errors.New("слишком много попыток входа"))
		return
	}
	bucket.Count++
	a.limits[host] = bucket
	a.mu.Unlock()
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &request); err != nil {
		apiError(w, r, 400, err)
		return
	}
	// Derive the password even for an unknown username to avoid a cheap oracle.
	passwordOK := verifyPassword(m.PasswordHash, request.Password)
	if subtle.ConstantTimeCompare([]byte(request.Username), []byte(m.Username)) != 1 || !passwordOK {
		apiError(w, r, 401, errors.New("неверный логин или пароль"))
		return
	}
	token, err := randomSecret()
	if err != nil {
		apiError(w, r, 500, err)
		return
	}
	csrf, err := randomSecret()
	if err != nil {
		apiError(w, r, 500, err)
		return
	}
	session := adminSession{csrf, now.Add(12 * time.Hour)}
	a.mu.Lock()
	if a.config.Username != m.Username || a.config.PasswordHash != m.PasswordHash {
		a.mu.Unlock()
		apiError(w, r, 401, errors.New("учётные данные изменились, повторите вход"))
		return
	}
	delete(a.limits, host)
	for key, old := range a.sessions {
		if now.After(old.Expires) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 128 {
		a.mu.Unlock()
		apiError(w, r, 429, errors.New("слишком много сессий"))
		return
	}
	a.sessions[sessionKey(token)] = session
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "subbridge_session", Value: token, Path: m.Path, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 12 * 60 * 60})
	jsonResponse(w, 200, map[string]any{"authenticated": true, "csrf": csrf})
}

func (a *adminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
	m := a.configuration()
	if !m.Enabled {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == strings.TrimSuffix(m.Path, "/") && m.Path != "/" {
		http.Redirect(w, r, m.Path, 303)
		return
	}
	if !strings.HasPrefix(r.URL.Path, m.Path) {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, m.Path)
	if path == "api/login" {
		a.login(w, r, m)
		return
	}
	session, authenticated := a.authenticate(r)
	if path == "api/session" && r.Method == http.MethodGet {
		jsonResponse(w, 200, map[string]any{"authenticated": authenticated, "csrf": session.CSRF, "version": version})
		return
	}
	if strings.HasPrefix(path, "api/") {
		if !authenticated {
			apiError(w, r, 401, errors.New("требуется вход в панель"))
			return
		}
		if r.Method != http.MethodGet && (r.Method != http.MethodPost || !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRF)) != 1) {
			http.Error(w, "forbidden", 403)
			return
		}
		a.api(w, r, path, m)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	if path == "index.html" {
		http.Redirect(w, r, m.Path, http.StatusSeeOther)
		return
	}
	switch path {
	case "", "app.js", "multi-select.js", "config-picker.js", "panel-presence.js", "i18n.js", "style.css", "InterVariable.woff2":
	default:
		http.NotFound(w, r)
		return
	}
	assets, _ := fs.Sub(webFiles, "ui")
	r2 := r.Clone(r.Context())
	r2.URL = &url.URL{Path: "/" + path}
	http.FileServer(http.FS(assets)).ServeHTTP(w, r2)
}

func mergePanelSecrets(panel, previous *panelConfig, password, token string) {
	panel.Password, panel.APIToken = "", ""
	if previous != nil {
		panel.Password, panel.APIToken = previous.Password, previous.APIToken
	}
	if password != "" {
		panel.Password = password
	}
	if token != "" {
		panel.APIToken = token
	}
}

func (a *adminServer) api(w http.ResponseWriter, r *http.Request, path string, m managementConfig) {
	b := a.engine.current.Load()
	switch path {
	case "api/panel-options":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var request struct {
			Panel    panelConfig `json:"panel"`
			Password string      `json:"password"`
			Token    string      `json:"token"`
		}
		if err := readJSON(w, r, &request); err != nil {
			apiError(w, r, 400, err)
			return
		}
		mergePanelSecrets(&request.Panel, b.config.Panel, request.Password, request.Token)
		// Discovery does not save the draft or require a client selection yet.
		enabled := true
		request.Panel.Enabled, request.Panel.AllClients = &enabled, true
		request.Panel.Emails, request.Panel.InboundIDs = nil, nil
		request.Panel.ConfigSelection = nil
		draft := b.config
		draft.Sources = append([]subscriptionConfig(nil), b.config.Sources...)
		draft.Panel = &request.Panel
		if err := draft.validate(filepath.Dir(a.engine.path)); err != nil {
			apiError(w, r, 400, err)
			return
		}
		client, err := newPanel(request.Panel)
		if err != nil {
			apiError(w, r, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		options, err := client.options(ctx)
		if err != nil {
			apiError(w, r, 502, err)
			return
		}
		jsonResponse(w, 200, options)
	case "api/logout":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		cookie, _ := r.Cookie("subbridge_session")
		a.mu.Lock()
		delete(a.sessions, sessionKey(cookie.Value))
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "subbridge_session", Path: m.Path, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonResponse(w, 200, map[string]bool{"ok": true})
	case "api/configurations":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		groups := b.configurationGroups()
		for i := range groups {
			groups[i].Issue = publicIssue(r, groups[i].Issue)
		}
		jsonResponse(w, 200, map[string]any{"groups": groups})
	case "api/panel-state":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		presence, err := b.observePanel(ctx, r.URL.Query().Get("refresh") == "1")
		if err != nil {
			apiError(w, r, 502, err)
			return
		}
		jsonResponse(w, 200, presence)
	case "api/config-selection":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var update struct {
			Selection json.RawMessage        `json:"selection"`
			Changes   *[]configurationChange `json:"changes"`
		}
		if err := readJSONLimit(w, r, &update, 1<<20); err != nil {
			apiError(w, r, 400, err)
			return
		}
		var selection *configurationSelection
		decoder := json.NewDecoder(strings.NewReader(string(update.Selection)))
		decoder.DisallowUnknownFields()
		if len(update.Selection) == 0 || decoder.Decode(&selection) != nil {
			apiError(w, r, 400, errors.New("JSON: явно укажите выбор конфигов"))
			return
		}
		if err := a.engine.applySelection(selection, update.Changes); err != nil {
			apiError(w, r, 400, err)
			return
		}
		jsonResponse(w, 202, map[string]bool{"ok": true, "queued": true})
	case "api/state":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		cached, issue := b.snapshot()
		counts := map[string]int{}
		for _, link := range cached.Links {
			scheme, _, _ := strings.Cut(link, "://")
			counts[scheme]++
		}
		b.mu.RLock()
		identity, attempts, activity, syncIssue := b.identity, b.lastAttempts, b.activity, b.syncIssue
		lastSync, syncedNodes, syncedClients := b.lastSync, b.syncedNodes, b.syncedClients
		b.mu.RUnlock()
		status := "ok"
		if !b.hasSource() {
			status = "unconfigured"
		} else if len(cached.Links) == 0 {
			status = "unavailable"
		} else if issue != "" {
			status = "stale"
		}
		warnings := cached.Warnings
		if englishRequest(r) && len(warnings) > 0 {
			warnings = []string{"Some JSON settings cannot be represented in standalone URIs. Keep the original JSON for routing and transport details."}
		}
		jsonResponse(w, 200, map[string]any{"status": status, "nodes": len(cached.Links), "protocols": counts, "last_success": cached.UpdatedAt, "issue": publicIssue(r, issue), "sync_issue": publicIssue(r, syncIssue), "last_sync": lastSync, "synced_nodes": syncedNodes, "synced_clients": syncedClients, "panel_enabled": panelEnabled(b.config.Panel), "warnings": warnings, "subscription_url": b.subscriptionURL(), "hwid": identity, "hwid_auto": b.config.Source.HWID == "", "attempts": attempts, "activity": activity, "version": version, "logs": a.engine.logs.snapshot()})
	case "api/config":
		if r.Method == http.MethodGet {
			c := b.config
			passwordSet, tokenSet := false, false
			if c.Panel != nil {
				clone := *c.Panel
				c.Panel = &clone
				passwordSet = clone.Password != ""
				tokenSet = clone.APIToken != ""
				c.Panel.Password = ""
				c.Panel.APIToken = ""
			}
			jsonResponse(w, 200, map[string]any{"config": c, "password_set": passwordSet, "token_set": tokenSet})
			return
		}
		var update struct {
			Config   config `json:"config"`
			Password string `json:"password"`
			Token    string `json:"token"`
		}
		if err := readJSONLimit(w, r, &update, 1<<20); err != nil {
			apiError(w, r, 400, err)
			return
		}
		if update.Config.Panel != nil {
			mergePanelSecrets(update.Config.Panel, b.config.Panel, update.Password, update.Token)
		}
		if err := a.engine.apply(update.Config, true); err != nil {
			apiError(w, r, 400, err)
			return
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
	case "api/management":
		if r.Method == http.MethodGet {
			m.PasswordHash = ""
			jsonResponse(w, 200, m)
			return
		}
		var update struct {
			Config   managementConfig `json:"config"`
			Password string           `json:"password"`
		}
		if err := readJSON(w, r, &update); err != nil {
			apiError(w, r, 400, err)
			return
		}
		update.Config.PasswordHash = m.PasswordHash
		if update.Password != "" {
			hash, err := hashPassword(update.Password)
			if err != nil {
				apiError(w, r, 400, err)
				return
			}
			update.Config.PasswordHash = hash
		}
		changed := update.Config.PasswordHash != m.PasswordHash || update.Config.Username != m.Username
		if err := a.apply(update.Config, true); err != nil {
			apiError(w, r, 400, err)
			return
		}
		if changed {
			if err := os.Remove(filepath.Join(filepath.Dir(a.path), "setup-credentials.txt")); err != nil && !errors.Is(err, os.ErrNotExist) {
				a.engine.logger.Print("не удалось удалить устаревший файл первоначальных данных входа")
			}
		}
		jsonResponse(w, 200, map[string]any{"ok": true, "credentials_changed": changed, "address_changed": m.IP != update.Config.IP || m.Port != update.Config.Port || m.Path != update.Config.Path, "path": update.Config.Path, "port": update.Config.Port})
	case "api/refresh", "api/sync":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		action := strings.TrimPrefix(path, "api/")
		if action == "sync" && !panelEnabled(b.config.Panel) {
			apiError(w, r, 409, errors.New("интеграция выключена: включите и сохраните интеграцию 3x-ui перед синхронизацией"))
			return
		}
		if !a.engine.trigger(action) {
			jsonResponse(w, 202, map[string]bool{"queued": true})
			return
		}
		jsonResponse(w, 202, map[string]bool{"queued": true})
	case "api/download":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		mode := first(r.URL.Query().Get("profile"), b.config.ExportProfile)
		protocol := first(r.URL.Query().Get("protocol"), "all")
		if !validProfile(mode) || (protocol != "all" && protocol != "vless") {
			http.Error(w, "invalid profile", 400)
			return
		}
		cached, _ := b.snapshot()
		if id := r.URL.Query().Get("source"); id != "" {
			member := b.sourceByID(id)
			if member == nil {
				http.NotFound(w, r)
				return
			}
			cached, _ = member.sourceSnapshot()
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="subbridge-configs.txt"`)
		_, _ = io.WriteString(w, strings.Join(exportLinks(cached.Links, mode, protocol), "\n")+"\n")
	default:
		http.NotFound(w, r)
	}
}
