package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvFileUTF8BOM(t *testing.T) {
	const key = "KCLW_TEST_UTF8_BOM"
	tests := []struct {
		name     string
		content  string
		want     string
		wantSet  bool
		existing bool
	}{
		{"assignment", "\ufeff" + key + "=from-file\n", "from-file", true, false},
		{"comment", "\ufeff# Configuration\n" + key + "=from-file\n", "from-file", true, false},
		{"export", "\ufeffexport " + key + "=from-file\n", "from-file", true, false},
		{"blank first line", "\ufeff\n" + key + "=from-file\n", "from-file", true, false},
		{"CRLF", "\ufeff# Configuration\r\n" + key + "=from-file\r\n", "from-file", true, false},
		{"whitespace after BOM", "\ufeff  " + key + " = 'from-file'\n", "from-file", true, false},
		{"BOM only", "\ufeff", "", false, false},
		{"existing environment", "\ufeff" + key + "=from-file\n", "from-env", true, true},
		{"embedded BOM in first value", "\ufeff" + key + "=before\ufeffafter\n", "before\ufeffafter", true, false},
		{"embedded BOM in later value", "# Configuration\n" + key + "='\ufeffvalue'\n", "\ufeffvalue", true, false},
		{"without BOM", key + "=from-file\n", "from-file", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Restore both possible keys even when the regression runs against
			// the old parser, which put the file marker into the variable name.
			for _, name := range []string{key, "\ufeff" + key, "\ufeff  " + key, "\ufeffexport " + key} {
				t.Setenv(name, "")
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			}
			if tt.existing {
				t.Setenv(key, "from-env")
			}
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := LoadEnvFile(path); err != nil {
				t.Fatal(err)
			}
			if got, exists := os.LookupEnv(key); got != tt.want || exists != tt.wantSet {
				t.Fatalf("setting = %q, present = %v; want %q, present = %v", got, exists, tt.want, tt.wantSet)
			}
			if _, exists := os.LookupEnv("\ufeff" + key); exists {
				t.Fatal("file BOM became part of the environment key")
			}
			if _, exists := os.LookupEnv("\ufeffexport " + key); exists {
				t.Fatal("file BOM prevented export parsing")
			}
		})
	}
}

func TestLoadEnvFileBOMOnlyAtFileStart(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		line    string
	}{
		{"after whitespace", " \ufeff# Configuration\n", "line 1"},
		{"later line", "# Configuration\n\ufeff# Another comment\n", "line 2"},
		{"repeated BOM", "\ufeff\ufeff# Configuration\n", "line 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := LoadEnvFile(path); err == nil || !strings.Contains(err.Error(), tt.line+": expected KEY=VALUE") {
				t.Fatalf("got %v, want unchanged parse error at %s", err, tt.line)
			}
		})
	}
}
