package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMultipleSubscriptionsKeepIndependentCachesAndIdentities(t *testing.T) {
	var fail atomic.Bool
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-HWID") != "primary-manual-hwid" {
			t.Error("primary identity changed")
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, testLink)
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hwidPattern.MatchString(r.Header.Get("X-HWID")) {
			t.Error("secondary automatic HWID missing")
		}
		_, _ = io.WriteString(w, secondLink+"\n"+testLink)
	}))
	defer secondary.Close()
	directory := t.TempDir()
	c := config{DataDir: filepath.Join(directory, "state"), SourceName: "Primary", Source: sourceConfig{URL: primary.URL, profile: profile{HWID: "primary-manual-hwid"}}, Sources: []subscriptionConfig{{ID: "secondary", Name: "Secondary", Source: sourceConfig{URL: secondary.URL}}}}
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
	cached, issue := b.snapshot()
	if issue != "" || !reflect.DeepEqual(cached.Links, []string{testLink, secondLink}) {
		t.Fatal("combined export must deduplicate without mixing caches", issue, cached.Links)
	}
	groups := b.configurationGroups()
	if len(groups) != 2 || len(groups[0].Nodes) != 1 || len(groups[1].Nodes) != 2 || groups[1].ID != "secondary" || groups[1].HWID == groups[0].HWID {
		t.Fatal("groups or identities mixed", groups)
	}
	identity := groups[1].HWID
	for i, group := range groups {
		if group.SubscriptionURL == b.subscriptionURL() {
			t.Fatal("source export reuses the combined token")
		}
		r := httptest.NewRecorder()
		b.ServeHTTP(r, httptest.NewRequest("GET", group.SubscriptionURL, nil))
		want := testLink + "\n"
		other := "secondary"
		if i == 1 {
			want = secondLink + "\n" + testLink + "\n"
			other = "default"
		}
		if r.Code != 200 || r.Body.String() != want {
			t.Fatal("source token returned another source", r.Code)
		}
		r = httptest.NewRecorder()
		b.ServeHTTP(r, httptest.NewRequest("GET", group.SubscriptionURL+"?source="+other, nil))
		if r.Code != 404 {
			t.Fatal("source token can access another source")
		}
	}
	for id, want := range map[string]string{"default": testLink + "\n", "secondary": secondLink + "\n" + testLink + "\n"} {
		r := httptest.NewRecorder()
		b.ServeHTTP(r, httptest.NewRequest("GET", b.subscriptionURL()+"?source="+id, nil))
		if r.Code != 200 || r.Body.String() != want {
			t.Fatal("source-scoped export", id, r.Code)
		}
	}
	fail.Store(true)
	if err := b.refresh(context.Background()); err == nil {
		t.Fatal("source failure not reported")
	}
	groups = b.configurationGroups()
	if groups[0].Issue == "" || groups[1].Issue != "" || len(groups[0].Nodes) != 1 || len(groups[1].Nodes) != 2 {
		t.Fatal("one source failure affected another source")
	}
	reloaded, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.token != b.token || reloaded.configurationGroups()[1].HWID != identity || reloaded.configurationGroups()[0].SubscriptionURL != groups[0].SubscriptionURL || reloaded.configurationGroups()[1].SubscriptionURL != groups[1].SubscriptionURL {
		t.Fatal("restart changed token or per-source identity")
	}
	if got, _ := reloaded.snapshot(); !reflect.DeepEqual(got.Links, cached.Links) {
		t.Fatal("restart lost per-source caches")
	}
	if _, err := os.Stat(filepath.Join(c.DataDir, "sources", "secondary", "cache.json")); err != nil {
		t.Fatal(err)
	}
	c.Sources = nil
	single, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := single.snapshot(); !reflect.DeepEqual(got.Links, []string{testLink}) || single.token != b.token {
		t.Fatal("removing a source changed primary state")
	}
}

func TestSubscriptionIDsAndMetadata(t *testing.T) {
	for _, id := range []string{"default", "Default", "all", "../outside", "a/b", "", "bad.name"} {
		c := config{Sources: []subscriptionConfig{{ID: id, Name: "test"}}}
		if c.validate(t.TempDir()) == nil {
			t.Errorf("unsafe or reserved ID accepted: %q", id)
		}
	}
	c := config{Sources: []subscriptionConfig{{ID: "a"}, {ID: "a"}}}
	if c.validate(t.TempDir()) == nil {
		t.Fatal("duplicate source IDs accepted")
	}
	c = config{Sources: []subscriptionConfig{{ID: "ONE"}, {ID: "one"}}}
	if c.validate(t.TempDir()) == nil {
		t.Fatal("source IDs collide on Windows")
	}
	node := describeConfiguration("vless://00000000-0000-4000-8000-000000000001@[2001:db8::1]:443?security=reality#%D0%A2%D0%B5%D1%81%D1%82")
	if node.Name != "Тест" || node.Address != "2001:db8::1" || node.Port != "443" || node.Protocol != "vless" || !strings.HasPrefix(node.URI, "vless://") {
		t.Fatal("configuration metadata", node)
	}
}
