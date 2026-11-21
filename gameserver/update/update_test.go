package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		want, have string
		ok         bool
	}{
		{"1.26", "1.25", true},
		{"1.25", "1.26", false},
		{"1.26", "1.26", false},
		{"1.26.1", "1.26", true},
		{"v1.27", "1.26", true},
		{"2.0", "1.99", true},
		{"1.100", "1.99", true},
		{"", "1.26", false},
		{"beta", "1.26", true},
		{"1.26", "beta", true},
	}
	for _, c := range cases {
		if got := newer(c.want, c.have); got != c.ok {
			t.Errorf("newer(%q, %q) = %v, want %v", c.want, c.have, got, c.ok)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v, ok := parseVersion("1.26.3"); !ok || len(v) != 3 || v[0] != 1 || v[2] != 3 {
		t.Fatalf("parseVersion(1.26.3) = %v, %v", v, ok)
	}
	if _, ok := parseVersion("nope"); ok {
		t.Fatal("parseVersion(nope) should fail")
	}
	if _, ok := parseVersion(""); ok {
		t.Fatal("parseVersion(\"\") should fail")
	}
	if v, ok := parseVersion("v1.30-rc1"); !ok || v[1] != 30 {
		t.Fatalf("parseVersion(v1.30-rc1) = %v, %v", v, ok)
	}
}

func TestResolveRelativeToManifest(t *testing.T) {
	got, err := resolve(
		"https://raw.githubusercontent.com/o/r/main/gameserver/update.json",
		"bin/gameserver-linux-amd64",
	)
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://raw.githubusercontent.com/o/r/main/gameserver/bin/gameserver-linux-amd64"
	if got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

func TestPlatformKeyIsStable(t *testing.T) {
	if p := Platform(); p == "" || p[0] == '-' {
		t.Fatalf("unexpected platform key %q", p)
	}
}
