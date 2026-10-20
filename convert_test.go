package main

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const testLink = "vless://00000000-0000-4000-8000-000000000123@example.com:443?encryption=none&security=reality&fp=qq&type=xhttp&extra=%7B%22mode%22%3A%22auto%22%7D&pqv=a_+-&x=1&x=2#Test"
const secondLink = "trojan://password@example.net:443?security=tls#Second"

func TestOpaqueLinksAndCompatibility(t *testing.T) {
	for _, input := range []string{testLink + "\n" + secondLink, base64.StdEncoding.EncodeToString([]byte(testLink + "\n" + secondLink)), "[" + compact(testLink) + "," + compact(secondLink) + "]", "profile-web-page-url: https://example.org\n" + base64.StdEncoding.EncodeToString([]byte(testLink+"\n"+secondLink))} {
		parsed, err := parseSubscription(input)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{testLink, secondLink}
		if !reflect.DeepEqual(parsed.Links, want) {
			t.Fatal("original URI bytes changed")
		}
		adapted := exportLinks(parsed.Links, "v2rayn", "all")
		if adapted[0] != strings.Replace(testLink, "fp=qq", "fp=firefox", 1) || adapted[1] != secondLink {
			t.Fatal("compatibility changed unrelated bytes")
		}
		if !reflect.DeepEqual(parsed.Links, want) {
			t.Fatal("export mutated source")
		}
		if got := exportLinks(parsed.Links, "original", "vless"); len(got) != 1 || got[0] != testLink {
			t.Fatal("VLESS filter")
		}
	}
	encoded := strings.Replace(testLink, "fp=qq", "f%70=q%71", 1)
	if exportLinks([]string{encoded}, "v2rayn", "all")[0] != strings.Replace(encoded, "f%70=q%71", "f%70=firefox", 1) {
		t.Fatal("encoded fingerprint")
	}
}

func TestJSONTransportsAndBadSiblings(t *testing.T) {
	input := `/* header */ [{"remarks":"LTE","routing":{"rules":[]},"outbounds":[
 {"protocol":"freedom"},
 {"protocol":"vless","tag":"proxy","settings":{"vnext":[{"address":"example.com","port":443,"users":[{"id":"id-123","encryption":"mlkem768x25519plus.native.0rtt.a_+","flow":"xtls-rprx-vision"},{"id":""}]}]},
 "streamSettings":{"network":"xhttp","security":"reality","realitySettings":{"password":"public-key","publicKey":"old","mldsa65Verify":"a_+","shortId":"abcd","fingerprint":"qq","serverName":"images.example.com","supportX25519MLKEM768":true},
 "xhttpSettings":{"host":"images.example.com","path":"/api/data","mode":"auto","extra":{"mode":"packet-up","xPaddingBytes":"100-1000"},"ignored":"bad"}}},
 {"protocol":"vless","settings":{"vnext":[{"address":"0.0.0.0","port":443,"users":[{"id":"stub"}]}]}}]}]`
	parsed, err := parseSubscription(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Links) != 1 {
		t.Fatalf("nodes=%d", len(parsed.Links))
	}
	u, err := url.Parse(parsed.Links[0])
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for key, want := range map[string]string{"fp": "qq", "type": "xhttp", "encryption": "mlkem768x25519plus.native.0rtt.a_+", "pbk": "public-key", "pqv": "a_+", "support-x25519mlkem768": "true", "flow": "xtls-rprx-vision", "path": "/api/data"} {
		if q.Get(key) != want {
			t.Errorf("query %s not preserved", key)
		}
	}
	var extra map[string]any
	if json.Unmarshal([]byte(q.Get("extra")), &extra) != nil || extra["mode"] != "packet-up" || extra["ignored"] != nil {
		t.Fatal("XHTTP extra precedence")
	}
	if len(parsed.Warnings) == 0 {
		t.Fatal("partial conversion needs warnings")
	}
	for _, network := range []string{"tcp", "raw", "ws", "httpupgrade", "grpc", "kcp"} {
		settings := `"` + network + `Settings":{"path":"/path","headers":{"Host":"host.example.com"},"serviceName":"service","multiMode":true,"seed":"test"}`
		doc := `{"protocol":"vless","settings":{"vnext":[{"address":"example.org","port":443,"users":[{"id":"id"}]}]},"streamSettings":{"network":"` + network + `","security":"none",` + settings + `}}`
		result, err := parseSubscription(doc)
		if err != nil || len(result.Links) != 1 {
			t.Fatalf("transport %s: %v", network, err)
		}
	}
}

