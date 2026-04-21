package client

import (
	"os"
	"path/filepath"
	"testing"

	modelConfig "github.com/hsqbyte/nlink/src/models/config"
)

func TestMergeProxies_DedupAndOverride(t *testing.T) {
	static := []modelConfig.ProxyConfig{
		{Name: "web", Type: "tcp", LocalPort: 80},
		{Name: "ssh", Type: "tcp", LocalPort: 22},
	}
	dyn := []modelConfig.ProxyConfig{
		{Name: "ssh", Type: "tcp", LocalPort: 2222}, // override
		{Name: "db", Type: "tcp", LocalPort: 3306},  // new
	}
	out := mergeProxies(static, dyn)
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d", len(out))
	}
	byName := map[string]modelConfig.ProxyConfig{}
	for _, p := range out {
		byName[p.Name] = p
	}
	if byName["ssh"].LocalPort != 2222 {
		t.Fatalf("ssh not overridden: %+v", byName["ssh"])
	}
	if byName["db"].LocalPort != 3306 {
		t.Fatalf("db missing")
	}
	if byName["web"].LocalPort != 80 {
		t.Fatalf("web lost")
	}
}

func TestLoadSaveRuntimeProxies_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.yaml")

	// ENOENT returns nil
	got, err := loadRuntimeProxies(path)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}

	want := []modelConfig.ProxyConfig{
		{Name: "a", Type: "tcp", LocalPort: 1},
		{Name: "b", Type: "socks5", RemotePort: 1080},
	}
	if err := saveRuntimeProxies(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file missing: %v", err)
	}
	got, err = loadRuntimeProxies(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestRuntimeProxyFile_Safelist(t *testing.T) {
	p := runtimeProxyFile("node/一", "10.0.0.1", 7000)
	if p == "" {
		t.Fatal("empty")
	}
	// should not contain path separator from input
	base := filepath.Base(p)
	for i := 0; i < len(base); i++ {
		c := base[i]
		if c == '/' || c == '\\' {
			t.Fatalf("bad char in %q", base)
		}
	}
}
