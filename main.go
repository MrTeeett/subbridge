package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

func initFromBundle(input, output string) error {
	f, err := os.Open(input)
	if err != nil {
		return errors.New("не удалось открыть переносимый файл")
	}
	defer f.Close()
	b, err := readBounded(f, maxBytes*2)
	if err != nil {
		return errors.New("не удалось прочитать переносимый файл")
	}
	var bundle struct {
		Format  string  `json:"format"`
		Version int     `json:"version"`
		Source  string  `json:"source"`
		Profile profile `json:"request_profile"`
	}
	if json.Unmarshal(b, &bundle) != nil || bundle.Format != "subbridge-subscription" || bundle.Version != 1 {
		return errors.New("нужен переносимый файл SubBridge версии 1")
	}
	c := config{Source: sourceConfig{URL: bundle.Source, profile: bundle.Profile}}
	base, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return errors.New("некорректный путь конфигурации")
	}
	if err := c.validate(base); err != nil {
		return err
	}
	c.DataDir = "state"
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return errors.New("не удалось создать конфигурацию")
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return errors.New("не удалось создать каталог конфигурации")
	}
	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("не удалось создать конфигурацию; существующий файл не перезаписывается")
	}
	_, writeErr := out.Write(append(data, '\n'))
	syncErr := out.Sync()
	closeErr := out.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		return errors.New("не удалось сохранить конфигурацию")
	}
	return nil
}

func syncPanel(ctx context.Context, p *panelClient, b *bridge, dryRun bool, logger *log.Logger) error {
	return syncPanelMode(ctx, p, b, dryRun, true, logger)
}

func syncPanelMode(ctx context.Context, p *panelClient, b *bridge, dryRun, explicit bool, logger *log.Logger) error {
	if p == nil {
		return nil
	}
	cached, _ := b.panelSnapshot()
	if !explicit && p.config.DeliveryMode != "subscription" {
		cached, _ = b.snapshot()
	}
	if len(cached.Links) == 0 && b.config.Panel != nil && b.config.Panel.ConfigSelection != nil {
		selection := b.config.Panel.ConfigSelection
		if len(selection.Sources) > 0 || len(selection.Nodes) > 0 {
			return errors.New("выбранные конфиги недоступны: обновите список и проверьте выбор; записи панели сохранены")
		}
	}
	if len(cached.Links) == 0 && (b.config.Panel == nil || b.config.Panel.ConfigSelection == nil) {
		return errors.New("панель не изменена: пригодного кеша пока нет")
	}
	links := exportLinks(cached.Links, b.config.ExportProfile, "all")
	if (explicit || p.config.DeliveryMode == "subscription") && len(strings.Join(links, "\n"))+1 > 2<<20 {
		return errors.New("выдача превышает лимит внешней подписки 3x-ui (2 МиБ)")
	}
	var result syncResult
	var err error
	if p.config.DeliveryMode == "subscription" && len(links) > 0 {
		var catalog *sharedCatalog
		if explicit {
			value := b.sharedCatalog()
			catalog = &value
		}
		result, err = p.syncSubscriptionCatalog(ctx, b.panelSubscriptionURL(), b.subscriptionURL(), contentID(b.token)[:16], dryRun, catalog)
	} else {
		catalog := b.sharedCatalog()
		if explicit && b.selectionChanges != nil {
			catalog.limitChanges(*b.selectionChanges)
		}
		result, err = p.syncShared(ctx, catalog, contentID(b.token)[:16], []string{b.panelSubscriptionURL(), b.subscriptionURL()}, explicit, dryRun)
	}
	if err != nil {
		return err
	}
	logger.Printf("панель: выбрано=%d узлов=%d добавлено=%d обновлено=%d без изменений=%d ошибок=%d dry_run=%t", result.Selected, len(links), result.Added, result.Updated, result.Unchanged, result.Failed, dryRun)
	if result.Failed > 0 {
		return errors.New(result.Error)
	}
	if !dryRun {
		b.mu.Lock()
		b.lastSync, b.syncedNodes, b.syncedClients = time.Now(), len(links), result.Selected
		b.panelPresence = nil
		b.mu.Unlock()
	}
	return nil
}

