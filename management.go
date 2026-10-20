package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Management is deliberately stored separately from source/3x-ui config.
type managementConfig struct {
	Enabled      bool   `json:"enabled"`
	IP           string `json:"ip"`
	Port         int    `json:"port"`
	Path         string `json:"path"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash,omitempty"`
	SSHHost      string `json:"ssh_host"`
	SSHPort      int    `json:"ssh_port"`
	SSHUser      string `json:"ssh_user"`
	TLSCert      string `json:"tls_cert,omitempty"`
	TLSKey       string `json:"tls_key,omitempty"`
}

func managementDefaults() managementConfig {
	return managementConfig{Enabled: true, IP: "127.0.0.1", Port: 8780, Path: "/admin/", Username: "admin", SSHPort: 22, SSHUser: "root"}
}

var adminPathPattern = regexp.MustCompile(`^/[a-zA-Z0-9/_-]*/$|^/$`)
var sshHostPattern = regexp.MustCompile(`^[a-zA-Z0-9.:-]*$`)
var sshUserPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func (m managementConfig) validate() error {
	if net.ParseIP(m.IP) == nil || m.Port < 1 || m.Port > 65535 {
		return errors.New("management: некорректный IP или порт")
	}
	if !adminPathPattern.MatchString(m.Path) || strings.Contains(m.Path, "//") || len(m.Path) > 128 {
		return errors.New("management: путь должен начинаться и заканчиваться / и содержать буквы, цифры, _ или -")
	}
	if len(m.Username) < 1 || len(m.Username) > 128 || strings.ContainsAny(m.Username, "\r\n\t") {
		return errors.New("management: некорректный логин")
	}
	if !sshHostPattern.MatchString(m.SSHHost) || len(m.SSHHost) > 253 || !sshUserPattern.MatchString(m.SSHUser) || m.SSHPort < 1 || m.SSHPort > 65535 {
		return errors.New("management: некорректные параметры SSH")
	}
	if (m.TLSCert == "") != (m.TLSKey == "") {
		return errors.New("management: задайте сертификат и ключ TLS вместе")
	}
	if m.PasswordHash == "" {
		return errors.New("management: пароль не настроен")
	}
	return nil
}
func hashPassword(password string) (string, error) {
	if len(password) < 8 || len(password) > 1024 {
		return "", errors.New("пароль должен содержать от 8 до 1024 символов")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", safeFailure("не удалось создать пароль", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if err != nil {
		return "", safeFailure("не удалось создать пароль", err)
	}
	return "pbkdf2-sha256$600000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || len(password) > 1024 {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 600000 || iterations > 1000000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", safeFailure("не удалось создать токен", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return safeFailure("не удалось создать каталог настроек", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("не удалось сериализовать настройки")
	}
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		return safeFailure("не удалось сохранить настройки", err)
	}
	return nil
}
func loadManagement(path string) (managementConfig, error) {
	m := managementDefaults()
	data, err := privateFile(path, 65536)
	if err != nil {
		return m, safeFailure("не удалось прочитать management.json", err)
	}
	if json.Unmarshal(data, &m) != nil {
		return m, errors.New("некорректный management.json")
	}
	return m, m.validate()
}
func ensureManagement(path string) (managementConfig, error) {
	m, err := loadManagement(path)
	if err == nil {
		return m, nil
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		return m, err
	}
	m = managementDefaults()
	password, err := randomSecret()
	if err != nil {
		return m, err
	}
	m.PasswordHash, err = hashPassword(password)
	if err != nil {
		return m, err
	}
	if err := writePrivateJSON(path, m); err != nil {
		return m, err
	}
	credentials := fmt.Sprintf("Username: %s\nPassword: %s\nPanel: http://%s%s\n", m.Username, password, net.JoinHostPort(m.IP, strconv.Itoa(m.Port)), m.Path)
	if err := atomicWrite(filepath.Join(filepath.Dir(path), "setup-credentials.txt"), []byte(credentials)); err != nil {
		return m, safeFailure("не удалось сохранить данные первого входа", err)
	}
	return m, nil
}
func setupConfiguration(path, directory string) (config, error) {
	if _, err := os.Stat(path); err == nil {
		return loadConfig(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return config{}, errors.New("не удалось проверить конфигурацию")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return config{}, err
	}
	c := config{DataDir: directory}
	if err := c.validate(base); err != nil {
		return c, err
	}
	if err := writePrivateJSON(path, c); err != nil {
		return c, err
	}
	return c, nil
}
