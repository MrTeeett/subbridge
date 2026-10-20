package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var errNode = errors.New("некорректный или неподдерживаемый узел")
var errTooMany = errors.New("подписка содержит более 10000 узлов")
var denial = regexp.MustCompile(`(?i)не поддержива|неподдержива|unsupported|not supported|лимит|device.?limit|limit.?device`)

type conversion struct {
	Links    []string
	Warnings []string
}

func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	case []any:
		out := make([]string, len(v))
		for i, x := range v {
			out[i] = text(x)
		}
		return strings.Join(out, ",")
	}
	return ""
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func array(v any) []any           { a, _ := v.([]any); return a }
func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func compact(v any) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(v) != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}
func escape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
func port(v any) (string, error) {
	s := text(v)
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return "", errNode
	}
	return strconv.Itoa(p), nil
}
func destination(host, name string) error {
	host = strings.TrimRight(strings.ToLower(strings.Trim(host, "[]")), ".")
	ip := net.ParseIP(host)
	if strings.Contains(host, ":") && ip == nil {
		return errNode
	}
	if host == "" || strings.IndexFunc(host, unicode.IsSpace) >= 0 || strings.ContainsAny(host, "/@?#") {
		return errNode
	}
	if ip != nil && ip.IsUnspecified() {
		return errors.New("источник вернул служебную заглушку")
	}
	if ((ip != nil && ip.IsLoopback()) || host == "localhost" || strings.HasSuffix(host, ".localhost")) && denial.MatchString(name) {
		return errors.New("источник вернул ограничение доступа вместо узла")
	}
	return nil
}

func validateLink(link string) error {
	if strings.IndexFunc(link, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errNode
	}
	scheme, payload, ok := strings.Cut(link, "://")
	if !ok {
		return errNode
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "vless", "vmess", "trojan", "ss", "ssr", "hysteria", "hysteria2", "hy2", "tuic", "socks", "socks5", "http", "https", "anytls", "wireguard":
	default:
		return errNode
	}
	if scheme == "vmess" && !strings.Contains(payload, "@") {
		b, err := decodeBase64(strings.SplitN(payload, "#", 2)[0])
		if err != nil {
			return errNode
		}
		var m map[string]any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if d.Decode(&m) != nil || text(m["id"]) == "" {
			return errNode
		}
		if _, err := port(m["port"]); err != nil {
			return err
		}
		return destination(text(m["add"]), text(m["ps"]))
	}
	if scheme == "ssr" {
		b, err := decodeBase64(payload)
		if err != nil {
			return errNode
		}
		parts := strings.SplitN(string(b), "/?", 2)
		fields := strings.Split(parts[0], ":")
		if len(fields) < 6 {
			return errNode
		}
		if _, err := port(fields[len(fields)-5]); err != nil {
			return err
		}
		return destination(strings.Join(fields[:len(fields)-5], ":"), "")
	}
	if (scheme == "ss" || scheme == "socks" || scheme == "socks5") && !strings.Contains(strings.SplitN(payload, "#", 2)[0], "@") {
		auth, frag, _ := strings.Cut(payload, "#")
		if !strings.Contains(auth, ":") {
			b, err := decodeBase64(strings.TrimRight(auth, "/"))
			if err != nil {
				return errNode
			}
			return validateLink(scheme + "://" + string(b) + "#" + frag)
		}
	}
	check := link
	if scheme == "hysteria2" || scheme == "hy2" {
		// Go's URL parser rejects port lists. Validate each port separately,
		// then inspect a temporary URL; return the original URI untouched.
		authority, tail := payload, ""
		if i := strings.IndexAny(authority, "/?#"); i >= 0 {
			authority, tail = authority[:i], authority[i:]
		}
		idx := strings.LastIndex(authority, ":")
		if idx > strings.LastIndex(authority, "]") && idx > strings.LastIndex(authority, "@") {
			ports := authority[idx+1:]
			for _, part := range strings.Split(ports, ",") {
				bounds := strings.Split(part, "-")
				if len(bounds) > 2 {
					return errNode
				}
				last := 0
				for _, bound := range bounds {
					p, err := port(bound)
					if err != nil {
						return err
					}
					n, _ := strconv.Atoi(p)
					if n < last {
						return errNode
					}
					last = n
				}
			}
			check = scheme + "://" + authority[:idx] + ":443" + tail
		}
	}
	u, err := url.Parse(check)
	if err != nil || u.Hostname() == "" {
		return errNode
	}
	p := u.Port()
	if p == "" && (scheme == "hysteria2" || scheme == "hy2" || scheme == "tuic" || scheme == "anytls") {
		p = "443"
	}
	if _, err := port(p); err != nil {
		return err
	}
	if (scheme == "vless" || scheme == "vmess" || scheme == "trojan" || scheme == "tuic" || scheme == "anytls" || scheme == "wireguard") && (u.User == nil || u.User.String() == "") {
		return errNode
	}
	if (scheme == "http" || scheme == "https") && (u.User == nil || (u.Path != "" && u.Path != "/")) {
		return errNode
	}
	if scheme == "ss" {
		if u.User == nil {
			return errNode
		}
		auth := u.User.Username()
		if pass, ok := u.User.Password(); ok {
			auth += ":" + pass
		}
		if !strings.Contains(auth, ":") {
			b, err := decodeBase64(auth)
			if err != nil {
				return errNode
			}
			auth = string(b)
		}
		method, password, ok := strings.Cut(auth, ":")
		if !ok || method == "" || password == "" {
			return errNode
		}
	}
	return destination(u.Hostname(), u.Fragment)
}

