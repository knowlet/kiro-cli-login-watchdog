package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileDoesNotOverrideExistingEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("KCLW_TEST_A=from-file\nKCLW_TEST_B='quoted value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KCLW_TEST_A", "from-env")
	_ = os.Unsetenv("KCLW_TEST_B")
	defer os.Unsetenv("KCLW_TEST_B")

	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("KCLW_TEST_A"); got != "from-env" {
		t.Fatalf("A = %q", got)
	}
	if got := os.Getenv("KCLW_TEST_B"); got != "quoted value" {
		t.Fatalf("B = %q", got)
	}
}
