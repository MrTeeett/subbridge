package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Preserve errors.Is for cancellation without exposing URLs in log messages.
type safeError struct {
	message string
	cause   error
}

func (e *safeError) Error() string                  { return e.message }
func (e *safeError) Unwrap() error                  { return e.cause }
func safeFailure(message string, cause error) error { return &safeError{message, cause} }

func decodeBase64(value string) ([]byte, error) {
	value = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, value)
	value = strings.NewReplacer("-", "+", "_", "/").Replace(value)
	value += strings.Repeat("=", (4-len(value)%4)%4)
	return base64.StdEncoding.DecodeString(value)
}

func isSourceURL(value string) bool {
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return false
	}
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	u, err := url.Parse(value)
	if err == nil && u.User != nil && u.Port() != "" && (u.Path == "" || u.Path == "/") {
		return false
	}
	return true
}

func unwrapOne(value string) (string, bool, error) {
	lower := strings.ToLower(value)
	for _, prefix := range []string{"http%3a", "https%3a", "v2raytun%3a", "happ%3a"} {
		if strings.HasPrefix(lower, prefix) {
			text, err := url.PathUnescape(value)
			if err != nil {
				return "", false, errors.New("некорректная ссылка импорта")
			}
			return text, true, nil
		}
	}
	for _, scheme := range []string{"v2raytun", "happ"} {
		for _, form := range []string{"crypt", "crypt3"} {
			prefix := scheme + "://" + form + "/"
			if strings.HasPrefix(value, prefix) {
				text, err := decryptPayload(strings.TrimPrefix(value, prefix))
				return text, true, err
			}
		}
		if prefix := scheme + "://import/"; strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix), true, nil
		}
		if strings.HasPrefix(value, scheme+"://import-sub?") || strings.HasPrefix(value, scheme+"://import-config?") {
			u, err := url.Parse(value)
			if err != nil {
				return "", false, errors.New("некорректная ссылка импорта")
			}
			q, err := url.ParseQuery(u.RawQuery)
			if err != nil || len(q["url"]) != 1 || q.Get("url") == "" {
				return "", false, errors.New("в ссылке импорта нужен единственный url")
			}
			return q.Get("url"), true, nil
		}
	}
	return value, false, nil
}

func sourceClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.User != nil {
			return errors.New("некорректное перенаправление источника")
		}
		// The source profile belongs to this configured subscription and its
		// provider redirects. Public endpoint headers never enter this client.
		for key, values := range via[0].Header {
			req.Header[key] = append([]string(nil), values...)
		}
		return nil
	}}
}

func fetchSource(ctx context.Context, client *http.Client, address string, p profile) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("некорректный URL источника")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", safeFailure("некорректный запрос источника", err)
	}
	req.Header, err = p.headers()
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	response, err := client.Do(req)
	if err != nil {
		return "", safeFailure("источник недоступен", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &sourceHTTPError{response.StatusCode}
	}
	b, err := readBounded(response.Body, maxBytes)
	if err != nil {
		return "", safeFailure("не удалось прочитать источник (максимум 8 МиБ)", err)
	}
	var reader io.ReadCloser
	switch strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		reader, err = gzip.NewReader(bytes.NewReader(b))
	case "deflate":
		reader, err = zlib.NewReader(bytes.NewReader(b))
	default:
		return "", errors.New("неподдерживаемое сжатие ответа источника")
	}
	if err != nil {
		return "", safeFailure("повреждённый сжатый ответ источника", err)
	}
	if reader != nil {
		defer reader.Close()
		b, err = readBounded(reader, maxBytes)
		if err != nil {
			return "", safeFailure("не удалось распаковать источник (максимум 8 МиБ)", err)
		}
	}
	_, params, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	if charset == "iso-8859-1" || charset == "latin-1" {
		runes := make([]rune, len(b))
		for i, v := range b {
			runes[i] = rune(v)
		}
		return string(runes), nil
	}
	if !utf8.Valid(b) {
		return "", errors.New("источник не является текстом UTF-8")
	}
	return string(b), nil
}

func resolveSource(ctx context.Context, client *http.Client, source sourceConfig) (string, error) {
	value := source.URL
	for layer := 0; layer <= 8; layer++ {
		if err := ctx.Err(); err != nil {
			return "", safeFailure("загрузка источника отменена", err)
		}
		value = strings.TrimPrefix(strings.TrimSpace(value), "\ufeff")
		if value == "" || len(value) > maxBytes {
			return "", errors.New("пустой или слишком большой источник")
		}
		next, changed, err := unwrapOne(value)
		if err != nil {
			return "", err
		}
		if changed {
			if layer == 8 {
				break
			}
			value = next
			continue
		}
		if isSourceURL(value) {
			if layer == 8 {
				break
			}
			value, err = fetchSource(ctx, client, value, source.profile)
			if err != nil {
				return "", err
			}
			continue
		}
		if b, err := decodeBase64(value); err == nil && utf8.Valid(b) {
			candidate := strings.TrimSpace(string(b))
			if isSourceURL(candidate) || strings.HasPrefix(candidate, "v2raytun://") || strings.HasPrefix(candidate, "happ://") {
				if layer == 8 {
					break
				}
				value = candidate
				continue
			}
		}
		return value, nil
	}
	return "", errors.New("превышен лимит 8 вложенных ссылок/ответов")
}
