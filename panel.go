package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

type panelClient struct {
	config        panelConfig
	token         string
	username      string
	password      string
	csrf          string
	authenticated bool
	requests      chan struct{}
	http          *http.Client
}
type syncResult struct {
	Selected  int
	Added     int
	Updated   int
	Unchanged int
	Failed    int
	Error     string
}

func panelEnabled(p *panelConfig) bool { return p != nil && (p.Enabled == nil || *p.Enabled) }

func newPanel(p panelConfig) (*panelClient, error) {
	if p.TokenEnv == "" {
		p.TokenEnv = "SUBBRIDGE_PANEL_TOKEN"
	}
	if p.UsernameEnv == "" {
		p.UsernameEnv = "SUBBRIDGE_PANEL_USERNAME"
	}
	if p.PasswordEnv == "" {
		p.PasswordEnv = "SUBBRIDGE_PANEL_PASSWORD"
	}
	token := os.Getenv(p.TokenEnv)
	username, password := os.Getenv(p.UsernameEnv), os.Getenv(p.PasswordEnv)
	if p.AuthMode == "password" {
		token = ""
		username, password = p.Username, p.Password
	}
	if p.AuthMode == "token" {
		token = p.APIToken
		username, password = "", ""
	}
	if token != "" {
		if len(token) > 4096 || strings.IndexFunc(token, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
			return nil, errors.New("некорректный API-токен панели")
		}
	} else if username == "" || password == "" || len(username) > 256 || len(password) > 4096 {
		return nil, errors.New("задайте API-токен либо логин и пароль панели в переменных окружения")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, errors.New("не удалось создать сессию панели")
	}
	return &panelClient{config: p, token: token, username: username, password: password, requests: make(chan struct{}, 1), http: &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type panelHTTPError struct {
	status  int
	message string
}

func (e *panelHTTPError) Error() string { return e.message }

// raw is called only while request owns the context-aware serialization gate.
func (p *panelClient) raw(ctx context.Context, path string, body any, csrf string, target any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return errors.New("не удалось подготовить запрос панели")
		}
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, p.config.URL+path, bytes.NewReader(encoded))
	if err != nil {
		return safeFailure("некорректный адрес API 3x-ui", err)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SubBridge/1")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		base, _ := url.Parse(p.config.URL)
		req.Header.Set("Origin", base.Scheme+"://"+base.Host)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return safeFailure("API 3x-ui недоступен", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case 401:
			return &panelHTTPError{401, "3x-ui: вход не выполнен или учётные данные отклонены"}
		case 403:
			return &panelHTTPError{403, "3x-ui: недостаточно прав или отклонён CSRF-токен"}
		case 404, 405:
			return &panelHTTPError{resp.StatusCode, "3x-ui: проверьте базовый путь панели и поддержку API"}
		default:
			return fmt.Errorf("API 3x-ui: HTTP %d", resp.StatusCode)
		}
	}
	b, err := readBounded(resp.Body, maxBytes)
	if err != nil {
		return safeFailure("не удалось прочитать API 3x-ui (максимум 8 МиБ)", err)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Object  json.RawMessage `json:"obj"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		return errors.New("3x-ui вернула некорректный JSON; проверьте базовый путь")
	}
	if !envelope.Success {
		return errors.New("операция отклонена API 3x-ui")
	}
	if target != nil && json.Unmarshal(envelope.Object, target) != nil {
		return errors.New("неожиданный формат ответа API 3x-ui")
	}
	return nil
}

func (p *panelClient) csrfToken(ctx context.Context) (string, error) {
	var token string
	if err := p.raw(ctx, "/csrf-token", nil, "", &token); err != nil {
		return "", err
	}
	if token == "" || len(token) > 512 || strings.IndexFunc(token, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return "", errors.New("3x-ui вернула некорректный CSRF-токен")
	}
	return token, nil
}

func (p *panelClient) login(ctx context.Context) error {
	p.authenticated = false
	token, err := p.csrfToken(ctx)
	if err != nil {
		return err
	}
	if err := p.raw(ctx, "/login", map[string]string{"username": p.username, "password": p.password}, token, nil); err != nil {
		return safeFailure("вход 3x-ui отклонён; проверьте логин/пароль (при включённой 2FA используйте API-токен)", err)
	}
	token, err = p.csrfToken(ctx)
	if err != nil {
		return err
	}
	p.csrf, p.authenticated = token, true
	return nil
}

func (p *panelClient) request(ctx context.Context, path string, body any, target any) error {
	return p.requestAPI(ctx, "/panel/api/clients/"+path, body, target)
}

func (p *panelClient) requestAPI(ctx context.Context, path string, body any, target any) error {
	select {
	case p.requests <- struct{}{}:
		defer func() { <-p.requests }()
	case <-ctx.Done():
		return safeFailure("запрос панели отменён", ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return safeFailure("запрос панели отменён", err)
	}
	if p.token == "" && !p.authenticated {
		if err := p.login(ctx); err != nil {
			return err
		}
	}
	err := p.raw(ctx, path, body, p.csrf, target)
	var status *panelHTTPError
	if p.token == "" && errors.As(err, &status) && status.status == 401 {
		// 3x-ui's authentication middleware rejects 401 before the handler.
		// Renew once; never retry ambiguous transport failures or API errors.
		if err := p.login(ctx); err != nil {
			return err
		}
		err = p.raw(ctx, path, body, p.csrf, target)
	}
	return err
}

type panelClientOption struct {
	Email      string `json:"email"`
	InboundIDs []int  `json:"inboundIds"`
}

type panelInboundOption struct {
	ID       int    `json:"id"`
	Remark   string `json:"remark"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}

type panelOptions struct {
	Clients  []panelClientOption  `json:"clients"`
	Inbounds []panelInboundOption `json:"inbounds"`
}

func (p *panelClient) options(ctx context.Context) (panelOptions, error) {
	result := panelOptions{Clients: []panelClientOption{}, Inbounds: []panelInboundOption{}}
	if err := p.request(ctx, "list", nil, &result.Clients); err != nil {
		return result, err
	}
	err := p.requestAPI(ctx, "/panel/api/inbounds/options", nil, &result.Inbounds)
	var status *panelHTTPError
	if errors.As(err, &status) && (status.status == 404 || status.status == 405) {
		err = p.requestAPI(ctx, "/panel/api/inbounds/list", nil, &result.Inbounds)
	}
	if err != nil {
		return result, err
	}
	if result.Clients == nil {
		result.Clients = []panelClientOption{}
	}
	if result.Inbounds == nil {
		result.Inbounds = []panelInboundOption{}
	}
	for _, client := range result.Clients {
		if client.Email == "" {
			return result, errors.New("некорректная запись клиента 3x-ui")
		}
	}
	for _, inbound := range result.Inbounds {
		if inbound.ID < 1 {
			return result, errors.New("некорректная запись инбаунда 3x-ui")
		}
	}
	return result, nil
}

func (p *panelClient) selected(ctx context.Context) ([]string, error) {
	seen := make(map[string]bool)
	emails := make([]string, 0)
	add := func(email string) {
		if !seen[email] {
			seen[email] = true
			emails = append(emails, email)
		}
	}
	for _, email := range p.config.Emails {
		add(email)
	}
	if p.config.AllClients || len(p.config.InboundIDs) > 0 {
		var rows []struct {
			Email      string `json:"email"`
			InboundIDs []int  `json:"inboundIds"`
		}
		if err := p.request(ctx, "list", nil, &rows); err != nil {
			return nil, err
		}
		ids := make(map[int]bool)
		for _, id := range p.config.InboundIDs {
			ids[id] = true
		}
		for _, row := range rows {
			if row.Email == "" {
				return nil, errors.New("некорректная запись клиента 3x-ui")
			}
			match := p.config.AllClients
			for _, id := range row.InboundIDs {
				match = match || ids[id]
			}
			if match {
				add(row.Email)
			}
		}
	}
	return emails, nil
}

func (p *panelClient) appendSubscription(ctx context.Context, email, subscription, legacy, owner string, dryRun bool) (bool, bool, error) {
	return p.appendSubscriptionCatalog(ctx, email, subscription, legacy, owner, dryRun, nil)
}

func (p *panelClient) appendSubscriptionCatalog(ctx context.Context, email, subscription, legacy, owner string, dryRun bool, catalog *sharedCatalog) (bool, bool, error) {
	key := url.PathEscape(email)
	var record map[string]json.RawMessage
	if err := p.request(ctx, "get/"+key, nil, &record); err != nil {
		return false, false, err
	}
	raw, ok := record["externalLinks"]
	if !ok {
		return false, false, errors.New("API клиента не поддерживает внешние подписки")
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return false, false, errors.New("некорректный список внешних подписок 3x-ui")
	}
	index := -1
	for i, row := range rows {
		var kind, value string
		if json.Unmarshal(row["kind"], &kind) != nil || json.Unmarshal(row["value"], &value) != nil || (kind != "link" && kind != "subscription") {
			return false, false, errors.New("некорректная внешняя ссылка 3x-ui")
		}
		if kind == "subscription" && value == subscription {
			index = i
		}
	}
	changed, added := false, false
	// Upgrade only entries created by SubBridge at its previous combined URL.
	// A disabled entry stays disabled; unrelated subscriptions are preserved.
	filtered := make([]map[string]json.RawMessage, 0, len(rows)+1)
	for i, row := range rows {
		var kind, value, remark string
		_ = json.Unmarshal(row["kind"], &kind)
		_ = json.Unmarshal(row["value"], &value)
		_ = json.Unmarshal(row["remark"], &remark)
		if kind == "link" && (owner != "" && ownedDirectValue(value, owner) || catalog != nil && managedMarker(linkMarker(value)) && catalog.match(value, owner) >= 0) {
			changed = true
			continue
		}
		ownedLegacy := legacy != "" && legacy != subscription && kind == "subscription" && value == legacy && remark == "SubBridge"
		if ownedLegacy && index >= 0 {
			changed = true
			continue
		}
		if ownedLegacy {
			index = i
		}
		if i == index && remark == "SubBridge" {
			prefix, _ := json.Marshal(p.config.NamePrefix)
			newValue, _ := json.Marshal(subscription)
			if !bytes.Equal(row["namePrefix"], prefix) || value != subscription {
				row["namePrefix"], row["value"] = prefix, newValue
				changed = true
			}
		}
		filtered = append(filtered, row)
	}
	rows = filtered
	if index < 0 {
		newRow := make(map[string]json.RawMessage)
		for k, v := range map[string]any{"kind": "subscription", "value": subscription, "remark": "SubBridge", "enable": true, "expiryTime": 0, "namePrefix": p.config.NamePrefix} {
			b, err := json.Marshal(v)
			if err != nil {
				return false, false, err
			}
			newRow[k] = b
		}
		rows = append(rows, newRow)
		changed, added = true, true
	}
	if changed && !dryRun {
		// Native API replaces this list. Read immediately before the write,
		// preserving every existing field; no client/inbound settings are sent.
		if err := p.request(ctx, key+"/externalLinks", map[string]any{"externalLinks": rows}, nil); err != nil {
			return false, false, err
		}
	}
	return added, changed && !added, nil
}

func (p *panelClient) sync(ctx context.Context, subscription string, dryRun bool) (syncResult, error) {
	return p.syncMigrating(ctx, subscription, "", dryRun)
}

func (p *panelClient) syncMigrating(ctx context.Context, subscription, legacy string, dryRun bool) (syncResult, error) {
	return p.syncSubscription(ctx, subscription, legacy, "", dryRun)
}

func (p *panelClient) syncSubscription(ctx context.Context, subscription, legacy, owner string, dryRun bool) (syncResult, error) {
	return p.syncSubscriptionCatalog(ctx, subscription, legacy, owner, dryRun, nil)
}

func (p *panelClient) syncSubscriptionCatalog(ctx context.Context, subscription, legacy, owner string, dryRun bool, catalog *sharedCatalog) (syncResult, error) {
	var result syncResult
	emails, err := p.selected(ctx)
	if err != nil {
		return result, err
	}
	result.Selected = len(emails)
	for _, email := range emails {
		if err := ctx.Err(); err != nil {
			return result, safeFailure("синхронизация панели отменена", err)
		}
		added, updated, err := p.appendSubscriptionCatalog(ctx, email, subscription, legacy, owner, dryRun, catalog)
		if err != nil {
			result.Failed++
			if result.Error == "" {
				result.Error = err.Error()
			}
		} else if added {
			result.Added++
		} else if updated {
			result.Updated++
		} else {
			result.Unchanged++
		}
	}
	return result, nil
}