func worker(ctx context.Context, b *bridge, p *panelClient, logger *log.Logger, commands <-chan string, syncSelection bool) {
	nextRefresh, nextSync := time.Time{}, time.Time{}
	activity := func(value string) { b.mu.Lock(); b.activity = value; b.mu.Unlock() }
	synchronize := func(explicit bool) {
		activity("sync")
		syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := syncPanelMode(syncCtx, p, b, false, explicit, logger)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Printf("синхронизация: %v", err)
		}
		b.mu.Lock()
		b.syncIssue = ""
		if err != nil {
			b.syncIssue = err.Error()
		}
		b.mu.Unlock()
		activity("")
		nextSync = time.Now().Add(time.Duration(p.config.SyncSeconds) * time.Second)
	}
	if syncSelection && p != nil {
		synchronize(true)
		if !panelEnabled(b.config.Panel) {
			p = nil
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if !time.Now().Before(nextRefresh) {
			if b.hasSource() {
				activity("refresh")
				refreshCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
				err := b.refresh(refreshCtx)
				cancel()
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					logger.Printf("обновление: %v; используется последняя успешная версия, если она есть", err)
				} else {
					cached, _ := b.snapshot()
					logger.Printf("подписка обновлена: узлов=%d предупреждений=%d", len(cached.Links), len(cached.Warnings))
				}
				activity("")
			}
			nextRefresh = time.Now().Add(time.Duration(b.config.RefreshSeconds) * time.Second)
		}
		if p != nil && !time.Now().Before(nextSync) {
			synchronize(false)
			if ctx.Err() != nil {
				return
			}
		}
		delay := time.Until(nextRefresh)
		if p != nil {
			if d := time.Until(nextSync); d < delay {
				delay = d
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case action := <-commands:
			timer.Stop()
			if action == "refresh" {
				nextRefresh = time.Time{}
			}
			if action == "sync" {
				nextSync = time.Time{}
			}
		}
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("subbridge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "config.json", "JSON configuration, mode 600")
	initFile := flags.String("init-from-bundle", "", "create config from a portable SubBridge JSON")
	check := flags.Bool("check", false, "validate config without network requests")
	once := flags.Bool("once", false, "refresh and synchronize once, then exit")
	dryRun := flags.Bool("dry-run", false, "once, without writing to the panel")
	printURL := flags.Bool("print-url", false, "print private subscription URL, then exit")
	showVersion := flags.Bool("version", false, "print version")
	logFile := flags.String("log-file", "", "append sanitized service logs to a private file")
	managementPath := flags.String("management-config", "", "separate management.json file")
	noWeb := flags.Bool("no-web", false, "disable the management listener")
	setup := flags.Bool("setup", false, "initialize missing configuration and management credentials")
	dataDir := flags.String("data-dir", "", "data directory for initial setup")
	adminIP := flags.String("admin-ip", "", "set management bind IP")
	adminPort := flags.Int("admin-port", 0, "set management port")
	adminName := flags.String("admin-username", "", "set management username")
	adminPasswordEnv := flags.String("admin-password-env", "", "environment variable with new management password")
	resetAdmin := flags.Bool("reset-admin", false, "generate new management password")
	showManagement := flags.Bool("show-management", false, "print management settings without password hash")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("неожиданные позиционные аргументы")
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, "SubBridge "+version+" ("+runtime.Version()+")")
		return err
	}
	if *initFile != "" {
		if err := initFromBundle(*initFile, *path); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "Приватная конфигурация создана (600).")
		return err
	}
	if *managementPath == "" {
		*managementPath = filepath.Join(filepath.Dir(*path), "management.json")
	}
	if *setup {
		if _, err := setupConfiguration(*path, *dataDir); err != nil {
			return err
		}
	}
	if *setup || *adminIP != "" || *adminPort != 0 || *adminName != "" || *adminPasswordEnv != "" || *resetAdmin || *showManagement {
		m, err := ensureManagement(*managementPath)
		if err != nil {
			return err
		}
		if *showManagement {
			m.PasswordHash = ""
			return json.NewEncoder(stdout).Encode(m)
		}
		if *adminIP != "" {
			m.IP = *adminIP
		}
		if *adminPort != 0 {
			m.Port = *adminPort
		}
		if *adminName != "" {
			m.Username = *adminName
		}
		password := ""
		if *adminPasswordEnv != "" {
			password = os.Getenv(*adminPasswordEnv)
			if password == "" {
				return errors.New("переменная с паролем пуста")
			}
		} else if *resetAdmin {
			password, err = randomSecret()
			if err != nil {
				return err
			}
		}
		if password != "" {
			m.PasswordHash, err = hashPassword(password)
			if err != nil {
				return err
			}
		}
		if err := m.validate(); err != nil {
			return err
		}
		if err := writePrivateJSON(*managementPath, m); err != nil {
			return err
		}
		if password != "" {
			if err := atomicWrite(filepath.Join(filepath.Dir(*managementPath), "setup-credentials.txt"), []byte("Username: "+m.Username+"\nPassword: "+password+"\n")); err != nil {
				return safeFailure("не удалось сохранить данные входа", err)
			}
		} else if *adminName != "" {
			credentialsPath := filepath.Join(filepath.Dir(*managementPath), "setup-credentials.txt")
			data, err := privateFile(credentialsPath, 4096)
			if err == nil {
				_, remaining, _ := strings.Cut(string(data), "\n")
				if err := atomicWrite(credentialsPath, []byte("Username: "+m.Username+"\n"+remaining)); err != nil {
					return safeFailure("не удалось обновить данные входа", err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return safeFailure("не удалось прочитать данные входа", err)
			}
		}
		_, err = fmt.Fprintln(stdout, "Настройки сохранены. Данные первого входа: setup-credentials.txt рядом с management.json.")
		return err
	}
	c, err := loadConfig(*path)
	if err != nil {
		return err
	}
	if _, err := loadKeys(); err != nil {
		return err
	}
	if *check {
		_, err := fmt.Fprintln(stdout, "Конфигурация и встроенные ключи проверены. Сетевых запросов не было.")
		return err
	}
	b, err := newBridge(c)
	if err != nil {
		return err
	}
	if *printURL {
		_, err := fmt.Fprintln(stdout, b.subscriptionURL())
		return err
	}
	var panel *panelClient
	if (*once || *dryRun) && panelEnabled(c.Panel) {
		panel, err = newPanel(*c.Panel)
		if err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := log.New(stderr, "subbridge: ", log.LstdFlags)
	if *once || *dryRun {
		refreshCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		refreshErr := b.refresh(refreshCtx)
		cancel()
		cached, _ := b.snapshot()
		logger.Printf("узлов=%d предупреждений=%d", len(cached.Links), len(cached.Warnings))
		syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		syncErr := syncPanel(syncCtx, panel, b, *dryRun, logger)
		cancel()
		return errors.Join(refreshErr, syncErr)
	}
	if *logFile != "" {
		file, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return safeFailure("не удалось открыть журнал", err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return errors.New("журналу нужны права 600")
		}
		stderr = io.MultiWriter(stderr, file)
		logger.SetOutput(stderr)
	}
	service, err := newEngine(ctx, *path, c, stderr)
	if err != nil {
		return err
	}
	defer service.close()
	var admin *adminServer
	if !*noWeb {
		m, err := ensureManagement(*managementPath)
		if err != nil {
			return err
		}
		admin, err = startAdmin(service, *managementPath, m)
		if err != nil {
			return err
		}
		defer admin.close()
	}
	logger.Printf("сервер запущен на %s", c.Listen)
	select {
	case <-ctx.Done():
		return nil
	case err = <-service.errors:
		return err
	}
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "subbridge:", err)
		os.Exit(1)
	}
}