func TestHysteriaAndOtherProtocols(t *testing.T) {
	doc := `{"protocol":"hysteria","settings":{"address":"example.com","port":443,"version":2},"streamSettings":{"network":"hysteria","security":"tls","hysteriaSettings":{"version":2,"auth":"test-password"},"tlsSettings":{"serverName":"test.example.com","allowInsecure":true},"finalmask":{"udp":[{"type":"salamander","settings":{"password":"obfs-password","packetSize":"1200-1000"}}],"quicParams":{"udpHop":{"ports":"443,20000-21000"}}}}}`
	parsed, err := parseSubscription(doc)
	if err != nil || len(parsed.Links) != 1 {
		t.Fatal(err)
	}
	u, _ := url.Parse(parsed.Links[0])
	q := u.Query()
	for k, v := range map[string]string{"insecure": "1", "obfs": "gecko", "minPacketSize": "1000", "maxPacketSize": "1200", "mport": "443,20000-21000"} {
		if q.Get(k) != v {
			t.Errorf("Hysteria field %s", k)
		}
	}
	if _, err := parseSubscription(strings.Replace(doc, `"version":2`, `"version":1`, 1)); err == nil {
		t.Fatal("Hysteria v1 relabeled as v2")
	}
	for _, protocol := range []string{"trojan", "shadowsocks", "socks", "http", "vmess"} {
		server := `{"address":"example.com","port":443,"password":"password","method":"aes-128-gcm","users":[{"id":"uuid","user":"user","pass":"pass"}]}`
		key := "servers"
		if protocol == "vmess" {
			key = "vnext"
		}
		result, err := parseSubscription(`{"protocol":"` + protocol + `","settings":{"` + key + `":[` + server + `]}}`)
		if err != nil || len(result.Links) != 1 {
			t.Fatalf("protocol %s: %v", protocol, err)
		}
	}
	uri := "hysteria2://auth@example.com:443,20000-21000?sni=example.com#PortList"
	if result, err := parseSubscription(uri); err != nil || result.Links[0] != uri {
		t.Fatal("Hysteria port list changed")
	}
}

func TestInvalidInputsAndLimits(t *testing.T) {
	for _, input := range []string{"", `{"outbounds":[]}`, `[{"protocol":"freedom"}]`, "vless://id@0.0.0.0:443#Error", "vless://id@127.0.0.1:443#Device%20limit", `{"secret":"must-not-log"}`, testLink + " bad", strings.Repeat("x", maxBytes+1), `{"protocol":"vless"} trailing`, `/* unterminated`} {
		if _, err := parseSubscription(input); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := parseSubscription(strings.Repeat(testLink+"\n", 10001)); err == nil {
		t.Fatal("node limit not enforced")
	}
	parsed, err := parseSubscription(testLink + "\n" + testLink)
	if err != nil || len(parsed.Links) != 1 {
		t.Fatal("deduplication failed")
	}
}

// Optional regression against the user's private portable export. Only counts
// and generic assertions appear in test output, never credentials or URLs.
func TestPortableParity(t *testing.T) {
	path := os.Getenv("SUBBRIDGE_PRIVATE_BUNDLE")
	if path == "" {
		t.Skip("private local fixture not configured")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private fixture unavailable")
	}
	var bundle struct {
		Original string   `json:"original"`
		Links    []string `json:"links"`
	}
	if json.Unmarshal(b, &bundle) != nil {
		t.Fatal("private fixture invalid")
	}
	parsed, err := parseSubscription(bundle.Original)
	if err != nil {
		t.Fatal("private JSON conversion failed")
	}
	if len(parsed.Links) != len(bundle.Links) {
		t.Fatalf("node counts differ: Go=%d reference=%d", len(parsed.Links), len(bundle.Links))
	}
	canonical := func(link string) string {
		u, err := url.Parse(link)
		if err != nil {
			return link
		}
		q := u.Query()
		for _, key := range []string{"extra", "fm"} {
			if value := q.Get(key); value != "" {
				var v any
				if json.Unmarshal([]byte(value), &v) == nil {
					q.Set(key, compact(v))
				}
			}
		}
		u.RawQuery = q.Encode()
		return u.String()
	}
	actual, expected := make([]string, len(parsed.Links)), make([]string, len(bundle.Links))
	for i, v := range parsed.Links {
		actual[i] = canonical(v)
	}
	for i, v := range bundle.Links {
		expected[i] = canonical(v)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("private configuration parameters differ from the verified reference")
	}
	t.Logf("Go and reference agree: %d node configurations", len(actual))
}
