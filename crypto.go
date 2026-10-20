package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// These are the original wrapped application assets, never user credentials.
//
//go:embed crypto-assets.json
var cryptoAssets []byte

type taggedKey struct {
	tag string
	key *rsa.PrivateKey
}

var loadKeys = sync.OnceValues(func() ([]taggedKey, error) {
	var assets struct {
		Bootstrap struct {
			A      []string `json:"array_a_u64"`
			B      []string `json:"array_b_u64"`
			SHA256 string   `json:"sha256"`
		} `json:"bootstrap"`
		Keys []struct {
			Tag    string `json:"tag"`
			Blob   string `json:"encrypted_blob_base64"`
			SHA256 string `json:"public_key_spki_sha256"`
		} `json:"keys"`
	}
	bad := errors.New("не удалось загрузить встроенные ключи")
	if json.Unmarshal(cryptoAssets, &assets) != nil || len(assets.Bootstrap.A) != 4 || len(assets.Bootstrap.B) != 4 || len(assets.Keys) != 3 {
		return nil, bad
	}
	bootstrap := make([]byte, 32)
	for i := range assets.Bootstrap.A {
		a, errA := strconv.ParseUint(assets.Bootstrap.A[i], 16, 64)
		b, errB := strconv.ParseUint(assets.Bootstrap.B[i], 16, 64)
		if errA != nil || errB != nil {
			return nil, bad
		}
		binary.BigEndian.PutUint64(bootstrap[i*8:], a^b)
	}
	mask := sha256.Sum256(bootstrap)
	if hex.EncodeToString(mask[:]) != assets.Bootstrap.SHA256 {
		return nil, bad
	}
	block, err := aes.NewCipher(bootstrap)
	if err != nil {
		return nil, bad
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, bad
	}
	keys := make([]taggedKey, 0, 3)
	for i, entry := range assets.Keys {
		if entry.Tag != []string{"crypt3", "crypt4", "happ_crypt3"}[i] {
			return nil, bad
		}
		encoded, err := base64.StdEncoding.DecodeString(entry.Blob)
		if err != nil || len(encoded) < 29 {
			return nil, bad
		}
		blob := make([]byte, len(encoded))
		for j := range encoded {
			blob[j] = encoded[len(encoded)-1-j] ^ mask[j%32]
		}
		if blob[0] != 12 {
			return nil, bad
		}
		der, err := gcm.Open(nil, blob[1:13], blob[13:], nil)
		if err != nil {
			return nil, bad
		}
		parsed, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			key, parseErr := x509.ParsePKCS1PrivateKey(der)
			if parseErr != nil {
				return nil, bad
			}
			parsed = key
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok || key.N.BitLen() != 4096 {
			return nil, bad
		}
		public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			return nil, bad
		}
		digest := sha256.Sum256(public)
		if hex.EncodeToString(digest[:]) != entry.SHA256 {
			return nil, bad
		}
		keys = append(keys, taggedKey{entry.Tag, key})
	}
	return keys, nil
})

func decryptPayload(payload string) (string, error) {
	if len(payload) > 16384 {
		return "", errors.New("зашифрованная ссылка слишком длинная")
	}
	payload = strings.TrimSpace(payload)
	payload = strings.NewReplacer(" ", "", "-", "+", "_", "/").Replace(payload)
	payload += strings.Repeat("=", (4-len(payload)%4)%4)
	ciphertext, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(ciphertext) != 512 {
		return "", errors.New("некорректный RSA/Base64 блок")
	}
	keys, err := loadKeys()
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		// PKCS#1 v1.5 is required by the existing application format.
		plain, err := rsa.DecryptPKCS1v15(nil, key.key, ciphertext)
		if err != nil {
			continue
		}
		if !utf8.Valid(plain) {
			return "", errors.New("расшифрованные данные не являются UTF-8")
		}
		text := strings.TrimSpace(string(plain))
		if text != "" {
			return text, nil
		}
	}
	return "", errors.New("зашифрованная ссылка не подходит к известным ключам v2rayTun")
}
