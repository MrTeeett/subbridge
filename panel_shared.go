package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const sharedMarkerPrefix = "subbridge-shared-"

type sharedNode struct {
	key, uri, name, connection, account string
	refs                                []configurationReference
	selected                            bool
	action                              bool
	updated                             time.Time
}

type sharedCatalog struct {
	nodes           []sharedNode
	byConnection    map[string]int
	byKey, byLegacy map[string][]int
	limited         bool
}

func (c *sharedCatalog) limitChanges(changes []configurationChange) {
	c.limited = true
	for i := range c.nodes {
		c.nodes[i].action = false
		for _, ref := range c.nodes[i].refs {
			for _, change := range changes {
				if ref == change.configurationReference {
					c.nodes[i].action, c.nodes[i].selected = true, change.Selected
				}
			}
		}
	}
}

func linkMarker(link string) string {
	if strings.HasPrefix(link, "vmess://") {
		raw, err := decodeBase64(strings.TrimPrefix(link, "vmess://"))
		if err != nil {
			return ""
		}
		var v struct {
			Name string `json:"ps"`
		}
		if json.Unmarshal(raw, &v) == nil {
			return v.Name
		}
		return ""
	}
	if u, err := url.Parse(link); err == nil {
		return u.Fragment
	}
	return ""
}

// Display names and query ordering do not change the actual connection.
func connectionID(link string) string {
	if strings.HasPrefix(link, "vmess://") {
		raw, err := decodeBase64(strings.TrimPrefix(link, "vmess://"))
		if err != nil {
			return ""
		}
		var v map[string]any
		if json.Unmarshal(raw, &v) != nil || v == nil {
			return ""
		}
		delete(v, "ps")
		encoded, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return contentID(string(encoded))
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" {
		return ""
	}
	u.Fragment = ""
	u.RawFragment = ""
	q := u.Query()
	u.RawQuery = q.Encode()
	u.Host = strings.ToLower(u.Host)
	return contentID(u.String())
}

func connectionAccount(link string) string {
	if strings.HasPrefix(link, "vmess://") {
		raw, err := decodeBase64(strings.TrimPrefix(link, "vmess://"))
		if err != nil {
			return ""
		}
		var v struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &v) == nil && v.ID != "" {
			return contentID("vmess:" + v.ID)
		}
		return ""
	}
	u, err := url.Parse(link)
	if err == nil && u.User != nil && u.User.String() != "" {
		return contentID(u.Scheme + ":" + u.User.String())
	}
	return ""
}

func managedMarker(marker string) bool {
	if !strings.HasPrefix(marker, "subbridge-") {
		return false
	}
	at := strings.LastIndexByte(marker, '-')
	if at < 10 {
		return false
	}
	id := marker[at+1:]
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 32 && id == strings.ToLower(id)
}

func sharedMarkerKey(marker string) (string, int64) {
	value, ok := strings.CutPrefix(marker, sharedMarkerPrefix)
	if !ok || !managedMarker(marker) {
		return "", 0
	}
	parts := strings.Split(value, "-")
	if len(parts) == 2 {
		return value, 0
	}
	if len(parts) == 3 {
		stamp, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil && stamp > 0 {
			return parts[0] + "-" + parts[2], stamp
		}
	}
	return "", 0
}

