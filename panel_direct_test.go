package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSyncPanelWorksWithoutInboundReachability(t *testing.T) {
	b, _, _ := makeTestBridge(t)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := b.config
	c.PublicURL = "http://127.0.0.1:1"
	panel := &mockPanel{clients: []*mockClient{{email: "Admin"}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	c.Panel = &panelConfig{URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}}
	if err := c.validate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
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
	rows := panel.clients[0].external
	if len(rows) != 2 || rows[0]["kind"] != "link" || rows[1]["kind"] != "link" || strings.SplitN(rows[0]["value"].(string), "#", 2)[0] != strings.SplitN(testLink, "#", 2)[0] || strings.SplitN(rows[1]["value"].(string), "#", 2)[0] != strings.SplitN(secondLink, "#", 2)[0] {
		t.Fatalf("Admin received an unreachable subscription URL instead of two direct configurations (rows=%d)", len(rows))
	}
}

func TestDirectDeliveryPreservesOtherEntriesAndReconcilesSelection(t *testing.T) {
	otherValue, err := directNodeValue(testLink, "other-instance")
	if err != nil {
		t.Fatal(err)
	}
	foreign := map[string]any{"kind": "link", "value": otherValue, "remark": "Other instance", "enable": false, "expiryTime": float64(123), "futureField": "keep"}
	manual := map[string]any{"kind": "subscription", "value": "https://example.invalid/manual", "remark": "Manual", "enable": true}
	panel := &mockPanel{clients: []*mockClient{{email: "Admin", external: []map[string]any{foreign, manual}}, {email: "Other"}}}
	server := httptest.NewServer(panel)
	defer server.Close()
	p, err := newPanel(panelConfig{URL: server.URL + "/secret-base", AuthMode: "token", APIToken: "synthetic-api-token", Emails: []string{"Admin"}, NamePrefix: "[LTE] "})
	if err != nil {
		t.Fatal(err)
	}
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"Original VMess","id":"00000000-0000-4000-8000-000000000001","add":"vmess.example","port":"443","opaque":{"must":"survive"}}`))
	links := []string{testLink, secondLink, vmess, testLink}
	result, err := p.syncDirect(context.Background(), links, "my-instance", nil, true)
	if err != nil || result.Added != 1 || panel.posts != 0 {
		t.Fatal("dry run changed remote", result, err)
	}
	result, err = p.syncDirect(context.Background(), links, "my-instance", nil, false)
	if err != nil || result.Added != 1 || panel.posts != 1 {
		t.Fatal("direct delivery failed", result, err)
	}
	rows := panel.clients[0].external
	if len(rows) != 5 || !reflect.DeepEqual(rows[0], foreign) || !reflect.DeepEqual(rows[1], manual) || len(panel.clients[1].external) != 0 {
		t.Fatal("unrelated entries changed")
	}
	for _, row := range rows[2:] {
		if !ownedDirectValue(row["value"].(string), "my-instance") || ownedDirectValue(row["value"].(string), "other-instance") {
			t.Fatal("ownership scope escaped")
		}
	}
	body, err := decodeBase64(strings.TrimPrefix(rows[4]["value"].(string), "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if json.Unmarshal(body, &decoded) != nil || string(decoded["opaque"]) != `{"must":"survive"}` || rows[4]["remark"] != "[LTE] Original VMess" {
		t.Fatal("VMess settings or display name changed")
	}
	result, err = p.syncDirect(context.Background(), links, "my-instance", nil, false)
	if err != nil || result.Unchanged != 1 || panel.posts != 1 {
		t.Fatal("repeat sync created duplicates", result, err)
	}
	panel.clients[0].external[2]["enable"] = false
	panel.clients[0].external[2]["futureField"] = "preserve"
	p.config.NamePrefix = "[New] "
	result, err = p.syncDirect(context.Background(), links, "my-instance", nil, false)
	if err != nil || result.Updated != 1 || panel.posts != 2 || panel.clients[0].external[2]["enable"] != false || panel.clients[0].external[2]["futureField"] != "preserve" || !strings.HasPrefix(panel.clients[0].external[2]["remark"].(string), "[New] ") {
		t.Fatal("prefix update lost manual settings", result, err)
	}
	result, err = p.syncDirect(context.Background(), []string{vmess}, "my-instance", nil, false)
	if err != nil || result.Updated != 1 || len(panel.clients[0].external) != 3 {
		t.Fatal("removed selection stayed in subscription", result, err)
	}
	result, err = p.syncSubscription(context.Background(), "https://example.invalid/filtered", "https://example.invalid/combined", "my-instance", false)
	if err != nil || result.Added != 1 || len(panel.clients[0].external) != 3 || panel.clients[0].external[2]["kind"] != "subscription" {
		t.Fatal("switch to subscription left stale direct entries", result, err)
	}
	result, err = p.syncDirect(context.Background(), []string{testLink}, "my-instance", []string{"https://example.invalid/filtered"}, false)
	if err != nil || result.Added != 1 || len(panel.clients[0].external) != 3 {
		t.Fatal("subscription migration failed", result, err)
	}
	result, err = p.syncDirect(context.Background(), nil, "my-instance", nil, false)
	if err != nil || result.Updated != 1 || len(panel.clients[0].external) != 2 || !reflect.DeepEqual(panel.clients[0].external[0], foreign) {
		t.Fatal("empty selection did not remove only managed entries", result, err)
	}
}

func TestSyncRejectsDisabledIntegration(t *testing.T) {
	_, client, base := adminFixture(t)
	csrf := adminLogin(t, client, base, "synthetic-password")
	if status, _ := adminRequest(t, client, base, "sync", csrf, map[string]any{}); status != 409 {
		t.Fatalf("disabled integration reported synchronization queued: HTTP %d", status)
	}
}

func TestDirectDeliverySharesIdenticalConfigsAcrossInstallations(t *testing.T) {
	first, _, _ := makeTestBridge(t)
	second, _, _ := makeTestBridge(t)
	for _, b := range []*bridge{first, second} {
		if err := b.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
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
	if len(panel.clients[0].external) != 2 || panel.posts != 1 {
		t.Fatal("independent installations duplicated identical configs")
	}
}
