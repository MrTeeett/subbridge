package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPanelOptionsDiscoverWithoutSavingOrExposingPrivateFields(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "options", true: "legacy-list"}[legacy], func(t *testing.T) {
			var writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes.Add(1)
					w.WriteHeader(405)
					return
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-api-token" {
					w.WriteHeader(401)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				var object any
				switch r.URL.Path {
				case "/secret/panel/api/clients/list":
					object = []map[string]any{{"id": 1, "email": "client-1", "inboundIds": []int{7}, "uuid": "private-client-uuid", "subId": "private-subscription-id"}}
				case "/secret/panel/api/inbounds/options":
					if legacy {
						w.WriteHeader(404)
						return
					}
					object = []map[string]any{{"id": 7, "remark": "VLESS 443", "protocol": "vless", "port": 443, "settings": "private-settings"}}
				case "/secret/panel/api/inbounds/list":
					object = []map[string]any{{"id": 7, "remark": "VLESS 443", "protocol": "vless", "port": 443, "settings": "private-settings"}}
				default:
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": object})
			}))
			defer srv.Close()
			a, client, base := adminFixture(t)
			disabled := false
			c := a.engine.current.Load().config
			c.Panel = &panelConfig{Enabled: &disabled, URL: srv.URL + "/secret", AuthMode: "token", APIToken: "synthetic-api-token"}
			if err := a.engine.apply(c, true); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(a.engine.path)
			if err != nil {
				t.Fatal(err)
			}
			request := map[string]any{"panel": map[string]any{"url": srv.URL + "/secret", "auth_mode": "token"}}
			if status, _ := adminRequest(t, client, base, "panel-options", "", request); status != 401 {
				t.Fatal("discovery lacks authentication", status)
			}
			csrf := adminLogin(t, client, base, "synthetic-password")
			if status, _ := adminRequest(t, client, base, "panel-options", "", request); status != 403 {
				t.Fatal("discovery lacks CSRF", status)
			}
			status, data := adminRequest(t, client, base, "panel-options", csrf, request)
			if status != 200 {
				t.Fatal("discovery without selected clients failed", status, string(data))
			}
			var result panelOptions
			if json.Unmarshal(data, &result) != nil || len(result.Clients) != 1 || len(result.Inbounds) != 1 || result.Inbounds[0].ID != 7 || result.Clients[0].Email != "client-1" {
				t.Fatal("unexpected catalogue", string(data))
			}
			for _, secret := range []string{"private-client-uuid", "private-settings", "private-subscription-id", "synthetic-api-token"} {
				if strings.Contains(string(data), secret) {
					t.Fatal("private field exposed", secret)
				}
			}
			after, _ := os.ReadFile(a.engine.path)
			if !bytes.Equal(before, after) || writes.Load() != 0 {
				t.Fatal("discovery modified configuration or panel")
			}
			request["token"] = "invalid-token"
			if status, _ := adminRequest(t, client, base, "panel-options", csrf, request); status != 502 {
				t.Fatal("invalid credentials accepted", status)
			}
			after, _ = os.ReadFile(a.engine.path)
			if !bytes.Equal(before, after) {
				t.Fatal("failed discovery replaced saved credentials")
			}
		})
	}
}