func (b *bridge) sharedCatalog() sharedCatalog {
	catalog := sharedCatalog{byConnection: map[string]int{}, byKey: map[string][]int{}, byLegacy: map[string][]int{}}
	selection := (*configurationSelection)(nil)
	if b.config.Panel != nil {
		selection = b.config.Panel.ConfigSelection
	}
	for _, member := range b.subscriptions() {
		cached, _ := member.Bridge.sourceSnapshot()
		ids := configurationIDs(cached.Links)
		scope := contentID(member.Bridge.config.Source.URL)[:16]
		for _, raw := range cached.Links {
			ref := configurationReference{member.ID, ids[raw]}
			chosen := selection == nil
			if selection != nil {
				for _, id := range selection.Sources {
					chosen = chosen || id == member.ID
				}
				for _, r := range selection.Nodes {
					chosen = chosen || r == ref
				}
			}
			for _, link := range exportLinks([]string{raw}, b.config.ExportProfile, "all") {
				connection := connectionID(link)
				if connection == "" {
					continue
				}
				key := scope + "-" + ref.NodeID
				index, ok := catalog.byConnection[connection]
				if !ok {
					index = len(catalog.nodes)
					catalog.byConnection[connection] = index
					node := describeConfiguration(link)
					catalog.nodes = append(catalog.nodes, sharedNode{key: key, uri: link, name: node.Name, connection: connection, account: connectionAccount(link), updated: cached.UpdatedAt, action: true})
				}
				n := &catalog.nodes[index]
				n.selected = n.selected || chosen
				n.refs = append(n.refs, ref)
				catalog.byKey[key] = appendUniqueIndex(catalog.byKey[key], index)
				catalog.byLegacy[ref.NodeID] = appendUniqueIndex(catalog.byLegacy[ref.NodeID], index)
				legacy := configurationID(link)
				catalog.byLegacy[legacy] = appendUniqueIndex(catalog.byLegacy[legacy], index)
			}
		}
	}
	return catalog
}

func appendUniqueIndex(indices []int, index int) []int {
	for _, i := range indices {
		if i == index {
			return indices
		}
	}
	return append(indices, index)
}

func (c sharedCatalog) match(value, owner string) int {
	if index, ok := c.byConnection[connectionID(value)]; ok {
		return index
	}
	marker := linkMarker(value)
	if key, _ := sharedMarkerKey(marker); key != "" {
		candidates := c.byKey[key]
		if len(candidates) == 1 {
			return candidates[0]
		}
		account := connectionAccount(value)
		for _, i := range candidates {
			if account != "" && account == c.nodes[i].account {
				return i
			}
		}
	}
	if managedMarker(marker) {
		id := marker[strings.LastIndexByte(marker, '-')+1:]
		candidates := c.byLegacy[id]
		account := connectionAccount(value)
		if ownedDirectValue(value, owner) && len(candidates) == 1 {
			return candidates[0]
		}
		for _, i := range candidates {
			if account != "" && account == c.nodes[i].account {
				return i
			}
		}
	}
	return -1
}

func rowStrings(row map[string]json.RawMessage) (kind, value string, err error) {
	if json.Unmarshal(row["kind"], &kind) != nil || json.Unmarshal(row["value"], &value) != nil || (kind != "link" && kind != "subscription") {
		return "", "", errors.New("некорректная внешняя ссылка 3x-ui")
	}
	return kind, value, nil
}

func readExternalLinks(ctx context.Context, p *panelClient, email string) ([]map[string]json.RawMessage, error) {
	var record map[string]json.RawMessage
	if err := p.request(ctx, "get/"+url.PathEscape(email), nil, &record); err != nil {
		return nil, err
	}
	raw, ok := record["externalLinks"]
	if !ok {
		return nil, errors.New("API клиента не поддерживает внешние конфиги")
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return nil, errors.New("некорректный список внешних конфигов 3x-ui")
	}
	return rows, nil
}

func externalActive(row map[string]json.RawMessage) bool {
	enabled := true
	_ = json.Unmarshal(row["enable"], &enabled)
	var expiry int64
	_ = json.Unmarshal(row["expiryTime"], &expiry)
	return enabled && (expiry <= 0 || expiry > time.Now().UnixMilli())
}

func sharedNodeValue(n sharedNode, marker string) (string, error) {
	// Reuse the URI encoder, retaining every connection parameter.
	if marker == "" {
		scope, id, _ := strings.Cut(n.key, "-")
		marker = sharedMarkerPrefix + scope + "-" + strconv.FormatInt(n.updated.UnixMilli(), 10) + "-" + id
	}
	return directNodeValueWithID(n.uri, "shared", strings.TrimPrefix(marker, sharedMarkerPrefix))
}

