package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPanelConfigurationSelectionAndScopedDelivery(t *testing.T) {
	var secondaryBody atomic.Value
	secondaryBody.Store(secondLink + "\n" + testLink)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/primary" {
			_, _ = io.WriteString(w, testLink)
			return
		}
		_, _ = io.WriteString(w, secondaryBody.Load().(string))
	}))
	defer upstream.Close()
	directory := t.TempDir()
	c := config{DataDir: filepath.Join(directory, "state"), Source: sourceConfig{URL: upstream.URL + "/primary"}, Sources: []subscriptionConfig{{ID: "secondary", Name: "Other", Source: sourceConfig{URL: upstream.URL + "/secondary"}}}}
	if err := c.validate(directory); err != nil {
		t.Fatal(err)
	}
	b, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	read := func(b *bridge, address string) (int, string) {
		t.Helper()
		out := httptest.NewRecorder()
		b.ServeHTTP(out, httptest.NewRequest("GET", address, nil))
		return out.Code, out.Body.String()
	}
	panelURL := b.panelSubscriptionURL()
	if panelURL == b.subscriptionURL() || panelURL == b.configurationGroups()[0].SubscriptionURL {
		t.Fatal("panel token is not isolated")
	}
	if status, body := read(b, panelURL); status != 200 || body != testLink+"\n"+secondLink+"\n" {
		t.Fatal("legacy all-source behavior changed", status)
	}
	disabled := false
	selection := &configurationSelection{Nodes: []configurationReference{{"secondary", configurationID(secondLink)}}}
	c.Panel = &panelConfig{Enabled: &disabled, ConfigSelection: selection}
	b, err = newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if b.panelSubscriptionURL() != panelURL {
		t.Fatal("saving selection rotated panel URL")
	}
	if status, body := read(b, panelURL); status != 200 || body != secondLink+"\n" {
		t.Fatal("selection not applied", status, body)
	}
	if status, body := read(b, b.subscriptionURL()); status != 200 || body != testLink+"\n"+secondLink+"\n" {
		t.Fatal("panel selection affected main subscription")
	}
	for _, query := range []string{"?source=default", "?source=secondary"} {
		if status, _ := read(b, panelURL+query); status != 404 {
			t.Fatal("panel token escaped selection", status)
		}
	}
	if status, body := read(b, panelURL+"?format=base64"); status != 200 || body != base64.StdEncoding.EncodeToString([]byte(secondLink+"\n")) {
		t.Fatal("selected Base64 export failed")
	}
	c.ExportProfile = "v2rayn"
	c.Panel = &panelConfig{Enabled: &disabled, ConfigSelection: &configurationSelection{Nodes: []configurationReference{{"default", configurationID(testLink)}}}}
	b, err = newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := read(b, panelURL); status != 200 || body != strings.Replace(testLink, "fp=qq", "fp=firefox", 1)+"\n" {
		t.Fatal("profile change lost configuration identity")
	}
	if b.configurationGroups()[0].Nodes[0].ID != configurationID(testLink) {
		t.Fatal("UI identity differs from filter")
	}
	c.ExportProfile = "original"
	c.Panel = &panelConfig{Enabled: &disabled, ConfigSelection: &configurationSelection{Sources: []string{"secondary"}, Nodes: []configurationReference{{"default", configurationID(testLink)}}}}
	b, err = newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	third := strings.Replace(secondLink, "#", "#New-", 1)
	secondaryBody.Store(secondLink + "\n" + testLink + "\n" + third)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status, body := read(b, panelURL); status != 200 || body != testLink+"\n"+secondLink+"\n"+third+"\n" {
		t.Fatal("whole source did not include future configs or deduplicate", status)
	}
	c.Panel = &panelConfig{Enabled: &disabled, ConfigSelection: &configurationSelection{Nodes: []configurationReference{{"secondary", configurationID(secondLink)}}}}
	secondaryBody.Store(testLink)
	b, err = newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status, body := read(b, panelURL); status != 200 || strings.TrimSpace(body) != "" {
		t.Fatal("missing selection leaked other nodes", status)
	}
	c.Panel = &panelConfig{Enabled: &disabled, ConfigSelection: &configurationSelection{}}
	b, err = newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := read(b, panelURL); status != 200 || strings.TrimSpace(body) != "" {
		t.Fatal("empty selection fell back to all", status)
	}
}

func TestConfigurationSelectionValidationAndLargeAdminSave(t *testing.T) {
	for _, selection := range []*configurationSelection{
		{Sources: []string{"../outside"}}, {Sources: []string{"all"}},
		{Nodes: []configurationReference{{"default", "bad"}}},
		{Nodes: []configurationReference{{"a/b", configurationID(testLink)}}},
		{Nodes: []configurationReference{{"default", strings.ToUpper(configurationID(testLink))}}},
	} {
		c := config{Panel: &panelConfig{Enabled: new(bool), ConfigSelection: selection}}
		if c.validate(t.TempDir()) == nil {
			t.Fatal("invalid disabled selection accepted")
		}
	}
	a, client, base := adminFixture(t)
	c := a.engine.current.Load().config
	c.Panel = &panelConfig{Enabled: new(bool), ConfigSelection: &configurationSelection{Sources: []string{}, Nodes: []configurationReference{}}}
	for i := 0; i < 600; i++ {
		c.Panel.ConfigSelection.Nodes = append(c.Panel.ConfigSelection.Nodes, configurationReference{"default", contentID(fmt.Sprint(i))})
	}
	csrf := adminLogin(t, client, base, "synthetic-password")
	if status, body := adminRequest(t, client, base, "config", csrf, map[string]any{"config": c}); status != 200 {
		t.Fatal("large selection rejected", status, string(body))
	}
	loaded, err := loadConfig(a.engine.path)
	if err != nil || !reflect.DeepEqual(loaded.Panel.ConfigSelection, c.Panel.ConfigSelection) {
		t.Fatal("selection not persisted", err)
	}
}

