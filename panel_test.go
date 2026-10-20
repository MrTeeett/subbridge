package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type mockClient struct {
	email    string
	inbounds []int
	external []map[string]any
}
type mockPanel struct {
	mu      sync.Mutex
	clients []*mockClient
	posts   int
}

func (p *mockPanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer synthetic-api-token" {
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	reply := func(value any) { _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": value}) }
	path := strings.TrimPrefix(r.URL.Path, "/secret-base/panel/api/clients/")
	if path == "list" {
		rows := make([]map[string]any, 0)
		for _, c := range p.clients {
			rows = append(rows, map[string]any{"email": c.email, "inboundIds": c.inbounds})
		}
		reply(rows)
		return
	}
	for _, c := range p.clients {
		if path == "get/"+c.email {
			reply(map[string]any{"client": map[string]any{"email": c.email, "uuid": "original-id", "totalGB": 1024, "expiryTime": 123456789, "enable": false}, "externalLinks": c.external, "inboundIds": c.inbounds})
			return
		}
		if path == c.email+"/externalLinks" && r.Method == http.MethodPost {
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["externalLinks"] == nil {
				w.WriteHeader(400)
				return
			}
			if json.Unmarshal(body["externalLinks"], &c.external) != nil {
				w.WriteHeader(400)
				return
			}
			p.posts++
			reply(nil)
			return
		}
	}
	w.WriteHeader(404)
}

func TestPanelAppendPreservesClientsAndDiscoversNewOnes(t *testing.T) {
	b, _, _ := makeTestBridge(t)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{"id": float64(1), "kind": "link", "value": secondLink, "remark": "Keep me", "enable": false, "expiryTime": float64(123456), "namePrefix": "old", "futureField": "must-be-preserved"}
	panel := &mockPanel{clients: []*mockClient{{email: "alpha+test@example.invalid", inbounds: []int{3}, external: []map[string]any{existing}}, {email: "other", inbounds: []int{8}}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	t.Setenv("TEST_PANEL_TOKEN", "synthetic-api-token")
	p, err := newPanel(panelConfig{URL: server.URL + "/secret-base", TokenEnv: "TEST_PANEL_TOKEN", InboundIDs: []int{3}, NamePrefix: "External "})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.sync(context.Background(), b.subscriptionURL(), true)
	if err != nil || result.Added != 1 || result.Failed != 0 || panel.posts != 0 {
		t.Fatal("dry-run", result, err)
	}
	result, err = p.sync(context.Background(), b.subscriptionURL(), false)
	if err != nil || result.Added != 1 || result.Failed != 0 {
		t.Fatal("sync", result, err)
	}
	panel.mu.Lock()
	saved := panel.clients[0].external
	posts := panel.posts
	panel.mu.Unlock()
	if posts != 1 || len(saved) != 2 || !reflect.DeepEqual(saved[0], existing) || saved[1]["namePrefix"] != "External " {
		t.Fatal("existing link data overwritten")
	}
	if len(panel.clients[1].external) != 0 {
		t.Fatal("unselected client modified")
	}
	// Simulate 3x-ui fetching the registered external source using its own HWID.
	remote := saved[1]["value"].(string)
	_, body, _ := testGET(t, remote, http.Header{"X-Hwid": {"panel-install-id"}})
	if body != testLink+"\n"+secondLink+"\n" {
		t.Fatal("registered endpoint is not a standard subscription")
	}
	result, err = p.sync(context.Background(), b.subscriptionURL(), false)
	if err != nil || result.Unchanged != 1 || result.Added != 0 || panel.posts != 1 {
		t.Fatal("repeat sync not idempotent")
	}
	panel.mu.Lock()
	panel.clients = append(panel.clients, &mockClient{email: "new-user", inbounds: []int{3}})
	panel.clients[0].external[1]["enable"] = false
	panel.mu.Unlock()
	result, err = p.sync(context.Background(), b.subscriptionURL(), false)
	if err != nil || result.Selected != 2 || result.Added != 1 || result.Unchanged != 1 {
		t.Fatal("new clients not discovered", result, err)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if panel.clients[0].external[1]["enable"] != false {
		t.Fatal("manually disabled subscription reenabled")
	}
}

func TestPanelErrorsAreSanitizedAndTokensNeverRedirected(t *testing.T) {
	t.Setenv("TEST_PANEL_TOKEN", "synthetic-api-token")
	var destinationMu sync.Mutex
	received := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationMu.Lock()
		received++
		destinationMu.Unlock()
		_, _ = io.WriteString(w, "unexpected")
	}))
	defer destination.Close()
	for _, kind := range []string{"redirect", "unauthorized", "raw-error", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "redirect":
					http.Redirect(w, r, destination.URL+"/private-secret", 302)
				case "unauthorized":
					w.WriteHeader(401)
				case "unsupported":
					_, _ = io.WriteString(w, `{"success":true,"obj":{"client":{}}}`)
				default:
					_, _ = io.WriteString(w, `{"success":false,"msg":"private-secret synthetic-api-token"}`)
				}
			}))
			defer srv.Close()
			p, err := newPanel(panelConfig{URL: srv.URL, TokenEnv: "TEST_PANEL_TOKEN", Emails: []string{"test"}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.sync(context.Background(), "https://example.invalid/sub/private-secret", false)
			if err == nil && result.Failed == 0 {
				t.Fatal("unexpected success")
			}
			message := result.Error
			if err != nil {
				message += err.Error()
			}
			if strings.Contains(message, "private-secret") || strings.Contains(message, "synthetic-api-token") || strings.Contains(message, srv.URL) {
				t.Fatal("private API data in error")
			}
		})
	}
	destinationMu.Lock()
	defer destinationMu.Unlock()
	if received != 0 {
		t.Fatal("bearer followed redirect")
	}
}

