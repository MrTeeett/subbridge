package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

type configurationPresence struct {
	SourceID     string `json:"source_id"`
	NodeID       string `json:"node_id"`
	ConnectionID string `json:"connection_id"`
	Present      int    `json:"present"`
	Active       int    `json:"active"`
	Managed      int    `json:"managed"`
	Manual       int    `json:"manual"`
	Duplicates   int    `json:"duplicates"`
}

type panelPresence struct {
	Configured bool                    `json:"configured"`
	Available  bool                    `json:"available"`
	Mode       string                  `json:"mode"`
	Clients    int                     `json:"clients"`
	CheckedAt  time.Time               `json:"checked_at"`
	Nodes      []configurationPresence `json:"nodes"`
	signature  string
}

// Read the native panel, never another SubBridge. Cached observations are
// immutable and short-lived; failed reads are never presented as absence.
func (b *bridge) observePanel(ctx context.Context, force bool) (*panelPresence, error) {
	result := &panelPresence{Nodes: []configurationPresence{}}
	cfg := b.config.Panel
	if cfg == nil || cfg.URL == "" || !cfg.AllClients && len(cfg.Emails) == 0 && len(cfg.InboundIDs) == 0 {
		return result, nil
	}
	result.Configured = true
	result.Mode = cfg.DeliveryMode
	if result.Mode == "" {
		result.Mode = "direct"
	}
	if result.Mode != "direct" {
		return result, nil
	}
	if !baseURL(cfg.URL) {
		return nil, errors.New("проверьте адрес панели 3x-ui")
	}
	catalog := b.sharedCatalog()
	var fingerprint strings.Builder
	for _, n := range catalog.nodes {
		fingerprint.WriteString(n.key)
		fingerprint.WriteString(n.connection)
		for _, ref := range n.refs {
			fingerprint.WriteString(ref.SourceID)
			fingerprint.WriteString(ref.NodeID)
		}
	}
	signature := contentID(fingerprint.String())
	select {
	case b.panelProbe <- struct{}{}:
		defer func() { <-b.panelProbe }()
	case <-ctx.Done():
		return nil, safeFailure("проверка панели отменена", ctx.Err())
	}
	b.mu.RLock()
	cached := b.panelPresence
	b.mu.RUnlock()
	if !force && cached != nil && cached.signature == signature && time.Since(cached.CheckedAt) < 8*time.Second {
		return cached, nil
	}
	p, err := newPanel(*cfg)
	if err != nil {
		return nil, err
	}
	emails, err := p.selected(ctx)
	if err != nil {
		return nil, err
	}
	result.Clients = len(emails)
	counts := make([]configurationPresence, len(catalog.nodes))
	owner := contentID(b.token)[:16]
	for _, email := range emails {
		rows, err := readExternalLinks(ctx, p, email)
		if err != nil {
			return nil, err
		}
		present, active, managed, manual := map[int]int{}, map[int]bool{}, map[int]bool{}, map[int]bool{}
		for _, row := range rows {
			kind, value, err := rowStrings(row)
			if err != nil {
				return nil, err
			}
			if kind != "link" {
				continue
			}
			index := catalog.match(value, owner)
			if index < 0 {
				continue
			}
			present[index]++
			active[index] = active[index] || externalActive(row)
			if managedMarker(linkMarker(value)) {
				managed[index] = true
			} else {
				manual[index] = true
			}
		}
		for index, total := range present {
			counts[index].Present++
			if active[index] {
				counts[index].Active++
			}
			if managed[index] {
				counts[index].Managed++
			}
			if manual[index] {
				counts[index].Manual++
			}
			counts[index].Duplicates += total - 1
		}
	}
	seen := map[configurationReference]bool{}
	for i, n := range catalog.nodes {
		for _, ref := range n.refs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			row := counts[i]
			row.SourceID, row.NodeID = ref.SourceID, ref.NodeID
			row.ConnectionID = n.connection
			result.Nodes = append(result.Nodes, row)
		}
	}
	result.Available = true
	result.CheckedAt = time.Now()
	result.signature = signature
	b.mu.Lock()
	b.panelPresence = result
	b.mu.Unlock()
	return result, nil
}
