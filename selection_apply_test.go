package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestApplySelectionRemovesManagedConfigWithAutoSyncDisabled(t *testing.T) {
	a, client, base := adminFixture(t)
	providerBridge, _, _ := makeTestBridge(t)
	c := a.engine.current.Load().config
	owner := contentID(a.engine.current.Load().token)[:16]
	value, err := directNodeValue(testLink, owner)
	if err != nil {
		t.Fatal(err)
	}
	manual := map[string]any{"kind": "link", "value": secondLink, "remark": "Manual", "enable": false}
	panel := &mockPanel{clients: []*mockClient{{email: "Admin", inbounds: []int{7}, external: []map[string]any{manual, {"kind": "link", "value": value, "remark": "Test", "enable": true}}}, {email: "Other", external: []map[string]any{manual}}}}
	var reject atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		panel.ServeHTTP(w, r)
	}))
	defer server.Close()
	c.Source = providerBridge.config.Source
	c.Panel = &panelConfig{Enabled: new(bool), URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}, ConfigSelection: &configurationSelection{Nodes: []configurationReference{{"default", configurationID(testLink)}}}}
	if err := a.engine.apply(c, true); err != nil {
		t.Fatal(err)
	}
	if err := a.engine.current.Load().refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	csrf := adminLogin(t, client, base, "synthetic-password")
	empty := &configurationSelection{Sources: []string{}, Nodes: []configurationReference{}}
	reject.Store(true)
	if status, _ := adminRequest(t, client, base, "config-selection", csrf, map[string]any{"selection": empty}); status != 202 {
		t.Fatalf("apply selection did not queue synchronization: HTTP %d", status)
	}
	failedDeadline := time.Now().Add(5 * time.Second)
	for {
		b := a.engine.current.Load()
		b.mu.RLock()
		issue := b.syncIssue
		b.mu.RUnlock()
		if issue != "" {
			break
		}
		if time.Now().After(failedDeadline) {
			t.Fatal("failed synchronization was not reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
	panel.mu.Lock()
	unchanged := len(panel.clients[0].external) == 2 && panel.posts == 0
	panel.mu.Unlock()
	if !unchanged {
		t.Fatal("failed synchronization changed remote entries")
	}
	reject.Store(false)
	if status, _ := adminRequest(t, client, base, "config-selection", csrf, map[string]any{"selection": empty}); status != 202 {
		t.Fatalf("apply selection did not queue a one-time sync: HTTP %d", status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b := a.engine.current.Load()
		b.mu.RLock()
		done, issue, count := !b.lastSync.IsZero(), b.syncIssue, b.syncedNodes
		b.mu.RUnlock()
		if issue != "" {
			t.Fatal(issue)
		}
		if done {
			if count != 0 {
				t.Fatal("empty selection included unselected configs")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("one-time synchronization did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	panel.mu.Lock()
	if len(panel.clients[0].external) != 1 || !reflect.DeepEqual(panel.clients[0].external[0], manual) || !reflect.DeepEqual(panel.clients[0].inbounds, []int{7}) || len(panel.clients[1].external) != 1 || panel.posts != 1 {
		panel.mu.Unlock()
		t.Fatal("removal changed unrelated configs, clients or inbound attachments")
	}
	panel.mu.Unlock()
	saved, err := loadConfig(a.engine.path)
	if err != nil || panelEnabled(saved.Panel) || saved.Panel.ConfigSelection == nil || len(saved.Panel.ConfigSelection.Nodes) != 0 || saved.Panel.APIToken != "synthetic-api-token" {
		t.Fatal("selection did not persist independently of automatic synchronization", err)
	}
	status, body := adminRequest(t, client, base, "configurations", csrf, nil)
	var groups struct {
		Groups []configurationGroup `json:"groups"`
	}
	if status != 200 || json.Unmarshal(body, &groups) != nil || len(groups.Groups[0].Nodes) != 2 {
		t.Fatal("removal changed the source configurations")
	}
	if status, _ := adminRequest(t, client, base, "config-selection", csrf, map[string]any{}); status != 400 {
		t.Fatal("missing selection was accepted as an all-node selection", status)
	}
}

func TestEmptySelectionRemovesManagedSubscription(t *testing.T) {
	b, _, _ := makeTestBridge(t)
	panel := &mockPanel{clients: []*mockClient{{email: "Admin", external: []map[string]any{{"kind": "subscription", "value": b.panelSubscriptionURL(), "remark": "SubBridge", "enable": true}}}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	c := b.config
	c.Panel = &panelConfig{Enabled: new(bool), URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}, DeliveryMode: "subscription", ConfigSelection: &configurationSelection{}}
	b, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPanel(*c.Panel)
	if err != nil {
		t.Fatal(err)
	}
	if err := syncPanel(context.Background(), p, b, false, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.clients[0].external) != 0 {
		t.Fatal("empty selection left its subscription registered")
	}
}
