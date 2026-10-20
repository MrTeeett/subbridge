package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func availableAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_ = l.Close()
	return address
}
func adminFixture(t *testing.T) (*adminServer, *http.Client, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	c := config{Listen: availableAddress(t), DataDir: filepath.Join(directory, "state")}
	if err := c.validate(directory); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(path, c); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(context.Background(), path, c, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.close)
	m := managementDefaults()
	address := availableAddress(t)
	_, port, _ := net.SplitHostPort(address)
	m.Port, err = strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	m.PasswordHash, err = hashPassword("synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	mpath := filepath.Join(directory, "management.json")
	if err := writePrivateJSON(mpath, m); err != nil {
		t.Fatal(err)
	}
	a, err := startAdmin(e, mpath, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	jar, _ := cookiejar.New(nil)
	return a, &http.Client{Jar: jar, Timeout: 10 * time.Second}, "http://" + address + m.Path
}

func adminRequest(t *testing.T, client *http.Client, base, path, csrf string, value any) (int, []byte) {
	t.Helper()
	method := http.MethodGet
	var body io.Reader
	if value != nil {
		method = http.MethodPost
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	r, err := http.NewRequest(method, base+"api/"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Content-Type", "application/json")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}
func adminLogin(t *testing.T, client *http.Client, base, password string) string {
	t.Helper()
	status, data := adminRequest(t, client, base, "login", "", map[string]string{"username": "admin", "password": password})
	var result struct {
		CSRF string `json:"csrf"`
	}
	if status != 200 || json.Unmarshal(data, &result) != nil || result.CSRF == "" {
		t.Fatal("login failed", status)
	}
	return result.CSRF
}

func TestAdminAuthenticationAndSeparatedSettings(t *testing.T) {
	a, client, base := adminFixture(t)
	response, err := client.Get(base)
	if err != nil {
		t.Fatal("management page did not load", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Contains(page, []byte(`id="app"`)) {
		t.Fatal("management page is missing")
	}
	if status, _ := adminRequest(t, client, base, "config", "", nil); status != 401 {
		t.Fatal("private configuration accessible without login")
	}
	if status, _ := adminRequest(t, client, base, "login", "", map[string]string{"username": "admin", "password": "wrong-password"}); status != 401 {
		t.Fatal("wrong password accepted")
	}
	csrf := adminLogin(t, client, base, "synthetic-password")
	if status, _ := adminRequest(t, client, base, "refresh", "", map[string]string{}); status != 403 {
		t.Fatal("missing CSRF accepted")
	}
	r, _ := http.NewRequest(http.MethodPost, base+"api/refresh", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://untrusted.example")
	r.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("foreign origin accepted")
	}
	if status, _ := adminRequest(t, client, base, "refresh", csrf, map[string]string{}); status != 202 {
		t.Fatal("refresh not queued")
	}
	if status, data := adminRequest(t, client, base, "management", "", nil); status != 200 || strings.Contains(string(data), "password_hash") {
		t.Fatal("password hash disclosed")
	}
	before, err := os.ReadFile(a.engine.path)
	if err != nil {
		t.Fatal(err)
	}
	m := a.configuration()
	m.SSHHost = "server.example.com"
	m.SSHPort = 2200
	m.PasswordHash = ""
	if status, _ := adminRequest(t, client, base, "management", csrf, map[string]any{"config": m, "password": ""}); status != 200 {
		t.Fatal("management update failed")
	}
	after, _ := os.ReadFile(a.engine.path)
	if !bytes.Equal(before, after) {
		t.Fatal("management update changed functional settings")
	}
	saved, err := loadManagement(a.path)
	if err != nil || saved.SSHPort != 2200 {
		t.Fatal("SSH settings not persisted", err)
	}
	if status, _ := adminRequest(t, client, base, "logout", csrf, map[string]string{}); status != 200 {
		t.Fatal("logout failed")
	}
	if status, _ := adminRequest(t, client, base, "state", "", nil); status != 401 {
		t.Fatal("session survived logout")
	}
}

func TestAdminRebindRollbackAndPasswordRotation(t *testing.T) {
	a, client, base := adminFixture(t)
	csrf := adminLogin(t, client, base, "synthetic-password")
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	m := a.configuration()
	m.Port = occupied.Addr().(*net.TCPAddr).Port
	if err := a.apply(m, true); err == nil {
		t.Fatal("occupied port accepted")
	}
	if status, _ := adminRequest(t, client, base, "state", "", nil); status != 200 {
		t.Fatal("failed rebind stopped previous panel")
	}
	address := availableAddress(t)
	_, port, _ := net.SplitHostPort(address)
	m.Port, _ = strconv.Atoi(port)
	if status, _ := adminRequest(t, client, base, "management", csrf, map[string]any{"config": m, "password": ""}); status != 200 {
		t.Fatal("rebind failed")
	}
	base = "http://" + address + m.Path
	if status, _ := adminRequest(t, client, base, "state", "", nil); status != 200 {
		t.Fatal("new listener lost session")
	}
	if status, _ := adminRequest(t, client, base, "management", csrf, map[string]any{"config": m, "password": "rotated-password"}); status != 200 {
		t.Fatal("password change failed")
	}
	if status, _ := adminRequest(t, client, base, "state", "", nil); status != 401 {
		t.Fatal("password change retained sessions")
	}
	_ = adminLogin(t, client, base, "rotated-password")
}

func TestManagementSetupAndValidation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	mpath := filepath.Join(directory, "management.json")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-config", path, "-setup"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	m, err := loadManagement(mpath)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := privateFile(filepath.Join(directory, "setup-credentials.txt"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(credentials), "Password: ") || strings.Contains(stdout.String(), "Password:") {
		t.Fatal("initial credentials missing or printed")
	}
	want := m.PasswordHash
	if err := run([]string{"-config", path, "-setup"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	m, _ = loadManagement(mpath)
	if m.PasswordHash != want {
		t.Fatal("upgrade rotated password")
	}
	stdout.Reset()
	if err := run([]string{"-config", path, "-show-management"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "password_hash") {
		t.Fatal("CLI disclosed hash")
	}
	for _, attempts := range []int{-1, 21} {
		c := config{Source: sourceConfig{HWIDAttempts: attempts}}
		if c.validate(directory) == nil {
			t.Fatal("invalid attempt limit accepted")
		}
	}
}
