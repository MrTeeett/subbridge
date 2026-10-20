package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func copyQuery(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (c *conversion) streamQuery(stream map[string]any) (url.Values, error) {
	network := first(text(stream["method"]), text(stream["network"]), "tcp")
	if alias := map[string]string{"raw": "tcp", "splithttp": "xhttp", "websocket": "ws", "mkcp": "kcp"}[network]; alias != "" {
		network = alias
	}
	switch network {
	case "tcp", "ws", "grpc", "httpupgrade", "xhttp", "kcp", "quic", "http", "h2":
	default:
		return nil, errNode
	}
	security := first(text(stream["security"]), "none")
	if security != "none" && security != "tls" && security != "reality" {
		return nil, errNode
	}
	q := url.Values{"type": {network}, "security": {security}}
	field := "tlsSettings"
	if security == "reality" {
		field = "realitySettings"
	}
	settings := object(stream[field])
	if security == "none" {
		settings = nil
	}
	mapping := map[string]string{"serverName": "sni", "fingerprint": "fp", "alpn": "alpn", "echConfigList": "ech", "verifyPeerCertByName": "vcn", "pinnedPeerCertSha256": "pcs"}
	if security == "reality" {
		for k, v := range map[string]string{"publicKey": "pbk", "shortId": "sid", "spiderX": "spx", "mldsa65Verify": "pqv", "pqv": "pqv"} {
			mapping[k] = v
		}
	}
	for key, param := range mapping {
		if value := text(settings[key]); value != "" {
			q.Set(param, value)
		}
	}
	if security == "reality" {
		if value := text(settings["password"]); value != "" {
			q.Set("pbk", value)
		}
		if value := text(settings["mldsa65Verify"]); value != "" {
			q.Set("pqv", value)
		}
	}
	if settings["allowInsecure"] == true {
		q.Set("allowInsecure", "1")
		c.warn("allowInsecure поддерживается не всеми импортерами.")
	}
	for _, key := range []string{"support-x25519mlkem768", "supportX25519MLKEM768"} {
		if v, ok := settings[key]; ok {
			q.Set("support-x25519mlkem768", text(v))
		}
	}
	allowed := []string{"password", "show", "allowInsecure", "support-x25519mlkem768", "supportX25519MLKEM768"}
	for k := range mapping {
		allowed = append(allowed, k)
	}
	c.unknown(settings, allowed...)
	transportField := network + "Settings"
	if network == "tcp" {
		transportField = "tcpSettings"
		if _, ok := stream["rawSettings"]; ok {
			transportField = "rawSettings"
		}
	}
	if network == "xhttp" {
		transportField = "splithttpSettings"
		if _, ok := stream["xhttpSettings"]; ok {
			transportField = "xhttpSettings"
		}
	}
	if network == "h2" {
		transportField = "httpSettings"
	}
	t := object(stream[transportField])
	if network == "ws" || network == "httpupgrade" || network == "xhttp" || network == "http" || network == "h2" {
		for _, key := range []string{"host", "path"} {
			if value := text(t[key]); value != "" {
				q.Set(key, value)
			}
		}
	}
	switch network {
	case "ws", "httpupgrade":
		for k, v := range object(t["headers"]) {
			if strings.EqualFold(k, "host") {
				if q.Get("host") == "" {
					q.Set("host", text(v))
				}
			} else {
				c.warn("Произвольные HTTP-заголовки не переносятся в URI.")
			}
		}
		c.unknown(t, "host", "path", "headers")
	case "xhttp":
		q.Set("mode", first(text(t["mode"]), "auto"))
		var extra map[string]any
		if v, ok := t["extra"]; ok && v != nil {
			if encoded, ok := v.(string); ok {
				d := json.NewDecoder(strings.NewReader(encoded))
				d.UseNumber()
				if d.Decode(&extra) != nil {
					return nil, errNode
				}
			} else {
				extra = object(v)
			}
			if extra == nil {
				return nil, errNode
			}
		} else {
			extra = make(map[string]any)
			for k, v := range t {
				if k != "host" && k != "path" && k != "extra" {
					extra[k] = v
				}
			}
		}
		if len(extra) > 0 {
			q.Set("extra", compact(extra))
		}
	case "grpc":
		for _, key := range []string{"serviceName", "authority"} {
			if value, ok := t[key]; ok {
				q.Set(key, text(value))
			}
		}
		mode := first(text(t["mode"]), "gun")
		if mode != "gun" && mode != "multi" && mode != "guna" {
			return nil, errNode
		}
		if v, ok := t["multiMode"]; ok {
			multi, ok := v.(bool)
			if !ok {
				return nil, errNode
			}
			mode = "gun"
			if multi {
				mode = "multi"
			}
		}
		q.Set("mode", mode)
		c.unknown(t, "serviceName", "authority", "mode", "multiMode")
	case "tcp":
		h := object(t["header"])
		if len(h) > 0 {
			q.Set("headerType", first(text(h["type"]), "none"))
			request := object(h["request"])
			for k, v := range object(request["headers"]) {
				if strings.EqualFold(k, "host") {
					q.Set("host", text(v))
				} else {
					c.warn("Произвольные TCP HTTP-заголовки не переносятся в URI.")
				}
			}
			if value := text(request["path"]); value != "" {
				q.Set("path", value)
			}
			c.unknown(request, "headers", "path")
			c.unknown(h, "type", "request")
		}
		c.unknown(t, "header")
	case "kcp", "quic":
		q.Set("headerType", first(text(object(t["header"])["type"]), "none"))
		fields := map[string]string{"seed": "seed", "mtu": "mtu"}
		if network == "quic" {
			fields = map[string]string{"security": "quicSecurity", "key": "key"}
		}
		for key, param := range fields {
			if v := text(t[key]); v != "" {
				q.Set(param, v)
			}
		}
		c.warn("KCP/QUIC URI требует совместимого ядра и не переносит все настройки транспорта.")
	default:
		c.unknown(t, "host", "path")
	}
	if mask, ok := stream["finalmask"]; ok && mask != nil {
		q.Set("fm", compact(mask))
		c.warn("finalmask сохраняется как расширение fm; клиент может его игнорировать.")
	}
	c.unknown(stream, "network", "method", "security", field, transportField, "finalmask")
	return q, nil
}

func plainStream(q url.Values) bool {
	return q.Get("security") == "none" && q.Get("type") == "tcp" && (len(q) == 2 || (len(q) == 3 && q.Get("headerType") == "none"))
}

func (c *conversion) hysteria(out map[string]any, name string) error {
	s := object(out["settings"])
	stream := object(out["streamSettings"])
	t := object(stream["hysteriaSettings"])
	auth := ""
	if value, exists := t["auth"]; exists {
		var ok bool
		auth, ok = value.(string)
		if !ok {
			return errNode
		}
	}
	version := text(s["version"])
	if version == "" && text(out["protocol"]) == "hysteria2" {
		version = "2"
	}
	if version != "2" || (text(t["version"]) != "" && text(t["version"]) != version) {
		return errNode
	}
	network := first(text(stream["method"]), text(stream["network"]), "hysteria")
	if (network != "hysteria" && network != "hysteria2") || first(text(stream["security"]), "tls") != "tls" {
		return errNode
	}
	tlsStream := map[string]any{"network": "tcp", "security": "tls", "tlsSettings": stream["tlsSettings"]}
	if fm := stream["finalmask"]; fm != nil {
		tlsStream["finalmask"] = fm
	}
	q, err := c.streamQuery(tlsStream)
	if err != nil {
		return err
	}
	q.Del("type")
	q.Del("security")
	if q.Has("allowInsecure") {
		q.Set("insecure", q.Get("allowInsecure"))
		q.Del("allowInsecure")
	}
	if pin := q.Get("pcs"); pin != "" && !strings.Contains(pin, ",") {
		q.Set("pinSHA256", pin)
		q.Del("pcs")
	}
	fm := object(stream["finalmask"])
	masks := array(fm["udp"])
	if len(masks) == 1 && text(object(masks[0])["type"]) == "salamander" {
		mask := object(object(masks[0])["settings"])
		password := text(mask["password"])
		if password == "" {
			return errNode
		}
		q.Set("obfs", "salamander")
		q.Set("obfs-password", password)
		if size := text(mask["packetSize"]); size != "" && size != "0" {
			bounds := strings.Split(size, "-")
			if len(bounds) > 2 {
				return errNode
			}
			low, err := strconv.Atoi(bounds[0])
			if err != nil {
				return errNode
			}
			high := low
			if len(bounds) == 2 {
				high, err = strconv.Atoi(bounds[1])
				if err != nil {
					return errNode
				}
			}
			if low > high {
				low, high = high, low
			}
			if low < 1 || high > 2048 {
				return errNode
			}
			q.Set("obfs", "gecko")
			q.Set("minPacketSize", strconv.Itoa(low))
			q.Set("maxPacketSize", strconv.Itoa(high))
		}
	}
	hop := object(object(fm["quicParams"])["udpHop"])
	if ports := text(hop["ports"]); ports != "" {
		q.Set("mport", ports)
	}
	c.unknown(s, "address", "port", "version")
	c.unknown(t, "version", "auth")
	c.unknown(out, "protocol", "tag", "remarks", "settings", "streamSettings")
	link, err := makeURI("hysteria2", text(s["address"]), s["port"], auth, q, name)
	if err != nil {
		return err
	}
	return c.add(link)
}

func vmessURI(server, user map[string]any, q url.Values, name string) (string, error) {
	p, err := port(server["port"])
	if err != nil || text(user["id"]) == "" {
		return "", errNode
	}
	m := map[string]string{"v": "2", "ps": name, "add": text(server["address"]), "port": p, "id": text(user["id"]), "aid": first(text(user["alterId"]), "0"), "scy": first(text(user["security"]), "auto"), "net": q.Get("type"), "type": first(q.Get("headerType"), "none"), "host": q.Get("host"), "path": q.Get("path"), "tls": q.Get("security")}
	if m["tls"] == "none" {
		m["tls"] = ""
	}
	switch q.Get("type") {
	case "grpc":
		m["type"], m["host"], m["path"] = q.Get("mode"), q.Get("authority"), q.Get("serviceName")
	case "xhttp":
		m["type"] = q.Get("mode")
	case "kcp":
		m["path"] = q.Get("seed")
	case "quic":
		m["host"], m["path"] = q.Get("quicSecurity"), q.Get("key")
	}
	for _, key := range []string{"sni", "fp", "alpn", "vcn", "pcs"} {
		if q.Has(key) {
			m[key] = q.Get(key)
		}
	}
	if q.Has("allowInsecure") {
		m["insecure"] = q.Get("allowInsecure")
	}
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(compact(m))), nil
}