func TestPanelExplicitEmailAndInboundUnion(t *testing.T) {
	panel := &mockPanel{clients: []*mockClient{{email: "by-email", inbounds: []int{1}}, {email: "by-inbound", inbounds: []int{2}}, {email: "neither", inbounds: []int{3}}}}
	srv := httptest.NewServer(panel)
	defer srv.Close()
	t.Setenv("TEST_PANEL_TOKEN", "synthetic-api-token")
	p, err := newPanel(panelConfig{URL: srv.URL + "/secret-base", TokenEnv: "TEST_PANEL_TOKEN", Emails: []string{"by-email"}, InboundIDs: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := p.selected(context.Background())
	if err != nil || !reflect.DeepEqual(selected, []string{"by-email", "by-inbound"}) {
		t.Fatal("selector union")
	}
}

func TestPasswordSessionCSRFAndRenewal(t *testing.T) {
	t.Setenv("TEST_SESSION_TOKEN", "")
	t.Setenv("TEST_SESSION_USER", "synthetic-admin")
	t.Setenv("TEST_SESSION_PASSWORD", "synthetic-password\n#unicode-Я")
	var mu sync.Mutex
	csrf := "before-login"
	authenticated := false
	expireOnWrite := true
	logins, posts := 0, 0
	var external []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": v}) }
		if r.URL.Path == "/secret/csrf-token" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-session", Path: "/secret/", HttpOnly: true})
			reply(csrf)
			return
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "synthetic-session" {
			w.WriteHeader(401)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-CSRF-Token") != csrf {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/secret/login" {
			var credentials map[string]string
			if json.NewDecoder(r.Body).Decode(&credentials) != nil || credentials["username"] != "synthetic-admin" || credentials["password"] != "synthetic-password\n#unicode-Я" {
				_, _ = io.WriteString(w, `{"success":false,"msg":"private-password"}`)
				return
			}
			logins++
			authenticated = true
			csrf = fmt.Sprintf("after-login-%d", logins)
			reply(nil)
			return
		}
		if !authenticated {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/secret/panel/api/clients/get/test" {
			reply(map[string]any{"externalLinks": external})
			return
		}
		if r.URL.Path == "/secret/panel/api/clients/test/externalLinks" && r.Method == http.MethodPost {
			if expireOnWrite {
				expireOnWrite = false
				authenticated = false
				w.WriteHeader(401)
				return
			}
			var body struct {
				Links []map[string]any `json:"externalLinks"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(400)
				return
			}
			external = body.Links
			posts++
			reply(nil)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	p, err := newPanel(panelConfig{URL: server.URL + "/secret", TokenEnv: "TEST_SESSION_TOKEN", UsernameEnv: "TEST_SESSION_USER", PasswordEnv: "TEST_SESSION_PASSWORD", Emails: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.sync(context.Background(), "https://example.invalid/sub/bridge", false)
	if err != nil || result.Failed != 0 || result.Added != 1 {
		t.Fatal("password/CSRF session sync", result, err)
	}
	mu.Lock()
	firstLogins, firstPosts := logins, posts
	authenticated = false
	mu.Unlock()
	if firstLogins != 2 || firstPosts != 1 {
		t.Fatal("expired write was not renewed exactly once")
	}
	result, err = p.sync(context.Background(), "https://example.invalid/sub/bridge", false)
	if err != nil || result.Unchanged != 1 {
		t.Fatal("expired read not renewed", result, err)
	}
	var readers sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		readers.Go(func() {
			var obj any
			if err := p.request(context.Background(), "get/test", nil, &obj); err != nil {
				failures <- err
			}
		})
	}
	readers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if logins != 3 || posts != 1 {
		t.Fatal("duplicate login or mutation")
	}
}

func TestWrongPasswordDoesNotExposeCredentials(t *testing.T) {
	t.Setenv("TEST_SESSION_TOKEN", "")
	t.Setenv("TEST_SESSION_USER", "synthetic-admin")
	t.Setenv("TEST_SESSION_PASSWORD", "private-password")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/csrf-token") {
			_, _ = io.WriteString(w, `{"success":true,"obj":"synthetic-csrf"}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":false,"msg":"private-password private-secret"}`)
	}))
	defer server.Close()
	p, err := newPanel(panelConfig{URL: server.URL, TokenEnv: "TEST_SESSION_TOKEN", UsernameEnv: "TEST_SESSION_USER", PasswordEnv: "TEST_SESSION_PASSWORD", Emails: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.sync(context.Background(), "https://example.invalid/sub", false)
	message := result.Error
	if err != nil {
		message += err.Error()
	}
	if result.Failed != 1 || strings.Contains(message, "private-password") || strings.Contains(message, "private-secret") {
		t.Fatal("login error was not sanitized")
	}
}