func (p *panelClient) syncShared(ctx context.Context, c sharedCatalog, owner string, legacy []string, explicit, dryRun bool) (syncResult, error) {
	var result syncResult
	for _, n := range c.nodes {
		if explicit && !n.action {
			continue
		}
		if !n.selected && explicit {
			continue
		}
		switch describeConfiguration(n.uri).Protocol {
		case "vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "wireguard", "wg":
		default:
			if explicit && n.selected {
				return result, errors.New("3x-ui: протокол не поддерживает прямую передачу; выберите режим ссылки подписки")
			}
		}
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
		added, changed, err := p.replaceShared(ctx, email, c, owner, legacy, explicit, dryRun)
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

func (p *panelClient) replaceShared(ctx context.Context, email string, c sharedCatalog, owner string, legacy []string, explicit, dryRun bool) (bool, bool, error) {
	rows, err := readExternalLinks(ctx, p, email)
	if err != nil {
		return false, false, err
	}
	before, err := json.Marshal(rows)
	if err != nil {
		return false, false, err
	}
	indices := make([]int, len(rows))
	managed := make([]bool, len(rows))
	removeLegacy := make([]bool, len(rows))
	preferred := map[int]int{}
	manual := map[int]bool{}
	for i, row := range rows {
		indices[i] = -1
		kind, value, err := rowStrings(row)
		if err != nil {
			return false, false, err
		}
		if kind == "subscription" {
			var remark string
			_ = json.Unmarshal(row["remark"], &remark)
			if explicit && !c.limited && remark == "SubBridge" {
				for _, address := range legacy {
					if value == address {
						removeLegacy[i] = true
					}
				}
			}
			continue
		}
		index := c.match(value, owner)
		indices[i] = index
		if index < 0 {
			continue
		}
		managed[i] = managedMarker(linkMarker(value))
		if !managed[i] {
			manual[index] = true
			continue
		}
		previous, ok := preferred[index]
		if !ok || !externalActive(rows[previous]) && externalActive(row) {
			preferred[index] = i
		}
	}
	next := make([]map[string]json.RawMessage, 0, len(rows)+len(c.nodes))
	emitted := map[int]bool{}
	added := false
	prepare := func(n sharedNode, row map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		if row == nil {
			row = map[string]json.RawMessage{"enable": json.RawMessage("true"), "expiryTime": json.RawMessage("0")}
		}
		var oldValue string
		_ = json.Unmarshal(row["value"], &oldValue)
		marker := linkMarker(oldValue)
		_, stamp := sharedMarkerKey(marker)
		if stamp == 0 {
			marker = ""
		}
		value, err := sharedNodeValue(n, "")
		if err != nil {
			return nil, err
		}
		row["kind"], _ = json.Marshal("link")
		if marker != "" && (connectionID(oldValue) == n.connection || stamp > n.updated.UnixMilli()) {
			value = oldValue
		}
		row["value"], _ = json.Marshal(value)
		if explicit || len(row["remark"]) == 0 {
			row["remark"], _ = json.Marshal(strings.TrimSpace(p.config.NamePrefix + n.name))
		}
		var prefix string
		if json.Unmarshal(row["namePrefix"], &prefix) == nil && prefix != "" {
			row["namePrefix"] = json.RawMessage(`""`)
		}
		return row, nil
	}
	for i, row := range rows {
		if removeLegacy[i] {
			continue
		}
		index := indices[i]
		if index < 0 || !managed[i] {
			next = append(next, row)
			continue
		}
		if explicit && !c.nodes[index].action {
			next = append(next, row)
			continue
		}
		if manual[index] || explicit && !c.nodes[index].selected || preferred[index] != i {
			continue
		}
		updated, err := prepare(c.nodes[index], row)
		if err != nil {
			return false, false, err
		}
		next = append(next, updated)
		emitted[index] = true
	}
	if explicit {
		for i, n := range c.nodes {
			if !n.action || !n.selected || emitted[i] || manual[i] {
				continue
			}
			row, err := prepare(n, nil)
			if err != nil {
				return false, false, err
			}
			next = append(next, row)
			added = true
		}
	}
	after, err := json.Marshal(next)
	if err != nil {
		return false, false, err
	}
	if len(rows) == 0 && len(next) == 0 || bytes.Equal(before, after) {
		return false, false, nil
	}
	if !dryRun {
		if err := p.request(ctx, url.PathEscape(email)+"/externalLinks", map[string]any{"externalLinks": next}, nil); err != nil {
			return false, false, err
		}
	}
	return added, true, nil
}