func makeURI(scheme, host string, portValue any, credential string, query url.Values, name string) (string, error) {
	if err := destination(host, name); err != nil {
		return "", err
	}
	p, err := port(portValue)
	if err != nil {
		return "", err
	}
	auth := ""
	if credential != "" {
		auth = escape(credential) + "@"
	}
	link := scheme + "://" + auth + net.JoinHostPort(strings.Trim(host, "[]"), p)
	if len(query) > 0 {
		link += "?" + strings.ReplaceAll(query.Encode(), "+", "%20")
	}
	if name != "" {
		link += "#" + escape(name)
	}
	return link, nil
}

func (c *conversion) warn(message string) {
	for _, w := range c.Warnings {
		if w == message {
			return
		}
	}
	c.Warnings = append(c.Warnings, message)
}
func (c *conversion) add(link string) error {
	if err := validateLink(link); err != nil {
		return err
	}
	if len(c.Links) >= 10000 {
		return errTooMany
	}
	c.Links = append(c.Links, link)
	return nil
}
func (c *conversion) unknown(m map[string]any, keys ...string) {
	allowed := make(map[string]bool, len(keys))
	for _, k := range keys {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			c.warn("Часть полей JSON не представима в стандартных URI; сохраните исходный JSON.")
			return
		}
	}
}

func (c *conversion) walk(v any, depth int, name string) error {
	if depth > 16 {
		return errors.New("слишком глубокая вложенность JSON")
	}
	switch v := v.(type) {
	case string:
		return c.add(strings.TrimSpace(v))
	case []any:
		for _, child := range v {
			if err := c.walk(child, depth+1, name); err != nil {
				if errors.Is(err, errTooMany) {
					return err
				}
				c.warn("Пропущены некорректные или неподдерживаемые узлы.")
			}
		}
		return nil
	case map[string]any:
		name = first(text(v["remarks"]), text(v["name"]), name)
		if outbounds, ok := v["outbounds"]; ok {
			if _, ok := outbounds.([]any); !ok {
				return errNode
			}
			c.warn("Маршрутизация, DNS, локальные входы и цепочки JSON не переносятся в отдельные URI.")
			return c.walk(outbounds, depth+1, name)
		}
		if _, ok := v["protocol"]; ok {
			return c.outbound(v, name)
		}
		if v["add"] != nil && v["port"] != nil && v["id"] != nil {
			return c.add("vmess://" + base64.StdEncoding.EncodeToString([]byte(compact(v))))
		}
		if v["server"] != nil && v["server_port"] != nil && v["method"] != nil && v["password"] != nil {
			return c.outbound(map[string]any{"protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": v["server"], "port": v["server_port"], "method": v["method"], "password": v["password"]}}}}, name)
		}
		if servers, ok := v["servers"].([]any); ok {
			return c.walk(servers, depth+1, name)
		}
	}
	return errNode
}

