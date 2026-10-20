package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
)

// A nil selection includes all current and future configurations. A non-nil,
// empty selection includes none. Whole sources also include future nodes.
type configurationSelection struct {
	Sources []string                 `json:"sources"`
	Nodes   []configurationReference `json:"nodes"`
}

type configurationReference struct {
	SourceID string `json:"source_id"`
	NodeID   string `json:"node_id"`
}

type configurationChange struct {
	configurationReference
	Selected bool `json:"selected"`
}

func (s *configurationSelection) contains(ref configurationReference) bool {
	if s == nil {
		return true
	}
	for _, source := range s.Sources {
		if source == ref.SourceID {
			return true
		}
	}
	for _, node := range s.Nodes {
		if node == ref {
			return true
		}
	}
	return false
}

func configurationID(link string) string {
	return configurationIdentityID(link, false)
}

func configurationIdentityID(link string, includeAddress bool) string {
	node := describeConfiguration(link)
	identity := struct{ Protocol, Name, Address, Port, Network, Security, Flow, Host, Path, ServerName, ServiceName string }{Protocol: node.Protocol, Name: strings.TrimSpace(node.Name), Address: strings.ToLower(node.Address), Port: node.Port}
	named := false
	if u, err := url.Parse(link); err == nil {
		named = strings.TrimSpace(u.Fragment) != ""
		q := u.Query()
		identity.Network = q.Get("type")
		identity.Security = q.Get("security")
		identity.Flow = q.Get("flow")
		identity.Host = q.Get("host")
		identity.Path = q.Get("path")
		identity.ServerName = q.Get("sni")
		identity.ServiceName = q.Get("serviceName")
	}
	if strings.HasPrefix(link, "vmess://") {
		if raw, err := decodeBase64(strings.TrimPrefix(link, "vmess://")); err == nil {
			var v struct {
				Name string `json:"ps"`
				Net  string `json:"net"`
				TLS  string `json:"tls"`
				Host string `json:"host"`
				Path string `json:"path"`
				SNI  string `json:"sni"`
			}
			if json.Unmarshal(raw, &v) == nil {
				named = strings.TrimSpace(v.Name) != ""
				identity.Network, identity.Security, identity.Host, identity.Path, identity.ServerName = v.Net, v.TLS, v.Host, v.Path, v.SNI
			}
		}
	}
	if identity.Network == "" && (node.Protocol == "vless" || node.Protocol == "vmess" || node.Protocol == "trojan") {
		identity.Network = "tcp"
	}
	if named && !includeAddress {
		identity.Address = ""
	}
	encoded, _ := json.Marshal(identity)
	return contentID(string(encoded))
}

// Named nodes can move to another IP. If several nodes share the same name
// and transport, retain their addresses to keep individual selection precise.
func configurationIDs(links []string) map[string]string {
	endpoints := make(map[string]string)
	ambiguous := make(map[string]bool)
	ids := make(map[string]string, len(links))
	for _, link := range links {
		id, endpoint := configurationID(link), configurationIdentityID(link, true)
		if previous, ok := endpoints[id]; ok && previous != endpoint {
			ambiguous[id] = true
		}
		endpoints[id] = endpoint
		ids[link] = id
	}
	for link, id := range ids {
		if ambiguous[id] {
			ids[link] = configurationIdentityID(link, true)
		}
	}
	return ids
}

func contentID(link string) string {
	sum := sha256.Sum256([]byte(link))
	return hex.EncodeToString(sum[:])
}

// Old versions selected by the full URI hash. Migrate against the loaded cache
// before refreshing it, so provider credential changes keep the same selection.
func (b *bridge) migrateConfigurationSelection() {
	p := b.config.Panel
	if p == nil || p.ConfigSelection == nil {
		return
	}
	panel := *p
	selection := *p.ConfigSelection
	selection.Sources = slices.Clone(selection.Sources)
	selection.Nodes = slices.Clone(selection.Nodes)
	for i, ref := range selection.Nodes {
		member := b.sourceByID(ref.SourceID)
		if member == nil {
			continue
		}
		cached, _ := member.sourceSnapshot()
		ids := configurationIDs(cached.Links)
		for _, link := range cached.Links {
			if contentID(link) == ref.NodeID || configurationIdentityID(link, true) == ref.NodeID {
				selection.Nodes[i].NodeID = ids[link]
				break
			}
		}
	}
	panel.ConfigSelection = &selection
	b.config.Panel = &panel
}

func (s *configurationSelection) validate() error {
	if s == nil {
		return nil
	}
	if len(s.Sources) > 32 || len(s.Nodes) > 4096 {
		return errors.New("слишком много выбранных конфигов или подписок")
	}
	for _, source := range s.Sources {
		if !sourceIDPattern.MatchString(source) || strings.EqualFold(source, "all") {
			return errors.New("некорректный ID выбранной подписки")
		}
	}
	for _, node := range s.Nodes {
		id, err := hex.DecodeString(node.NodeID)
		if !sourceIDPattern.MatchString(node.SourceID) || strings.EqualFold(node.SourceID, "all") || err != nil || len(id) != sha256.Size || node.NodeID != strings.ToLower(node.NodeID) {
			return errors.New("некорректный ID выбранного конфига")
		}
	}
	return nil
}

func (b *bridge) panelSubscriptionURL() string {
	return b.config.PublicURL + "/sub/" + b.panelToken
}

// Filter original cache entries before applying the export profile. IDs therefore
// survive switching export profiles and the general subscription stays intact.
func (b *bridge) panelSnapshot() (cacheData, string) {
	selection := b.config.Panel
	if selection == nil || selection.ConfigSelection == nil {
		return b.snapshot()
	}
	s := selection.ConfigSelection
	sources, nodes := make(map[string]bool), make(map[configurationReference]bool)
	for _, id := range s.Sources {
		sources[id] = true
	}
	for _, node := range s.Nodes {
		nodes[node] = true
	}
	combined := cacheData{Version: 1, Links: []string{}, Warnings: []string{}}
	seen := make(map[string]bool)
	var issues []string
	for _, member := range b.subscriptions() {
		selectedSource := sources[member.ID]
		for _, ref := range s.Nodes {
			if ref.SourceID == member.ID {
				selectedSource = true
				break
			}
		}
		if !selectedSource {
			continue
		}
		cached, issue := member.Bridge.sourceSnapshot()
		ids := configurationIDs(cached.Links)
		if !cached.UpdatedAt.IsZero() && (combined.UpdatedAt.IsZero() || cached.UpdatedAt.Before(combined.UpdatedAt)) {
			combined.UpdatedAt = cached.UpdatedAt
		}
		if issue != "" {
			issues = append(issues, issue)
		}
		for _, link := range cached.Links {
			if (sources[member.ID] || nodes[configurationReference{member.ID, ids[link]}]) && !seen[link] {
				seen[link] = true
				combined.Links = append(combined.Links, link)
			}
		}
	}
	return combined, strings.Join(issues, "; ")
}
