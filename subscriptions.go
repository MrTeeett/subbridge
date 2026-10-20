package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

type subscriptionMember struct {
	ID     string
	Name   string
	Bridge *bridge
}

func (b *bridge) subscriptions() []subscriptionMember {
	members := make([]subscriptionMember, 0, len(b.sources)+1)
	if b.config.Source.URL != "" || len(b.sources) == 0 {
		members = append(members, subscriptionMember{"default", b.config.SourceName, b})
	}
	for i, child := range b.sources {
		source := b.config.Sources[i]
		members = append(members, subscriptionMember{source.ID, source.Name, child})
	}
	return members
}

func (b *bridge) hasSource() bool {
	for _, member := range b.subscriptions() {
		if strings.TrimSpace(member.Bridge.config.Source.URL) != "" {
			return true
		}
	}
	return false
}

func (b *bridge) sourceByID(id string) *bridge {
	for _, member := range b.subscriptions() {
		if member.ID == id {
			return member.Bridge
		}
	}
	return nil
}

func (b *bridge) snapshot() (cacheData, string) {
	if len(b.sources) == 0 {
		return b.sourceSnapshot()
	}
	combined := cacheData{Version: 1, Links: []string{}, Warnings: []string{}}
	seen := map[string]bool{}
	var issues []error
	for _, member := range b.subscriptions() {
		cached, issue := member.Bridge.sourceSnapshot()
		for _, link := range cached.Links {
			if !seen[link] {
				seen[link] = true
				combined.Links = append(combined.Links, link)
			}
		}
		combined.Warnings = append(combined.Warnings, cached.Warnings...)
		if !cached.UpdatedAt.IsZero() && (combined.UpdatedAt.IsZero() || cached.UpdatedAt.Before(combined.UpdatedAt)) {
			combined.UpdatedAt = cached.UpdatedAt
		}
		if issue != "" {
			issues = append(issues, errors.New(issue))
		}
	}
	if err := errors.Join(issues...); err != nil {
		return combined, err.Error()
	}
	return combined, ""
}

func (b *bridge) refresh(ctx context.Context) error {
	if len(b.sources) == 0 {
		return b.refreshSource(ctx)
	}
	b.groupRefreshMu.Lock()
	defer b.groupRefreshMu.Unlock()
	members := b.subscriptions()
	failures := make([]error, len(members))
	var workers sync.WaitGroup
	for i, member := range members {
		if strings.TrimSpace(member.Bridge.config.Source.URL) == "" {
			continue
		}
		workers.Go(func() { failures[i] = member.Bridge.refreshSource(ctx) })
	}
	workers.Wait()
	return errors.Join(failures...)
}

type configurationNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     string `json:"port"`
	URI      string `json:"uri"`
}

type configurationGroup struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	UpdatedAt       time.Time           `json:"updated_at"`
	Issue           string              `json:"issue"`
	HWID            string              `json:"hwid"`
	SubscriptionURL string              `json:"subscription_url"`
	Nodes           []configurationNode `json:"nodes"`
}

func describeConfiguration(link string) configurationNode {
	node := configurationNode{URI: link}
	u, err := url.Parse(link)
	if err == nil {
		node.Protocol, node.Name, node.Address, node.Port = u.Scheme, u.Fragment, u.Hostname(), u.Port()
	}
	if strings.HasPrefix(link, "vmess://") {
		if decoded, err := decodeBase64(strings.TrimPrefix(link, "vmess://")); err == nil {
			var value struct {
				Name    string          `json:"ps"`
				Address string          `json:"add"`
				Port    json.RawMessage `json:"port"`
			}
			if json.Unmarshal(decoded, &value) == nil {
				node.Protocol, node.Name, node.Address, node.Port = "vmess", value.Name, value.Address, strings.Trim(string(value.Port), "\"")
			}
		}
	}
	if node.Name == "" {
		node.Name = strings.ToUpper(node.Protocol)
	}
	return node
}

func (b *bridge) configurationGroups() []configurationGroup {
	groups := make([]configurationGroup, 0, len(b.sources)+1)
	for _, member := range b.subscriptions() {
		cached, issue := member.Bridge.sourceSnapshot()
		member.Bridge.mu.RLock()
		hwid := member.Bridge.identity
		member.Bridge.mu.RUnlock()
		token := member.Bridge.token
		if member.Bridge == b {
			token = b.sourceToken
		}
		group := configurationGroup{ID: member.ID, Name: member.Name, UpdatedAt: cached.UpdatedAt, Issue: issue, HWID: hwid, SubscriptionURL: b.config.PublicURL + "/sub/" + token, Nodes: []configurationNode{}}
		ids := configurationIDs(cached.Links)
		for _, original := range cached.Links {
			for _, link := range exportLinks([]string{original}, b.config.ExportProfile, "all") {
				node := describeConfiguration(link)
				node.ID = ids[original]
				group.Nodes = append(group.Nodes, node)
			}
		}
		groups = append(groups, group)
	}
	return groups
}