func TestPanelMigratesManagedSourceAndUpdatesPrefix(t *testing.T) {
	legacy, target := "https://example.invalid/sub/old", "https://example.invalid/sub/filtered"
	untouched := map[string]any{"kind": "link", "value": secondLink, "remark": "Other", "enable": true, "futureField": "keep"}
	managed := map[string]any{"id": float64(7), "kind": "subscription", "value": legacy, "remark": "SubBridge", "enable": false, "expiryTime": float64(123), "namePrefix": "Old ", "futureField": "keep"}
	panel := &mockPanel{clients: []*mockClient{{email: "admin", external: []map[string]any{untouched, managed}}, {email: "unselected"}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	p, err := newPanel(panelConfig{URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"admin"}, NamePrefix: "[LTE] "})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.syncMigrating(context.Background(), target, legacy, true)
	if err != nil || result.Updated != 1 || panel.posts != 0 {
		t.Fatal("migration dry run failed", result, err)
	}
	result, err = p.syncMigrating(context.Background(), target, legacy, false)
	if err != nil || result.Updated != 1 || panel.posts != 1 {
		t.Fatal("migration failed", result, err)
	}
	rows := panel.clients[0].external
	if len(rows) != 2 || !reflect.DeepEqual(rows[0], untouched) || rows[1]["value"] != target || rows[1]["enable"] != false || rows[1]["futureField"] != "keep" || rows[1]["namePrefix"] != "[LTE] " || len(panel.clients[1].external) != 0 {
		t.Fatal("migration altered unrelated settings", rows)
	}
	result, err = p.syncMigrating(context.Background(), target, legacy, false)
	if err != nil || result.Unchanged != 1 || panel.posts != 1 {
		t.Fatal("migration not idempotent", result, err)
	}
	p.config.NamePrefix = "[New] "
	result, err = p.syncMigrating(context.Background(), target, legacy, false)
	if err != nil || result.Updated != 1 || panel.posts != 2 || panel.clients[0].external[1]["namePrefix"] != "[New] " {
		t.Fatal("existing prefix did not update", result, err)
	}
}

func TestSelectionFollowsCredentialRefresh(t *testing.T) {
	b, _, provider := makeTestBridge(t)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := testLink
	sum := sha256.Sum256([]byte(original))
	legacy := hex.EncodeToString(sum[:])
	c := b.config
	c.Panel = &panelConfig{Enabled: new(bool), ConfigSelection: &configurationSelection{Nodes: []configurationReference{{"default", legacy}}}}
	b, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(original)
	u.User = url.User("00000000-0000-4000-8000-000000000099")
	u.Host = "192.0.2.99:" + u.Port()
	q := u.Query()
	q.Set("pbk", "rotated-key")
	u.RawQuery = q.Encode()
	changed := u.String()
	provider.set(changed, 200)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	selected, _ := b.panelSnapshot()
	if len(selected.Links) != 1 || selected.Links[0] != changed {
		t.Fatal("selected node disappeared after provider changed IP, credentials or query order")
	}
}

func TestSelectionDistinguishesSameNamedEndpoints(t *testing.T) {
	b, _, provider := makeTestBridge(t)
	u, err := url.Parse(testLink)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "192.0.2.99:" + u.Port()
	other := u.String()
	provider.set(testLink+"\n"+other, 200)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes := b.configurationGroups()[0].Nodes
	if len(nodes) != 2 || nodes[0].ID == nodes[1].ID {
		t.Fatal("same named endpoints cannot be selected separately")
	}
	c := b.config
	c.Panel = &panelConfig{Enabled: new(bool), ConfigSelection: &configurationSelection{Nodes: []configurationReference{{"default", nodes[0].ID}}}}
	b, err = newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := b.panelSnapshot()
	if len(selected.Links) != 1 || selected.Links[0] != testLink {
		t.Fatal("selecting one endpoint included another endpoint")
	}
}

func TestStartupPersistsMigratedSelection(t *testing.T) {
	b, _, _ := makeTestBridge(t)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := b.config
	c.Listen = availableAddress(t)
	c.Panel = &panelConfig{Enabled: new(bool), ConfigSelection: &configurationSelection{Nodes: []configurationReference{{"default", contentID(testLink)}}}}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := writePrivateJSON(path, c); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(context.Background(), path, c, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	saved, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Panel.ConfigSelection.Nodes[0].NodeID != configurationID(testLink) || c.Panel.ConfigSelection.Nodes[0].NodeID != contentID(testLink) {
		t.Fatal("migrated selection was not saved independently of the input")
	}
}
