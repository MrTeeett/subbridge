package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type testProvider struct {
	mu      sync.Mutex
	body    string
	status  int
	headers []http.Header
}

func (p *testProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.headers = append(p.headers, r.Header.Clone())
	if r.Header.Get("X-HWID") != "synthetic-phone" || r.Header.Get("User-Agent") != "v2raytun/android" || r.Header.Get("X-Device-Model") != "Synthetic Phone" {
		w.WriteHeader(403)
		return
	}
	w.WriteHeader(p.status)
	_, _ = io.WriteString(w, p.body)
}
func (p *testProvider) set(body string, status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body, p.status = body, status
}
func makeTestBridge(t *testing.T) (*bridge, *httptest.Server, *testProvider) {
	t.Helper()
	provider := &testProvider{body: testLink + "\n" + secondLink, status: 200}
	upstream := httptest.NewServer(provider)
	t.Cleanup(upstream.Close)
	server := httptest.NewUnstartedServer(nil)
	c := config{Source: sourceConfig{URL: upstream.URL + "/private-source", profile: profile{HWID: "synthetic-phone", DeviceModel: "Synthetic Phone"}}, PublicURL: "http://" + server.Listener.Addr().String(), DataDir: filepath.Join(t.TempDir(), "state")}
	if err := c.validate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b, err := newBridge(c)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = b
	server.Start()
	t.Cleanup(server.Close)
	return b, server, provider
}
func testGET(t *testing.T, address string, headers http.Header) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if headers != nil {
		req.Header = headers
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header
}

func TestHeadlessCacheIdentityAndProfiles(t *testing.T) {
	b, server, provider := makeTestBridge(t)
	if status, _, _ := testGET(t, b.subscriptionURL(), nil); status != 503 {
		t.Fatal("empty cache was served")
	}
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, raw, _ := testGET(t, b.subscriptionURL(), http.Header{"X-Hwid": {"untrusted-second-device"}})
	if status != 200 || raw != testLink+"\n"+secondLink+"\n" {
		t.Fatal("original source altered")
	}
	_, desktop, _ := testGET(t, b.subscriptionURL()+"?profile=v2rayn&protocol=vless", nil)
	if desktop != strings.Replace(testLink, "fp=qq", "fp=firefox", 1)+"\n" {
		t.Fatal("desktop endpoint")
	}
	_, encoded, _ := testGET(t, b.subscriptionURL()+"?format=base64", nil)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != raw {
		t.Fatal("base64 endpoint")
	}
	for _, path := range []string{"/sub/wrong-token", "/api/profile", "/api/convert"} {
		if status, _, _ := testGET(t, server.URL+path, nil); status != 404 {
			t.Fatal("unexpected public endpoint")
		}
	}
	if status, _, _ := testGET(t, b.subscriptionURL()+"?profile=bad", nil); status != 400 {
		t.Fatal("invalid profile accepted")
	}
	if status, _, _ := testGET(t, b.subscriptionURL()+"?profile=original&profile=v2rayn", nil); status != 400 {
		t.Fatal("duplicate query accepted")
	}
	provider.mu.Lock()
	requests := len(provider.headers)
	provider.mu.Unlock()
	if requests != 1 {
		t.Fatal("public requests triggered upstream fetches")
	}
	provider.set("vless://stub@0.0.0.0:443#Error", 200)
	if err := b.refresh(context.Background()); err == nil {
		t.Fatal("provider denial cached")
	}
	_, stale, headers := testGET(t, b.subscriptionURL(), nil)
	if stale != raw || headers.Get("X-SubBridge-Stale") != "true" {
		t.Fatal("last good cache not preserved")
	}
	if _, body, _ := testGET(t, server.URL+"/health", nil); strings.Contains(body, "synthetic-phone") || strings.Contains(body, "private-source") || !strings.Contains(body, "stale") {
		t.Fatal("health disclosure/status")
	}
	restarted, err := newBridge(b.config)
	if err != nil {
		t.Fatal(err)
	}
	cached, _ := restarted.snapshot()
	if len(cached.Links) != 2 || restarted.token != b.token {
		t.Fatal("restart lost cache or stable URL")
	}
	changed := b.config
	changed.Source.HWID = "different-profile"
	other, err := newBridge(changed)
	if err != nil {
		t.Fatal(err)
	}
	cached, _ = other.snapshot()
	if len(cached.Links) != 0 {
		t.Fatal("cache reused for another identity")
	}
	for _, name := range []string{"token", "cache.json"} {
		info, err := os.Stat(filepath.Join(b.config.DataDir, name))
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
			t.Fatal("private state permissions")
		}
	}
}

