package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSyntheticCryptoFixtures(t *testing.T) {
	b, err := os.ReadFile("testdata/crypto.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Payload  string `json:"payload_base64"`
			URLSafe  string `json:"payload_base64url_unpadded"`
			Expected string `json:"expected_text"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Cases) != 3 {
		t.Fatal("expected three original-key fixtures")
	}
	for i, c := range fixtures.Cases {
		for _, payload := range []string{c.Payload, "\u2003 " + c.URLSafe[:40] + " " + c.URLSafe[40:] + " \u2003"} {
			got, err := decryptPayload(payload)
			if err != nil || got != c.Expected {
				t.Fatalf("fixture %d: %v", i, err)
			}
		}
		for _, prefix := range []string{"v2raytun://crypt/", "v2raytun://crypt3/", "happ://crypt/", "happ://crypt3/"} {
			got, changed, err := unwrapOne(prefix + c.Payload)
			if err != nil || !changed || got != c.Expected {
				t.Fatalf("prefix fixture %d: %v", i, err)
			}
		}
		if _, err := decryptPayload(c.Payload[:100] + "\t" + c.Payload[100:]); err == nil {
			t.Fatal("internal tab accepted")
		}
	}
	for _, bad := range []string{"", "AAAA", "!bad!", strings.Repeat("x", 16385), base64.StdEncoding.EncodeToString(make([]byte, 512)), base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\xff", 512)))} {
		if _, err := decryptPayload(bad); err == nil {
			t.Fatal("invalid encrypted block accepted")
		}
	}
}
