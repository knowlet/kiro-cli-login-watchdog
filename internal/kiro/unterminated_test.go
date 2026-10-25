package kiro

import (
	"bytes"
	"context"
	"errors"
	"log"
	"testing"
	"time"
)

func TestLoginDelimitedPromptBeforeChildExit(t *testing.T) {
	for _, mode := range []string{"newline-wait", "delimited-wait", "delimited-stderr"} {
		t.Run(mode, func(t *testing.T) {
			c := helperClient(t, mode, nil)
			c.loginTimeout = time.Second
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := c.Login(ctx, func(flow DeviceFlow) error {
				calls++
				wantURL := "https://example.test/activate?user_code=ABCD-EFGH"
				if flow.Code != "ABCD-EFGH" || flow.URL != wantURL {
					t.Errorf("flow=%+v", flow)
				}
				cancel()
				return nil
			})
			if calls != 1 || !errors.Is(err, context.Canceled) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestLoginOutputDelimitedSplitWrites(t *testing.T) {
	for _, line := range []string{
		"Open this URL: https://example.test/activate/path?user_code=ABCD-EFGH ",
		"Visit https://example.test/#/device/path?user_code=ABCD-EFGH ",
	} {
		// Every possible two-write split includes path, query, parameter name and
		// code boundaries. A partial cued URL must not latch at equal strength.
		for split := 0; split < len(line); split++ {
			var logs bytes.Buffer
			calls := 0
			var got DeviceFlow
			p := NewDeviceFlowParser(func(f DeviceFlow) error { calls++; got = f; return nil })
			w := &loginOutput{client: &Client{logger: log.New(&logs, "", 0)}, parser: p, cancel: func() {}}
			_, _ = w.Write([]byte(line[:split]))
			if calls != 0 {
				t.Fatalf("early flow at split %d: %+v (%q)", split, got, line)
			}
			_, _ = w.Write([]byte(line[split:]))
			if calls != 1 || got.Code != "ABCD-EFGH" {
				t.Fatalf("split %d: calls=%d flow=%+v", split, calls, got)
			}
			expected := deviceURLs(line)[0].URL
			if got.URL != expected {
				t.Fatalf("split %d: truncated URL %q, want %q", split, got.URL, expected)
			}
			if logs.Len() != 0 {
				t.Fatal("partial buffer was logged")
			}
			w.flush()
			if calls != 1 {
				t.Fatal("duplicate callback after flush")
			}
		}
	}
}

func TestLoginOutputBareTokenWaitsForDelimiter(t *testing.T) {
	p := NewDeviceFlowParser(nil)
	w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
	_, _ = w.Write([]byte("Open this URL: https://example.test/device?user_code=ABC"))
	if p.sent || p.flow.URL != "" || p.flow.Code != "" {
		t.Fatalf("partial token latched: %+v", p.flow)
	}
	_, _ = w.Write([]byte("D-EFGH"))
	if p.sent {
		t.Fatal("bare final token emitted without delimiter")
	}
	_, _ = w.Write([]byte("\t"))
	if !p.sent || p.flow.Code != "ABCD-EFGH" {
		t.Fatalf("flow=%+v", p.flow)
	}
}

func TestLoginOutputBytewisePreservesCueAndCode(t *testing.T) {
	for _, line := range []string{
		"Open this URL: https://example.test/activate/path?user_code=ABCD-EFGH ",
	} {
		calls := 0
		p := NewDeviceFlowParser(func(f DeviceFlow) error {
			calls++
			if f.URL != deviceURLs(line)[0].URL || f.Code != "ABCD-EFGH" {
				t.Errorf("flow=%+v", f)
			}
			return nil
		})
		w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
		for i := range len(line) {
			_, _ = w.Write([]byte{line[i]})
			if i < len(line)-1 && calls != 0 {
				t.Fatalf("early callback at byte %d", i)
			}
		}
		if calls != 1 {
			t.Fatalf("calls=%d", calls)
		}
	}
}

func TestLoginDelimitedCallbackFailureCancelsChild(t *testing.T) {
	c := helperClient(t, "delimited-wait", nil)
	sentinel := errors.New("notification failed")
	err := c.Login(context.Background(), func(DeviceFlow) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoginOutputSplitANSIDoesNotDelimitToken(t *testing.T) {
	p := NewDeviceFlowParser(nil)
	w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
	for _, part := range []string{"Open this URL: https://example.test/device?user_code=ABC", "\x1b[ ", "m", "D-EFGH"} {
		_, _ = w.Write([]byte(part))
		if p.sent || p.flow.URL != "" {
			t.Fatalf("partial URL latched: %+v", p.flow)
		}
	}
	_, _ = w.Write([]byte(" "))
	if !p.sent || p.flow.Code != "ABCD-EFGH" {
		t.Fatalf("flow=%+v", p.flow)
	}
}

func TestLoginOutputSameLineRankingAcrossSplits(t *testing.T) {
	line := "Code: ABCD-EFGH; Open this URL: https://example.test/generic Visit https://example.test/device?user_code=IJKL-MNOP "
	for split := 0; split < len(line); split++ {
		var got DeviceFlow
		calls := 0
		p := NewDeviceFlowParser(func(f DeviceFlow) error { calls++; got = f; return nil })
		w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
		_, _ = w.Write([]byte(line[:split]))
		if calls != 0 || p.flow.URL != "" {
			t.Fatalf("premature lower-ranked candidate at %d: %+v", split, p.flow)
		}
		_, _ = w.Write([]byte(line[split:]))
		if calls != 1 || got.Code != "IJKL-MNOP" || got.URL != "https://example.test/device?user_code=IJKL-MNOP" {
			t.Fatalf("split %d: calls=%d flow=%+v", split, calls, got)
		}
	}
}

func TestLoginOutputLowerRankedPromptsWaitForLineEnd(t *testing.T) {
	for _, line := range []string{
		"Code: ABCD-EFGH; Open this URL: https://example.test/generic ",
		"Code: ABCD-EFGH; Visit https://example.test/device ",
	} {
		p := NewDeviceFlowParser(nil)
		w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
		_, _ = w.Write([]byte(line))
		if p.sent || p.flow.URL != "" {
			t.Fatalf("lower-ranked candidate parsed early: %+v", p.flow)
		}
		w.flush()
		p.Finalize()
		if !p.sent || p.flow.Code != "ABCD-EFGH" {
			t.Fatalf("line-end flow=%+v", p.flow)
		}
	}
}

func TestLoginOutputWhitespaceDelimiters(t *testing.T) {
	for _, delimiter := range []string{" ", "\t", "\r", "\f"} {
		p := NewDeviceFlowParser(nil)
		w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
		_, _ = w.Write([]byte("https://example.test/device?user_code=ABCD-EFGH" + delimiter))
		if !p.sent || p.flow.Code != "ABCD-EFGH" {
			t.Fatalf("delimiter %q: %+v", delimiter, p.flow)
		}
	}
}
