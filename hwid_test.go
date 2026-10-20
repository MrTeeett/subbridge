package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

func automaticBridge(t *testing.T, server *httptest.Server, attempts int) *bridge {
	t.Helper()
	c := config{Source: sourceConfig{URL: server.URL, HWIDAttempts: attempts}, DataDir: filepath.Join(t.TempDir(), "state")}
	if err := c.validate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAutomaticHWIDAcceptedAndReused(t *testing.T) {
	var mu sync.Mutex
	var identities []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		identities = append(identities, r.Header.Get("X-HWID"))
		if len(identities) < 3 {
			w.WriteHeader(403)
			return
		}
		_, _ = io.WriteString(w, testLink)
	}))
	defer srv.Close()
	b := automaticBridge(t, srv, 3)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(identities) != 3 {
		t.Fatal("unexpected attempt count")
	}
	seen := map[string]bool{}
	for _, id := range identities {
		if !hwidPattern.MatchString(id) || seen[id] {
			t.Fatal("invalid or repeated identity")
		}
		seen[id] = true
	}
	want := identities[2]
	mu.Unlock()
	restarted, err := newBridge(b.config)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.identity != want || restarted.token != b.token {
		t.Fatal("identity or subscription URL changed after restart")
	}
	cached, _ := restarted.snapshot()
	if len(cached.Links) != 1 {
		t.Fatal("accepted identity did not retain cache")
	}
	if err := restarted.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(identities) != 4 || identities[3] != want {
		t.Fatal("accepted HWID was not reused")
	}
}

func TestHWIDExhaustionRetainsIdentityAndCache(t *testing.T) {
	var mu sync.Mutex
	deny := false
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if deny {
			w.WriteHeader(403)
			return
		}
		_, _ = io.WriteString(w, testLink)
	}))
	defer srv.Close()
	b := automaticBridge(t, srv, 2)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := b.identity
	mu.Lock()
	deny = true
	mu.Unlock()
	if err := b.refresh(context.Background()); err == nil {
		t.Fatal("denial accepted")
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 3 {
		t.Fatal("attempt limit not enforced")
	}
	restarted, err := newBridge(b.config)
	if err != nil {
		t.Fatal(err)
	}
	cached, _ := restarted.snapshot()
	if restarted.identity != want || len(cached.Links) != 1 {
		t.Fatal("failed candidates replaced accepted identity/cache")
	}
}

func TestHWIDOnlyRetriesDeviceRejection(t *testing.T) {
	for _, status := range []int{429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(status) }))
			defer srv.Close()
			b := automaticBridge(t, srv, 3)
			if err := b.refresh(context.Background()); err == nil || requests != 1 {
				t.Fatal("non-device failure was retried")
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	b := automaticBridge(t, srv, 20)
	b.config.Source.HWID = "manual-device"
	b.identity = "manual-device"
	if err := b.refresh(context.Background()); err == nil || b.lastAttempts != 1 || b.identity != "manual-device" {
		t.Fatal("manual HWID was rotated")
	}
	b.config.Source.HWID = ""
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation cause lost", err)
	}
	if identityRejected("device limit", nil) || identityRejected("hwid", &sourceHTTPError{429}) || !identityRejected("device limit", errors.New("parse")) {
		t.Fatal("rejection classification")
	}
}
