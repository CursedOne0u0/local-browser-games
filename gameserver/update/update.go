// Package update keeps downloaded gameserver binaries current.
//
// On startup a release build (anything named gameserver-* in bin/) asks the
// published manifest for the newest version. If the manifest is newer, the
// matching platform binary is downloaded, checked against its published
// SHA-256, swapped in over the running executable, and the new build is
// re-executed with the same arguments. Every failure is non-fatal: offline
// players keep running the copy they already have.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repoSlug          = "CursedOne0u0/local-browser-games"
	defaultManifest   = "https://raw.githubusercontent.com/" + repoSlug + "/main/gameserver/update.json"
	manifestTimeout   = 5 * time.Second
	downloadTimeout   = 90 * time.Second
	maxBinaryBytes    = 64 << 20
	staleMarkerSuffix = ".old"
)

type build struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Version string           `json:"version"`
	Notes   string           `json:"notes"`
	Builds  map[string]build `json:"builds"`
}

// Platform is the manifest key for the running build ("linux-arm64", ...).
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// MaybeUpdate checks the manifest and installs a newer release when one exists.
// disabled skips the check entirely (-no-update); dev builds are always skipped.
func MaybeUpdate(current string, disabled bool) {
	if disabled || os.Getenv("GAMESERVER_UPDATED") == "1" ||
		os.Getenv("GAMESERVER_NO_UPDATE") == "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil || !isReleaseBuild(exe) {
		return // go run / ad-hoc output: leave the working copy alone
	}
	removeStale(exe)

	m, err := fetchManifest()
	if err != nil {
		fmt.Println("↻ update check skipped:", err)
		return
	}
	if !newer(m.Version, current) {
		fmt.Printf("✓ gameserver %s is up to date\n", current)
		return
	}
	b, ok := m.Builds[Platform()]
	if !ok || b.File == "" {
		fmt.Printf("↻ %s is available, but there is no %s build yet — staying on %s\n",
			m.Version, Platform(), current)
		return
	}
	fmt.Printf("↻ updating gameserver %s → %s\n", current, m.Version)

	data, err := download(b)
	if err != nil {
		fmt.Printf("✗ update failed: %v — staying on %s\n", err, current)
		return
	}
	if err := replaceSelf(exe, data); err != nil {
		fmt.Printf("✗ update failed: %v — staying on %s\n", err, current)
		return
	}
	if m.Notes != "" {
		fmt.Println("  " + m.Notes)
	}
	fmt.Printf("✓ updated to %s — restarting\n\n", m.Version)
	reexec(exe)
}

func isReleaseBuild(exe string) bool {
	base := filepath.Base(exe)
	return strings.HasPrefix(base, "gameserver") && !strings.Contains(base, ".test")
}

func manifestURL() string {
	if v := os.Getenv("GAMESERVER_UPDATE_URL"); v != "" {
		return v
	}
	return defaultManifest
}

func fetchManifest() (manifest, error) {
	var m manifest
	req, err := http.NewRequest(http.MethodGet, manifestURL(), nil)
	if err != nil {
		return m, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	client := &http.Client{Timeout: manifestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("manifest returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("manifest unreadable: %w", err)
	}
	if m.Version == "" {
		return m, errors.New("manifest has no version")
	}
	return m, nil
}

func download(b build) ([]byte, error) {
	u, err := resolve(manifestURL(), b.File)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBinaryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBinaryBytes {
		return nil, errors.New("download is larger than expected")
	}
	if len(data) < 1<<20 {
		return nil, errors.New("download is too small to be a gameserver binary")
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, b.SHA256) {
		return nil, fmt.Errorf("checksum mismatch (expected %s, got %s)", short(b.SHA256), short(got))
	}
	return data, nil
}

// replaceSelf writes data next to exe and swaps it in. On Linux/macOS the
// rename over a running executable is atomic; Windows cannot replace a running
// image, so the old one is moved aside first and cleaned up next start.
func replaceSelf(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".gameserver-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", exe, err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		aside := exe + staleMarkerSuffix
		os.Remove(aside)
		if err := os.Rename(exe, aside); err != nil {
			return fmt.Errorf("cannot move the running binary aside: %w", err)
		}
		if err := os.Rename(tmpName, exe); err != nil {
			os.Rename(aside, exe)
			return err
		}
		ok = true
		return nil
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return err
	}
	ok = true
	return nil
}

// reexec hands off to the freshly installed binary with the original
// arguments. POSIX replaces the process image so the PID (and any launcher
// watching it) survives; Windows starts a child and mirrors its exit code.
func reexec(exe string) {
	if err := execSelf(exe, os.Args[1:]); err != nil {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), "GAMESERVER_UPDATED=1")
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
	}
	os.Exit(0)
}

func removeStale(exe string) {
	matches, err := filepath.Glob(exe + staleMarkerSuffix + "*")
	if err != nil {
		return
	}
	for _, m := range matches {
		os.Remove(m) // fails harmlessly while still in use
	}
}

func resolve(base, rel string) (string, error) {
	bu, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	ru, err := url.Parse(rel)
	if err != nil {
		return "", err
	}
	return bu.ResolveReference(ru).String(), nil
}

// newer reports whether want is a later version than have.
func newer(want, have string) bool {
	w, okw := parseVersion(want)
	h, okh := parseVersion(have)
	if !okw || !okh {
		return strings.TrimSpace(want) != "" && strings.TrimSpace(want) != strings.TrimSpace(have)
	}
	for i := 0; i < len(w) || i < len(h); i++ {
		var a, b int
		if i < len(w) {
			a = w[i]
		}
		if i < len(h) {
			b = h[i]
		}
		if a != b {
			return a > b
		}
	}
	return false
}

func parseVersion(v string) ([]int, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if v == "" {
		return nil, false
	}
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
