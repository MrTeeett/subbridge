package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func directMarkerPrefix(owner string) string { return "subbridge-" + owner + "-" }

// The URI's display name carries installation ownership. 3x-ui uses the remark
// field as the public name, so credentials and transport settings stay intact.
func directNodeValue(link, owner string) (string, error) {
	return directNodeValueWithID(link, owner, configurationID(link))
}

func directNodeValueWithID(link, owner, id string) (string, error) {
	marker := directMarkerPrefix(owner) + id
	if strings.HasPrefix(link, "vmess://") {
		body, err := decodeBase64(strings.TrimPrefix(link, "vmess://"))
		if err != nil {
			return "", errors.New("не удалось подготовить VMess для 3x-ui")
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(body, &value) != nil || value == nil {
			return "", errors.New("некорректный VMess для 3x-ui")
		}
		value["ps"], _ = json.Marshal(marker)
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(encoded), nil
	}
	return strings.SplitN(link, "#", 2)[0] + "#" + marker, nil
}

func ownedDirectValue(value, owner string) bool { return ownedDirectID(value, owner) != "" }

func ownedDirectID(value, owner string) string {
	marker := ""
	if strings.HasPrefix(value, "vmess://") {
		body, err := decodeBase64(strings.TrimPrefix(value, "vmess://"))
		if err != nil {
			return ""
		}
		var v struct {
			Name string `json:"ps"`
		}
		if json.Unmarshal(body, &v) != nil {
			return ""
		}
		marker = v.Name
	} else if u, err := url.Parse(value); err == nil {
		marker = u.Fragment
	}
	id, ok := strings.CutPrefix(marker, directMarkerPrefix(owner))
	if !ok {
		return ""
	}
	decoded, err := hex.DecodeString(id)
	if err == nil && len(decoded) == 32 && id == strings.ToLower(id) {
		return id
	}
	return ""
}

type directNode struct{ value, name string }

func (p *panelClient) syncDirect(ctx context.Context, links []string, owner string, legacy []string, dryRun bool) (syncResult, error) {
	var result syncResult
	nodes := make([]directNode, 0, len(links))
	ids := configurationIDs(links)
	seen := map[string]bool{}
	for _, link := range links {
		if seen[link] {
			continue
		}
		seen[link] = true
		node := describeConfiguration(link)
		switch node.Protocol {
		case "vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "wireguard", "wg":
		default:
			return result, errors.New("3x-ui: протокол не поддерживает прямую передачу; выберите режим ссылки подписки")
		}
		value, err := directNodeValueWithID(link, owner, ids[link])
		if err != nil {
			return result, err
		}
		name := node.Name
		if strings.TrimSpace(p.config.NamePrefix) != "" {
			name = p.config.NamePrefix + name
		}
		name = strings.TrimSpace(name)
		nodes = append(nodes, directNode{value, name})
	}
	emails, err := p.selected(ctx)
	if err != nil {
		return result, err
	}
	result.Selected = len(emails)
	for _, email := range emails {
		if err := ctx.Err(); err != nil {
			return result, safeFailure("синхронизация панели отменена", err)
		}
		added, changed, err := p.replaceDirect(ctx, email, nodes, owner, legacy, dryRun)
		if err != nil {
			result.Failed++
			if result.Error == "" {
				result.Error = err.Error()
			}
		} else if added {
			result.Added++
		} else if changed {
			result.Updated++
		} else {
			result.Unchanged++
		}
	}
	return result, nil
}

func (p *panelClient) replaceDirect(ctx context.Context, email string, nodes []directNode, owner string, legacy []string, dryRun bool) (bool, bool, error) {
	key := url.PathEscape(email)
	var record map[string]json.RawMessage
	if err := p.request(ctx, "get/"+key, nil, &record); err != nil {
		return false, false, err
	}
	raw, ok := record["externalLinks"]
	if !ok {
		return false, false, errors.New("API клиента не поддерживает внешние конфиги")
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return false, false, errors.New("некорректный список внешних конфигов 3x-ui")
	}
	before, err := json.Marshal(rows)
	if err != nil {
		return false, false, err
	}
	managed := map[string]map[string]json.RawMessage{}
	keep := make([]map[string]json.RawMessage, 0, len(rows)+len(nodes))
	insertAt := -1
	defaultEnabled := true
	for _, row := range rows {
		var kind, value, remark string
		if json.Unmarshal(row["kind"], &kind) != nil || json.Unmarshal(row["value"], &value) != nil || (kind != "link" && kind != "subscription") {
			return false, false, errors.New("некорректная внешняя ссылка 3x-ui")
		}
		_ = json.Unmarshal(row["remark"], &remark)
		if kind == "link" && ownedDirectValue(value, owner) {
			if insertAt < 0 {
				insertAt = len(keep)
			}
			managed[ownedDirectID(value, owner)] = row
			continue
		}
		old := false
		if kind == "subscription" && remark == "SubBridge" {
			for _, address := range legacy {
				if value == address {
					old = true
				}
			}
		}
		if old {
			if insertAt < 0 {
				insertAt = len(keep)
			}
			var enabled bool
			if json.Unmarshal(row["enable"], &enabled) == nil && !enabled {
				defaultEnabled = false
			}
			continue
		}
		keep = append(keep, row)
	}
	if insertAt < 0 {
		insertAt = len(keep)
	}
	next := make([]map[string]json.RawMessage, 0, len(keep)+len(nodes))
	next = append(next, keep[:insertAt]...)
	for _, node := range nodes {
		row := managed[ownedDirectID(node.value, owner)]
		if row == nil {
			row = map[string]json.RawMessage{}
			row["enable"], _ = json.Marshal(defaultEnabled)
			row["expiryTime"] = json.RawMessage("0")
		}
		for k, v := range map[string]string{"kind": "link", "value": node.value, "remark": node.name} {
			row[k], _ = json.Marshal(v)
		}
		var prefix string
		if json.Unmarshal(row["namePrefix"], &prefix) == nil && prefix != "" {
			row["namePrefix"] = json.RawMessage(`""`)
		}
		next = append(next, row)
	}
	next = append(next, keep[insertAt:]...)
	after, err := json.Marshal(next)
	if err != nil {
		return false, false, err
	}
	if bytes.Equal(before, after) {
		return false, false, nil
	}
	if !dryRun {
		if err := p.request(ctx, key+"/externalLinks", map[string]any{"externalLinks": next}, nil); err != nil {
			return false, false, err
		}
	}
	return len(managed) == 0 && len(nodes) > 0, true, nil
}