func TestConcurrentRefreshAndHTTP(t *testing.T) {
	b, _, provider := makeTestBridge(t)
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	wg.Go(func() {
		for i := 0; i < 12; i++ {
			provider.set(testLink, 200)
			if i%2 == 0 {
				provider.set(secondLink, 200)
			}
			if err := b.refresh(context.Background()); err != nil {
				failures <- err
				return
			}
		}
	})
	wg.Go(func() {
		for i := 0; i < 30; i++ {
			resp, err := http.Get(b.subscriptionURL() + "?profile=v2rayn")
			if err != nil {
				failures <- err
				return
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				failures <- err
				return
			}
			if resp.StatusCode != 200 || len(body) == 0 {
				failures <- errors.New("invalid concurrent response")
				return
			}
		}
	})
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestSourceWrappersCompressionAndBounds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nested":
			_, _ = io.WriteString(w, "http://"+r.Host+"/gzip")
		case "/gzip", "/bomb":
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			defer zw.Close()
			if r.URL.Path == "/bomb" {
				_, _ = io.Copy(zw, strings.NewReader(strings.Repeat("x", maxBytes+1)))
			} else {
				_, _ = io.WriteString(zw, testLink)
			}
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		default:
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	p := profile{}
	p.defaults()
	for _, source := range []string{server.URL + "/nested", "v2raytun://import/" + url.QueryEscape(server.URL+"/gzip"), "v2raytun://import-sub?url=" + url.QueryEscape(server.URL+"/gzip"), base64.StdEncoding.EncodeToString([]byte(server.URL + "/gzip"))} {
		got, err := resolveSource(context.Background(), sourceClient(), sourceConfig{URL: source, profile: p})
		if err != nil || got != testLink {
			t.Fatalf("nested/wrapped/gzip: %v", err)
		}
	}
	for _, path := range []string{"/bomb", "/loop", "/private-secret"} {
		_, err := resolveSource(context.Background(), sourceClient(), sourceConfig{URL: server.URL + path, profile: p})
		if err == nil || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("unsanitized/unbounded source failure")
		}
	}
}

func TestInFlightCancellation(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	p := profile{}
	p.defaults()
	go func() { _, err := fetchSource(ctx, sourceClient(), srv.URL, p); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), srv.URL) {
			t.Fatal("cancellation cause lost or URL leaked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not cancel")
	}
}

func TestConfigAndPortableBootstrap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := `{"source":{"url":"https://example.invalid/sub","hwid":"synthetic-phone"}}`
	if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil || c.Listen != "127.0.0.1:8770" || c.Source.UserAgent != "v2raytun/android" {
		t.Fatal("config defaults", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil && runtime.GOOS != "windows" {
		t.Fatal("public config accepted")
	}
	_ = os.Chmod(path, 0600)
	for _, bad := range []string{doc + " {}", strings.Replace(doc, "hwid", "typo", 1), `{"source":{"url":"x"},"panel":{"url":"https://example.com"}}`, strings.Replace(doc, "synthetic-phone", "bad\\r\\nvalue", 1)} {
		_ = os.WriteFile(path, []byte(bad), 0600)
		if _, err := loadConfig(path); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	bundlePath := filepath.Join(dir, "portable.json")
	bundle := map[string]any{"format": "subbridge-subscription", "version": 1, "source": "https://example.invalid/sub", "request_profile": profile{HWID: "synthetic-phone", DeviceModel: "Synthetic Phone"}}
	data, _ := json.Marshal(bundle)
	_ = os.WriteFile(bundlePath, data, 0600)
	output := filepath.Join(dir, "new-config.json")
	if err := initFromBundle(bundlePath, output); err != nil {
		t.Fatal(err)
	}
	imported, err := loadConfig(output)
	if err != nil || imported.Source.HWID != "synthetic-phone" || imported.Source.DeviceModel != "Synthetic Phone" {
		t.Fatal("profile import", err)
	}
	if err := initFromBundle(bundlePath, output); err == nil {
		t.Fatal("bootstrap overwrote existing config")
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-config", output, "-check"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String()+stderr.String(), "synthetic-phone") {
		t.Fatal("check printed identity")
	}
}
