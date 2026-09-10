package kiro

import (
	"reflect"
	"testing"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
)

func TestLoginArgs(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want []string
	}{
		{
			name: "identity center",
			cfg:  config.Config{AuthMethod: config.AuthIdentityCenter, IdentityProvider: "https://d-example.awsapps.com/start", Region: "ap-northeast-1"},
			want: []string{"login", "--license", "pro", "--identity-provider", "https://d-example.awsapps.com/start", "--region", "ap-northeast-1", "--use-device-flow"},
		},
		{
			name: "google",
			cfg:  config.Config{AuthMethod: config.AuthGoogle},
			want: []string{"login", "--social", "google", "--use-device-flow"},
		},
		{
			name: "github",
			cfg:  config.Config{AuthMethod: config.AuthGitHub},
			want: []string{"login", "--social", "github", "--use-device-flow"},
		},
		{
			name: "builder id",
			cfg:  config.Config{AuthMethod: config.AuthBuilderID},
			want: []string{"login", "--license", "free", "--use-device-flow"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.cfg, nil).LoginArgs()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("args = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestIsLoggedOut(t *testing.T) {
	for _, output := range []string{
		"Not logged in",
		"Not logged in.",
		"You are not logged in",
		"Error: Not logged in",
		`{"error":"Not logged in"}`,
	} {
		if !isLoggedOut(output) {
			t.Errorf("isLoggedOut(%q) = false, want true", output)
		}
	}
}
