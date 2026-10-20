package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

const maxBytes = 8 << 20

type profile struct {
	UserAgent   string `json:"user_agent,omitempty"`
	HWID        string `json:"hwid,omitempty"`
	DeviceOS    string `json:"device_os,omitempty"`
	OSVersion   string `json:"os_version,omitempty"`
	DeviceModel string `json:"device_model,omitempty"`
	AppVersion  string `json:"app_version,omitempty"`
}

type sourceConfig struct {
	URL          string `json:"url"`
	HWIDAttempts int    `json:"hwid_attempts,omitempty"`
	profile
}

type subscriptionConfig struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Source sourceConfig `json:"source"`
}

type panelConfig struct {
	Enabled         *bool                   `json:"enabled,omitempty"`
	AuthMode        string                  `json:"auth_mode,omitempty"`
	Username        string                  `json:"username,omitempty"`
	Password        string                  `json:"password,omitempty"`
	APIToken        string                  `json:"api_token,omitempty"`
	URL             string                  `json:"url"`
	TokenEnv        string                  `json:"api_token_env"`
	UsernameEnv     string                  `json:"username_env,omitempty"`
	PasswordEnv     string                  `json:"password_env,omitempty"`
	AllClients      bool                    `json:"all_clients"`
	Emails          []string                `json:"emails"`
	InboundIDs      []int                   `json:"inbound_ids"`
	SyncSeconds     int                     `json:"sync_seconds"`
	NamePrefix      string                  `json:"name_prefix"`
	DeliveryMode    string                  `json:"delivery_mode,omitempty"`
	ConfigSelection *configurationSelection `json:"config_selection,omitempty"`
}

type config struct {
	Source         sourceConfig         `json:"source"`
	SourceName     string               `json:"source_name,omitempty"`
	Sources        []subscriptionConfig `json:"sources,omitempty"`
	Listen         string               `json:"listen"`
	PublicURL      string               `json:"public_url"`
	DataDir        string               `json:"data_dir"`
	RefreshSeconds int                  `json:"refresh_seconds"`
	ExportProfile  string               `json:"export_profile"`
	Panel          *panelConfig         `json:"panel,omitempty"`
}

func (p *profile) defaults() {
	if p.UserAgent == "" {
		p.UserAgent = "v2raytun/android"
	}
	if p.DeviceOS == "" {
		p.DeviceOS = "Android"
	}
	if p.AppVersion == "" {
		p.AppVersion = "5.26.83"
	}
}

func (p profile) headers() (http.Header, error) {
	h := make(http.Header)
	for key, value := range map[string]string{"User-Agent": p.UserAgent, "X-HWID": p.HWID,
		"X-Device-OS": p.DeviceOS, "X-Ver-OS": p.OSVersion,
		"X-Device-Model": p.DeviceModel, "X-App-Version": p.AppVersion} {
		if len(value) > 512 {
			return nil, fmt.Errorf("слишком длинный заголовок %s", key)
		}
		for _, r := range value {
			if r < 32 || r > 126 {
				return nil, fmt.Errorf("некорректный заголовок %s", key)
			}
		}
		if value != "" {
			h.Set(key, value)
		}
	}
	return h, nil
}

func baseURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if port := u.Port(); port != "" {
		v, err := strconv.Atoi(port)
		if err != nil || v < 1 || v > 65535 {
			return false
		}
	}
	return !strings.ContainsAny(value, "\r\n\t ")
}

func validProfile(value string) bool { return value == "original" || value == "v2rayn" }

func (s *sourceConfig) validate() error {
	if len(s.URL) > 32768 {
		return errors.New("слишком длинный source.url")
	}
	if s.HWIDAttempts == 0 {
		s.HWIDAttempts = 3
	}
	if s.HWIDAttempts < 1 || s.HWIDAttempts > 20 {
		return errors.New("hwid_attempts: допустимо 1–20")
	}
	s.defaults()
	_, err := s.headers()
	return err
}

var sourceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (c *config) validate(baseDir string) error {
	if err := c.Source.validate(); err != nil {
		return err
	}
	if len(c.Sources) > 31 {
		return errors.New("допустимо не более 32 подписок")
	}
	validName := func(name string) bool { return len(name) <= 256 && strings.IndexFunc(name, unicode.IsControl) < 0 }
	if !validName(c.SourceName) {
		return errors.New("некорректное название подписки")
	}
	ids := map[string]bool{"default": true, "all": true}
	for i := range c.Sources {
		s := &c.Sources[i]
		if !sourceIDPattern.MatchString(s.ID) || ids[strings.ToLower(s.ID)] || !validName(s.Name) {
			return errors.New("некорректное название или повторяющийся ID подписки")
		}
		ids[strings.ToLower(s.ID)] = true
		if err := s.Source.validate(); err != nil {
			return err
		}
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8770"
	}
	host, port, err := net.SplitHostPort(c.Listen)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || net.ParseIP(host) == nil || n < 1 || n > 65535 {
		return errors.New("listen: нужен IP:порт")
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://" + net.JoinHostPort("127.0.0.1", port)
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if !baseURL(c.PublicURL) {
		return errors.New("public_url: нужен базовый HTTP(S) адрес без query, fragment и логина")
	}
	if c.DataDir == "" {
		c.DataDir = "state"
	}
	if !filepath.IsAbs(c.DataDir) {
		c.DataDir = filepath.Join(baseDir, c.DataDir)
	}
	if c.RefreshSeconds == 0 {
		c.RefreshSeconds = 300
	}
	if c.RefreshSeconds < 30 || c.RefreshSeconds > 86400 {
		return errors.New("refresh_seconds: допустимо 30–86400")
	}
	if c.ExportProfile == "" {
		c.ExportProfile = "original"
	}
	if !validProfile(c.ExportProfile) {
		return errors.New("export_profile: original или v2rayn")
	}
	if p := c.Panel; p != nil {
		if p.DeliveryMode == "" {
			p.DeliveryMode = "direct"
		}
		if p.DeliveryMode != "direct" && p.DeliveryMode != "subscription" {
			return errors.New("panel.delivery_mode: direct или subscription")
		}
		if err := p.ConfigSelection.validate(); err != nil {
			return err
		}
		if p.AuthMode == "" {
			p.AuthMode = "environment"
		}
		if p.AuthMode != "environment" && p.AuthMode != "password" && p.AuthMode != "token" {
			return errors.New("неизвестный способ входа 3x-ui")
		}
		if !panelEnabled(p) {
			return nil
		}
		p.URL = strings.TrimRight(p.URL, "/")
		if !baseURL(p.URL) {
			return errors.New("panel.url: нужен базовый адрес панели с её секретным путём, перед /panel/")
		}
		if p.TokenEnv == "" {
			p.TokenEnv = "SUBBRIDGE_PANEL_TOKEN"
		}
		if p.UsernameEnv == "" {
			p.UsernameEnv = "SUBBRIDGE_PANEL_USERNAME"
		}
		if p.PasswordEnv == "" {
			p.PasswordEnv = "SUBBRIDGE_PANEL_PASSWORD"
		}
		if !p.AllClients && len(p.Emails) == 0 && len(p.InboundIDs) == 0 {
			return errors.New("укажите panel.emails, panel.inbound_ids или all_clients=true")
		}
		for _, email := range p.Emails {
			if email == "" || len(email) > 256 || strings.IndexFunc(email, unicode.IsControl) >= 0 {
				return errors.New("некорректный элемент panel.emails")
			}
		}
		for _, id := range p.InboundIDs {
			if id < 1 {
				return errors.New("panel.inbound_ids: нужны положительные ID")
			}
		}
		if p.SyncSeconds == 0 {
			p.SyncSeconds = 60
		}
		if p.SyncSeconds < 30 || p.SyncSeconds > 86400 {
			return errors.New("panel.sync_seconds: допустимо 30–86400")
		}
		if len(p.NamePrefix) > 256 || strings.IndexFunc(p.NamePrefix, unicode.IsControl) >= 0 {
			return errors.New("некорректный panel.name_prefix")
		}
	}
	return nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("превышен размер данных")
	}
	return b, nil
}

func loadConfig(path string) (config, error) {
	var c config
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("не удалось открыть конфигурацию")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return c, errors.New("конфигурация содержит секреты: нужны права chmod 600")
	}
	b, err := readBounded(f, 1<<20)
	if err != nil {
		return c, errors.New("не удалось прочитать конфигурацию (максимум 1 МиБ)")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, errors.New("некорректный JSON или неизвестное поле конфигурации")
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("после конфигурации есть лишние данные")
	}
	fullPath, err := filepath.Abs(path)
	if err != nil {
		return c, errors.New("некорректный путь конфигурации")
	}
	return c, c.validate(filepath.Dir(fullPath))
}