func (c *conversion) outbound(out map[string]any, name string) error {
	protocol := text(out["protocol"])
	name = first(text(out["remarks"]), name, text(out["tag"]), protocol)
	switch protocol {
	case "freedom", "blackhole", "dns", "loopback":
		return nil
	case "hysteria", "hysteria2":
		return c.hysteria(out, name)
	case "vless", "vmess", "trojan", "shadowsocks", "socks", "http":
	default:
		return errNode
	}
	s := object(out["settings"])
	q, err := c.streamQuery(object(out["streamSettings"]))
	if err != nil {
		return err
	}
	listKey := "servers"
	if protocol == "vless" || protocol == "vmess" {
		listKey = "vnext"
	}
	servers := array(s[listKey])
	if len(servers) == 0 {
		return errNode
	}
	c.unknown(s, listKey)
	c.unknown(out, "protocol", "tag", "remarks", "settings", "streamSettings")
	for si, raw := range servers {
		server := object(raw)
		users := array(server["users"])
		if protocol != "vless" && protocol != "vmess" && protocol != "socks" && protocol != "http" {
			users = []any{map[string]any{}}
		}
		if len(users) == 0 {
			if protocol == "socks" || protocol == "http" {
				users = []any{map[string]any{}}
			} else {
				c.warn("Пропущен узел без пользователей.")
				continue
			}
		}
		for ui, rawUser := range users {
			user := object(rawUser)
			display := name
			if si > 0 || ui > 0 {
				display = fmt.Sprintf("%s [%d:%d]", name, si+1, ui+1)
			}
			nq := copyQuery(q)
			scheme, credential := protocol, ""
			var link string
			switch protocol {
			case "vless":
				credential = text(user["id"])
				nq.Set("encryption", first(text(user["encryption"]), "none"))
				if flow := text(user["flow"]); flow != "" {
					nq.Set("flow", flow)
				}
				c.unknown(user, "id", "encryption", "flow", "email", "level")
			case "vmess":
				link, err = vmessURI(server, user, nq, display)
				represented := map[string]bool{}
				for _, key := range []string{"type", "security", "headerType", "host", "path", "mode", "authority", "serviceName", "seed", "quicSecurity", "key", "sni", "fp", "alpn", "vcn", "pcs", "allowInsecure"} {
					represented[key] = true
				}
				for key := range nq {
					if !represented[key] {
						c.warn("Часть параметров транспорта не представима в VMess JSON URI.")
					}
				}
				c.unknown(user, "id", "alterId", "security", "email", "level")
			case "trojan":
				credential = text(server["password"])
				if flow := text(server["flow"]); flow != "" {
					nq.Set("flow", flow)
				}
			case "shadowsocks":
				if !plainStream(q) || text(server["method"]) == "" || text(server["password"]) == "" {
					c.warn("Shadowsocks с плагином или некорректными параметрами пропущен.")
					continue
				}
				scheme = "ss"
				credential = base64.RawURLEncoding.EncodeToString([]byte(text(server["method"]) + ":" + text(server["password"])))
				nq = nil
			case "socks", "http":
				if !plainStream(q) {
					c.warn("SOCKS/HTTP с дополнительным транспортом пропущен.")
					continue
				}
				nq = nil
				username, password := text(user["user"]), text(user["pass"])
				if username != "" || password != "" {
					credential = base64.RawURLEncoding.EncodeToString([]byte(username + ":" + password))
				}
				if protocol == "http" {
					if credential == "" {
						c.warn("HTTP без авторизации пропущен.")
						continue
					}
					credential = username + ":" + password
				}
			}
			if protocol != "vmess" {
				if credential == "" && protocol != "socks" {
					c.warn("Пропущен узел без идентификатора.")
					continue
				}
				link, err = makeURI(scheme, text(server["address"]), server["port"], credential, nq, display)
				if protocol == "http" && err == nil {
					link = strings.Replace(link, escape(credential)+"@", url.UserPassword(text(user["user"]), text(user["pass"])).String()+"@", 1)
				}
			}
			if err == nil {
				err = c.add(link)
			}
			if errors.Is(err, errTooMany) {
				return err
			}
			if err != nil {
				c.warn("Пропущены некорректные или служебные конфигурации.")
			}
			err = nil
		}
	}
	return nil
}
