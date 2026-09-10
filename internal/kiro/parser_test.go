package kiro

import "testing"

func TestDeviceFlowParser(t *testing.T) {
	var got DeviceFlow
	calls := 0
	p := NewDeviceFlowParser(func(flow DeviceFlow) error {
		calls++
		got = flow
		return nil
	})

	p.Feed("Learn more at https://kiro.dev/docs/cli/authentication/")
	p.Feed("Confirm the following code in the browser")
	p.Feed("Code: STDG-DFBR")
	p.Feed("")
	p.Feed("Open this URL: https://d-example.awsapps.com/start/#/device?user_code=STDG-DFBR")
	p.Feed("Open this URL: https://should-not-send-twice.example/device?user_code=NOPE")

	if calls != 1 {
		t.Fatalf("callback calls = %d, want 1", calls)
	}
	if got.Code != "STDG-DFBR" {
		t.Fatalf("code = %q", got.Code)
	}
	wantURL := "https://d-example.awsapps.com/start/#/device?user_code=STDG-DFBR"
	if got.URL != wantURL {
		t.Fatalf("url = %q, want %q", got.URL, wantURL)
	}
}

func TestDeviceFlowParserCanReadCodeFromURLQuery(t *testing.T) {
	var got DeviceFlow
	p := NewDeviceFlowParser(func(flow DeviceFlow) error {
		got = flow
		return nil
	})
	p.Feed("Open this URL: https://example.test/device?user_code=ABCD-EFGH")
	if got.Code != "ABCD-EFGH" {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestSanitizeLogLineRedactsDeviceCredentials(t *testing.T) {
	for _, line := range []string{
		"Code: ABCD-EFGH",
		"Open this URL: https://example.test/device?user_code=ABCD-EFGH",
	} {
		if got := sanitizeLogLine(line); got == line {
			t.Fatalf("line was not redacted: %q", line)
		}
	}
}
