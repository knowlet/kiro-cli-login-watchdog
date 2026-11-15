package kiro

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os/exec"
	"strings"
	"testing"
)

func TestLoginPendingPromptAfterExit(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		for _, result := range []string{"failed", "success"} {
			t.Run(result+"-"+stream, func(t *testing.T) {
				var logs bytes.Buffer
				client := helperClient(t, result+"-pending-"+stream, log.New(&logs, "", 0))
				calls := 0
				err := client.Login(context.Background(), func(flow DeviceFlow) error {
					calls++
					if flow.Code != "ABCD-EFGH" || flow.URL != "https://example.test/activate" {
						t.Errorf("unexpected flow: %+v", flow)
					}
					return nil
				})
				if result == "failed" {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
						t.Errorf("want original exit code 7, got %v", err)
					}
					if calls != 0 {
						t.Errorf("failed login delivered %d pending prompts", calls)
					}
				} else if err != nil || calls != 1 {
					t.Errorf("successful exit: callbacks=%d err=%v", calls, err)
				}
				if !strings.Contains(logs.String(), "[device login URL emitted; redacted]") {
					t.Error("final output was not logged")
				}
				if strings.Contains(logs.String(), "ABCD-EFGH") || strings.Contains(logs.String(), "https://") {
					t.Error("credentials appeared in logs")
				}
			})
		}
	}
}
