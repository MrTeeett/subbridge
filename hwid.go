package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type hwidRecord struct {
	Value    string `json:"value"`
	Accepted bool   `json:"accepted"`
}
type hwidFile struct {
	Version int                   `json:"version"`
	Sources map[string]hwidRecord `json:"sources"`
}

var hwidPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)
var hwidDenial = regexp.MustCompile(`(?i)hwid|лимит.{0,30}устрой|device.{0,25}(limit|bound|registered)|(?:limit|bound).{0,25}device`)

func randomHWID() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", safeFailure("не удалось создать HWID", err)
	}
	return strings.ToUpper(hex.EncodeToString(value)), nil
}
func sourceScope(source string) string {
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:])
}
func sourceFingerprint(s sourceConfig) string {
	// Preserve the previous fingerprint format: retry policy is not identity.
	b, _ := json.Marshal(struct {
		URL string `json:"url"`
		profile
	}{s.URL, s.profile})
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:])
}
func loadHWIDs(directory string) (hwidFile, error) {
	f := hwidFile{Version: 1, Sources: map[string]hwidRecord{}}
	b, err := privateFile(filepath.Join(directory, "hwid.json"), 128<<10)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, safeFailure("не удалось прочитать HWID", err)
	}
	if json.Unmarshal(b, &f) != nil || f.Version != 1 || f.Sources == nil || len(f.Sources) > 256 {
		return f, errors.New("повреждён файл HWID")
	}
	return f, nil
}
func persistIdentity(c config, value string, accepted bool) error {
	f, err := loadHWIDs(c.DataDir)
	if err != nil {
		return err
	}
	key := sourceScope(c.Source.URL)
	if _, ok := f.Sources[key]; !ok && len(f.Sources) >= 256 {
		return errors.New("достигнут лимит сохранённых источников HWID")
	}
	f.Sources[key] = hwidRecord{value, accepted}
	encoded, err := json.Marshal(f)
	if err != nil {
		return errors.New("не удалось сохранить HWID")
	}
	if err := atomicWrite(filepath.Join(c.DataDir, "hwid.json"), encoded); err != nil {
		return safeFailure("не удалось сохранить HWID", err)
	}
	return nil
}
func sourceIdentity(c config) (string, error) {
	if c.Source.URL == "" {
		return "", nil
	}
	if c.Source.HWID != "" {
		return c.Source.HWID, nil
	}
	f, err := loadHWIDs(c.DataDir)
	if err != nil {
		return "", err
	}
	if found, ok := f.Sources[sourceScope(c.Source.URL)]; ok {
		if !hwidPattern.MatchString(found.Value) {
			return "", errors.New("повреждён сохранённый HWID")
		}
		return found.Value, nil
	}
	value, err := randomHWID()
	if err != nil {
		return "", err
	}
	if err := persistIdentity(c, value, false); err != nil {
		return "", err
	}
	return value, nil
}

type sourceHTTPError struct{ status int }

func (e *sourceHTTPError) Error() string {
	return fmt.Sprintf("источник вернул HTTP %d", e.status)
}
func identityRejected(body string, err error) bool {
	var status *sourceHTTPError
	if errors.As(err, &status) {
		return status.status == 403
	}
	if err == nil {
		return false
	}
	for i := 0; i < 5; i++ {
		decoded, _ := url.PathUnescape(body)
		if hwidDenial.MatchString(decoded) {
			return true
		}
		b, decodeErr := decodeBase64(body)
		if decodeErr != nil {
			break
		}
		body = string(b)
	}
	return false
}
func (b *bridge) resolveWithIdentity(ctx context.Context) (conversion, sourceConfig, error) {
	source := b.config.Source
	b.mu.RLock()
	source.HWID = b.identity
	b.mu.RUnlock()
	attempts := 1
	if b.config.Source.HWID == "" {
		attempts = b.config.Source.HWIDAttempts
		if attempts < 1 {
			attempts = 3
		}
	}
	var parsed conversion
	for attempt := 1; attempt <= attempts; attempt++ {
		b.mu.Lock()
		b.lastAttempts = attempt
		b.mu.Unlock()
		body, err := resolveSource(ctx, b.client, source)
		if err == nil {
			parsed, err = parseSubscription(body)
		}
		if err == nil {
			if b.config.Source.HWID == "" {
				if err := persistIdentity(b.config, source.HWID, true); err != nil {
					return parsed, source, err
				}
			}
			b.mu.Lock()
			b.identity = source.HWID
			b.mu.Unlock()
			return parsed, source, nil
		}
		if !identityRejected(body, err) || b.config.Source.HWID != "" {
			return parsed, source, err
		}
		if attempt == attempts {
			return parsed, source, safeFailure(fmt.Sprintf("HWID отклонён: попыток %d. Проверьте привязку устройства у провайдера", attempt), err)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return parsed, source, safeFailure("подбор HWID отменён", ctx.Err())
		case <-timer.C:
		}
		var generateErr error
		source.HWID, generateErr = randomHWID()
		if generateErr != nil {
			return parsed, source, generateErr
		}
	}
	return parsed, source, errors.New("источник недоступен")
}