func withoutComments(text string) (string, error) {
	b := []byte(text)
	quoted, escaped := false, false
	for i := 0; i < len(b); i++ {
		if quoted {
			if escaped {
				escaped = false
			} else if b[i] == '\\' {
				escaped = true
			} else if b[i] == '"' {
				quoted = false
			}
			continue
		}
		if b[i] == '"' {
			quoted = true
			continue
		}
		if b[i] != '/' || i+1 >= len(b) {
			continue
		}
		if b[i+1] == '/' {
			b[i] = ' '
			i++
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
			continue
		}
		if b[i+1] == '*' {
			b[i], b[i+1] = ' ', ' '
			i += 2
			closed := false
			for ; i+1 < len(b); i++ {
				if b[i] == '*' && b[i+1] == '/' {
					b[i], b[i+1] = ' ', ' '
					i++
					closed = true
					break
				}
				if b[i] != '\n' && b[i] != '\r' {
					b[i] = ' '
				}
			}
			if !closed {
				return "", errors.New("незавершённый JSON-комментарий")
			}
		}
	}
	return string(b), nil
}

func parseSubscription(input string) (conversion, error) {
	c := conversion{}
	if len(input) > maxBytes || !utf8.ValidString(input) {
		return c, errors.New("некорректная подписка (UTF-8, максимум 8 МиБ)")
	}
	for layer := 0; layer <= 4; layer++ {
		input = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "\ufeff"))
		if strings.HasPrefix(input, "/*") {
			var err error
			input, err = withoutComments(input)
			if err != nil {
				return c, err
			}
			input = strings.TrimSpace(input)
		}
		if input == "" {
			return c, errors.New("подписка пуста")
		}
		if strings.ContainsAny(input[:1], "{[\"") {
			clean, err := withoutComments(input)
			if err != nil {
				return c, err
			}
			var data any
			d := json.NewDecoder(strings.NewReader(clean))
			d.UseNumber()
			if d.Decode(&data) != nil || d.Decode(new(any)) != io.EOF {
				return c, errors.New("некорректный JSON подписки")
			}
			if err := c.walk(data, 0, ""); err != nil {
				return c, err
			}
			break
		}
		found := false
		filtered := make([]string, 0)
		for _, line := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "HTTP/") {
				continue
			}
			key, _, _ := strings.Cut(line, ":")
			switch strings.ToLower(key) {
			case "profile-title", "profile-update-interval", "subscription-userinfo", "support-url", "profile-web-page-url", "content-type", "content-length", "content-disposition", "cache-control", "date", "server", "connection", "etag", "last-modified", "announce", "announce-url":
				continue
			}
			if strings.Contains(line, "://") {
				found = true
			}
			filtered = append(filtered, line)
		}
		if found {
			for _, line := range filtered {
				if err := c.add(line); err != nil {
					if errors.Is(err, errTooMany) {
						return c, err
					}
					c.warn("Пропущены некорректные или служебные ссылки.")
				}
			}
			break
		}
		if layer == 4 {
			return c, errors.New("неизвестный формат или более 4 слоёв Base64")
		}
		b, err := decodeBase64(strings.Join(filtered, "\n"))
		if err != nil || !utf8.Valid(b) {
			return c, errors.New("ожидается URI-подписка, Base64 или Xray JSON")
		}
		input = string(b)
	}
	if len(c.Links) == 0 {
		return c, errors.New("источник не вернул пригодных конфигураций; возможно, это ограничение доступа")
	}
	seen := make(map[string]bool)
	unique := make([]string, 0, len(c.Links))
	for _, link := range c.Links {
		if !seen[link] {
			unique = append(unique, link)
			seen[link] = true
		}
	}
	c.Links = unique
	return c, nil
}

func exportLinks(links []string, mode, protocol string) []string {
	out := make([]string, 0, len(links))
	for _, link := range links {
		if protocol == "vless" && !strings.HasPrefix(strings.ToLower(link), "vless://") {
			continue
		}
		if mode == "v2rayn" && strings.HasPrefix(strings.ToLower(link), "vless://") {
			head, frag, hasFrag := strings.Cut(link, "#")
			prefix, query, hasQuery := strings.Cut(head, "?")
			parts := strings.Split(query, "&")
			reality := false
			for _, part := range parts {
				k, v, _ := strings.Cut(part, "=")
				k, _ = url.QueryUnescape(k)
				v, _ = url.QueryUnescape(v)
				if k == "security" && v == "reality" {
					reality = true
				}
			}
			if hasQuery && reality {
				for i, part := range parts {
					k, v, eq := strings.Cut(part, "=")
					dk, _ := url.QueryUnescape(k)
					dv, _ := url.QueryUnescape(v)
					if eq && dk == "fp" && dv == "qq" {
						parts[i] = k + "=firefox"
					}
				}
				link = prefix + "?" + strings.Join(parts, "&")
				if hasFrag {
					link += "#" + frag
				}
			}
		}
		out = append(out, link)
	}
	return out
}
