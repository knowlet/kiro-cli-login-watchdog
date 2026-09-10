package kiro

import (
	"strings"
	"testing"
)

func TestDeviceFlowURLSelectionWithEarlierCode(t *testing.T) {
	const code = "ABCD-EFGH"
	const explicitURL = "https://example.test/#/device?user_code=" + code
	const genericURL = "https://example.test/activate"
	tests := []struct {
		name, line, wantURL string
	}{
		{"docs before cue", "Docs https://kiro.dev/docs; Open this URL: " + genericURL, genericURL},
		{"docs before fragment", "Docs https://kiro.dev/docs; Open this URL: " + explicitURL, explicitURL},
		{"device help before code URL", "Help https://kiro.dev/device-help then " + explicitURL, explicitURL},
		{"cue before code URL", "Open this URL: https://kiro.dev/docs then " + explicitURL, explicitURL},
		{"cue outranks device heuristic", "Help https://kiro.dev/device-help; Open this URL: " + genericURL, genericURL},
		{"cue only binds next URL", "Open this URL: " + genericURL + " Support https://kiro.dev/help", genericURL},
		{"mixed case and ANSI cue", "Docs https://kiro.dev/docs; \x1b[32mOPEN THIS URL:\x1b[0m " + genericURL, genericURL},
		{"device fallback", "Visit https://example.test/device", "https://example.test/device"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			var got DeviceFlow
			p := NewDeviceFlowParser(func(flow DeviceFlow) error {
				calls++
				got = flow
				return nil
			})
			p.Feed("Code: " + code)
			p.Feed(tt.line)
			// End-of-output finalization is when a weak /device-only candidate is
			// allowed to become the fallback. Strong candidates already emitted.
			p.Finalize()
			if got.URL != tt.wantURL || got.Code != code || calls != 1 {
				t.Fatalf("flow=%+v calls=%d; want URL=%s code=%s once", got, calls, tt.wantURL, code)
			}
			p.Feed("Open this URL: https://other.test/device?user_code=OTHER-CODE")
			if calls != 1 {
				t.Fatalf("duplicate callback: %d", calls)
			}
			if clean := sanitizeLogLine(tt.line); strings.Contains(clean, "https://") || strings.Contains(clean, code) {
				t.Fatalf("credential-bearing line not redacted: %s", clean)
			}
		})
	}
}

func TestURLCueDoesNotClassifySurroundingDocumentation(t *testing.T) {
	const actual = "https://example.test/activate"
	for _, line := range []string{
		"Docs https://kiro.dev/docs; Open this URL: " + actual + " Support https://kiro.dev/help",
		"Docs https://kiro.dev/docs; Open this URL: " + actual,
	} {
		flows := deviceURLs(line)
		if len(flows) != 1 || flows[0].URL != actual {
			t.Fatalf("cue candidates=%+v; want only %s", flows, actual)
		}
	}
	if flows := deviceURLs("Docs https://kiro.dev/docs; Open this URL:"); len(flows) != 0 {
		t.Fatalf("cue bound backwards: %+v", flows)
	}
}
