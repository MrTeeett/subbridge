// Command package creates public release archives from an explicit allowlist.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var timestamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type entry struct {
	path, name string
	mode       int64
}

func writeArchive(path string, entries []entry, windows bool) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	var tw *tar.Writer
	var zw *zip.Writer
	var gz *gzip.Writer
	if windows {
		zw = zip.NewWriter(f)
		defer func() { err = errors.Join(err, zw.Close()) }()
	} else {
		gz = gzip.NewWriter(f)
		defer func() { err = errors.Join(err, gz.Close()) }()
		tw = tar.NewWriter(gz)
		defer func() { err = errors.Join(err, tw.Close()) }()
	}
	for _, e := range entries {
		info, err := os.Stat(e.path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", e.path)
		}
		var output io.Writer
		if windows {
			h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
			h.SetModTime(timestamp)
			h.SetMode(fs.FileMode(e.mode))
			output, err = zw.CreateHeader(h)
		} else {
			err = tw.WriteHeader(&tar.Header{Name: e.name, Size: info.Size(), Mode: e.mode, ModTime: timestamp, Typeflag: tar.TypeReg})
			output = tw
		}
		if err != nil {
			return err
		}
		input, err := os.Open(e.path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		if err := errors.Join(copyErr, input.Close()); err != nil {
			return err
		}
	}
	return nil
}
func packageRelease(version, tag string) error {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?$`).MatchString(version) {
		return errors.New("invalid version")
	}
	if tag != "dev" && tag != "v"+version {
		return errors.New("release tag must be dev or v followed by the version")
	}
	directory := "dist/releases"
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	common := []entry{
		{"LICENSE", "LICENSE", 0644},
		{"README.md", "README.md", 0644},
		{"README.ru.md", "README.ru.md", 0644},
		{"docs/README.md", "docs/README.md", 0644},
		{"docs/README.ru.md", "docs/README.ru.md", 0644},
		{"ui/OFL.txt", "licenses/Inter-OFL.txt", 0644},
		{"licenses/Go-LICENSE", "licenses/Go-LICENSE", 0644},
	}
	var archives []string
	for _, target := range []string{"linux-amd64", "linux-arm64", "linux-armv7", "linux-386", "windows-amd64", "windows-arm64"} {
		windows := strings.HasPrefix(target, "windows-")
		suffix, extension := "", ".tar.gz"
		if windows {
			suffix = ".exe"
			extension = ".zip"
		}
		entries := append([]entry(nil), common...)
		entries = append(entries, entry{"dist/subbridge-" + target + suffix, "subbridge" + suffix, 0755})
		files := []string{"install.sh", "subbridge.sh", "deploy/subbridge.service", "deploy/subbridge.openrc", "deploy/config.example.json", "deploy/panel.env.example"}
		if windows {
			files = []string{"install.ps1", "subbridge.ps1", "deploy/config.example.json"}
		}
		for _, path := range files {
			mode := int64(0644)
			if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".openrc") {
				mode = 0755
			}
			entries = append(entries, entry{path, path, mode})
		}
		name := "subbridge-" + tag + "-" + target + extension
		if err := writeArchive(filepath.Join(directory, name), entries, windows); err != nil {
			return err
		}
		archives = append(archives, name)
	}
	var source []entry
	// Source exports cannot accidentally pick up live configs, bundles or caches.
	rootFiles := []string{"go.mod", "crypto-assets.json", "LICENSE", "README.md", "README.ru.md", ".gitignore", "build.sh", "test.sh", "package.sh", "install.sh", "subbridge.sh", "install.ps1", "subbridge.ps1"}
	goFiles, err := filepath.Glob("*.go")
	if err != nil {
		return err
	}
	rootFiles = append(rootFiles, goFiles...)
	for _, path := range rootFiles {
		mode := int64(0644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0755
		}
		source = append(source, entry{path, path, mode})
	}
	for _, root := range []string{"ui", "testdata", "scripts", "tools", "deploy", "licenses", ".github", "docs"} {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			mode := int64(0644)
			if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".openrc") {
				mode = 0755
			}
			source = append(source, entry{path, filepath.ToSlash(path), mode})
			return nil
		}); err != nil {
			return err
		}
	}
	sort.Slice(source, func(i, j int) bool { return source[i].name < source[j].name })
	name := "subbridge-" + tag + "-source.tar.gz"
	if err := writeArchive(filepath.Join(directory, name), source, false); err != nil {
		return err
	}
	archives = append(archives, name)
	sort.Strings(archives)
	var checksums strings.Builder
	for _, name := range archives {
		input, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, input)
		if err := errors.Join(copyErr, input.Close()); err != nil {
			return err
		}
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), name)
	}
	return os.WriteFile(filepath.Join(directory, "checksums.txt"), []byte(checksums.String()), 0644)
}
func main() {
	version := flag.String("version", "1.1.0", "release version")
	tag := flag.String("tag", "", "release tag (dev or v followed by version)")
	flag.Parse()
	if *tag == "" {
		*tag = "v" + *version
	}
	if err := packageRelease(*version, *tag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
