package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceSelfInstallsAndKeepsExecutableBit(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "gameserver")
	if err := os.WriteFile(exe, []byte("old-binary-contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBytes := []byte("new-binary-contents")
	if err := replaceSelf(exe, newBytes); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBytes) {
		t.Fatalf("replaceSelf wrote %q, want %q", got, newBytes)
	}
	st, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replaced binary is not executable: %v", st.Mode())
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".gameserver-update-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestRemoveStaleClearsMarkers(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "gameserver")
	stale := exe + staleMarkerSuffix
	if err := os.WriteFile(stale, []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	removeStale(exe)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale marker still present: %v", err)
	}
}
