package main

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedSubscriptionsDeduplicateAcrossInstallations(t *testing.T) {
	first, _, _ := makeTestBridge(t)
	second, _, _ := makeTestBridge(t)
	for _, b := range []*bridge{first, second} {
		if err := b.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		b.config.Panel = &panelConfig{ConfigSelection: &configurationSelection{Nodes: []configurationReference{{"default", configurationID(testLink)}}}}
	}
	panel := &mockPanel{clients: []*mockClient{{email: "Admin"}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	p, err := newPanel(panelConfig{URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []*bridge{first, second, first} {
		if err := syncPanel(context.Background(), p, b, false, log.New(io.Discard, "", 0)); err != nil {
			t.Fatal(err)
		}
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.clients[0].external) != 1 || panel.posts != 1 {
		t.Fatalf("identical config was duplicated by another SubBridge: entries=%d writes=%d", len(panel.clients[0].external), panel.posts)
	}
}

func TestConnectingNewInstancePreservesRemoteSelection(t *testing.T) {
	a, _, _ := adminFixture(t)
	providerBridge, _, _ := makeTestBridge(t)
	value, err := directNodeValue(testLink, "previous-instance")
	if err != nil {
		t.Fatal(err)
	}
	panel := &mockPanel{clients: []*mockClient{{email: "Admin", external: []map[string]any{{"kind": "link", "value": value, "remark": "Existing name", "enable": true}}}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	c := a.engine.current.Load().config
	c.Source = providerBridge.config.Source
	c.Panel = &panelConfig{URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}, ConfigSelection: &configurationSelection{}}
	if err := a.engine.apply(c, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b := a.engine.current.Load()
		b.mu.RLock()
		done := !b.lastSync.IsZero()
		issue := b.syncIssue
		b.mu.RUnlock()
		if issue != "" {
			t.Fatal(issue)
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new instance did not observe the existing subscription")
		}
		time.Sleep(10 * time.Millisecond)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.clients[0].external) != 1 || connectionID(panel.clients[0].external[0]["value"].(string)) != connectionID(testLink) || panel.clients[0].external[0]["remark"] != "Existing name" {
		t.Fatal("connecting a new instance replaced the remote selection with its local empty default")
	}
}

func TestSharedPresenceRemovalAndFreshness(t *testing.T) {
	first, _, provider := makeTestBridge(t)
	if err := first.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := first.config
	c.DataDir = filepath.Join(t.TempDir(), "state")
	second, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	panel := &mockPanel{clients: []*mockClient{{email: "Admin"}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	selection := &configurationSelection{Nodes: []configurationReference{{"default", configurationID(testLink)}}}
	pc := panelConfig{URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}, ConfigSelection: selection, NamePrefix: "[A] "}
	first.config.Panel = &pc
	other := pc
	other.NamePrefix = "[B] "
	second.config.Panel = &other
	p, err := newPanel(pc)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := newPanel(other)
	if err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	check := func(b *bridge, want int) {
		t.Helper()
		status, err := b.observePanel(context.Background(), true)
		if err != nil || !status.Available || status.Clients != 1 {
			t.Fatal("native panel observation failed", err)
		}
		for _, node := range status.Nodes {
			if node.NodeID == configurationID(testLink) {
				if node.Present != want {
					t.Fatalf("remote presence=%d, want %d", node.Present, want)
				}
				return
			}
		}
		t.Fatal("configuration missing from observed catalog")
	}
	check(second, 0)
	if err := syncPanel(context.Background(), p, first, false, logger); err != nil {
		t.Fatal(err)
	}
	check(second, 1)
	if err := syncPanelMode(context.Background(), p2, second, false, false, logger); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	if panel.posts != 1 {
		panel.mu.Unlock()
		t.Fatal("background sync rewrote another instance's prefix")
	}
	panel.mu.Unlock()
	u, _ := url.Parse(testLink)
	u.User = url.User("00000000-0000-4000-8000-000000000099")
	u.Host = "192.0.2.99:" + u.Port()
	q := u.Query()
	q.Set("pbk", "refreshed-key")
	u.RawQuery = q.Encode()
	provider.set(u.String(), 200)
	if err := second.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := syncPanelMode(context.Background(), p2, second, false, false, logger); err != nil {
		t.Fatal(err)
	}
	if err := syncPanelMode(context.Background(), p, first, false, false, logger); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	if panel.posts != 2 || len(panel.clients[0].external) != 1 || connectionID(panel.clients[0].external[0]["value"].(string)) != connectionID(u.String()) {
		panel.mu.Unlock()
		t.Fatal("stale instance overwrote refreshed connection parameters")
	}
	panel.mu.Unlock()
	first.config.Panel = &panelConfig{ConfigSelection: &configurationSelection{}}
	if err := syncPanel(context.Background(), p, first, false, logger); err != nil {
		t.Fatal(err)
	}
	check(second, 0)
	if err := syncPanelMode(context.Background(), p2, second, false, false, logger); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.clients[0].external) != 0 {
		t.Fatal("automatic sync restored a configuration removed by another instance")
	}
}

func TestSharedApplyOnlyChangesTouchedConfigurations(t *testing.T) {
	b, _, _ := makeTestBridge(t)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner := contentID(b.token)[:16]
	first, err := directNodeValue(testLink, owner)
	if err != nil {
		t.Fatal(err)
	}
	second, err := directNodeValue(secondLink, owner)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]any{"kind": "link", "value": second, "remark": "Added elsewhere after the picker opened", "enable": true, "futureField": "keep"}
	panel := &mockPanel{clients: []*mockClient{{email: "Admin", external: []map[string]any{{"kind": "link", "value": first, "remark": "Remove", "enable": true}, kept}}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	p, err := newPanel(panelConfig{URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}})
	if err != nil {
		t.Fatal(err)
	}
	b.config.Panel = &panelConfig{ConfigSelection: &configurationSelection{}}
	changes := []configurationChange{{configurationReference: configurationReference{"default", configurationID(testLink)}, Selected: false}}
	b.selectionChanges = &changes
	logger := log.New(io.Discard, "", 0)
	if err := syncPanel(context.Background(), p, b, false, logger); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	if len(panel.clients[0].external) != 1 || panel.clients[0].external[0]["value"] != second || panel.clients[0].external[0]["futureField"] != "keep" {
		panel.mu.Unlock()
		t.Fatal("applying one checkbox changed an untouched configuration")
	}
	writes := panel.posts
	panel.mu.Unlock()
	changes = []configurationChange{}
	if err := syncPanel(context.Background(), p, b, false, logger); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if panel.posts != writes {
		t.Fatal("applying an unchanged picker rewrote another instance's entries")
	}
}
