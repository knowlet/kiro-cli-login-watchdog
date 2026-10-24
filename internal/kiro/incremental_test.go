package kiro

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
)

func TestLoginOutputFragmentedScanAllocations(t *testing.T) {
	// Allocation count, rather than wall time, catches full-buffer copies on each
	// write without depending on runner speed. Both paths previously allocated
	// over 32,000 times for this 16 KiB synthetic line.
	for _, emitted := range []bool{false, true} {
		t.Run(fmt.Sprint(emitted), func(t *testing.T) {
			data := bytes.Repeat([]byte("x"), 16*1024)
			allocs := testing.AllocsPerRun(3, func() {
				p := NewDeviceFlowParser(nil)
				w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
				if emitted {
					_, _ = w.Write([]byte("https://example.test/device?user_code=ABCD-EFGH "))
				}
				for i := range data {
					_, _ = w.Write(data[i : i+1])
				}
			})
			if allocs > 512 {
				t.Fatalf("fragmented scan allocated %.0f times; want <=512", allocs)
			}
		})
	}
}

func TestLoginOutputLongFragmentedLinePreservesURLAndRedaction(t *testing.T) {
	var logs bytes.Buffer
	calls := 0
	want := "https://example.test/activate/path?user_code=ABCD-EFGH"
	p := NewDeviceFlowParser(func(f DeviceFlow) error {
		calls++
		if f.URL != want || f.Code != "ABCD-EFGH" {
			t.Errorf("unexpected synthetic flow: %+v", f)
		}
		return nil
	})
	w := &loginOutput{client: &Client{logger: log.New(&logs, "", 0)}, parser: p, cancel: func() {}}
	data := []byte(strings.Repeat("x ", 64*1024) + want + " " + strings.Repeat("y", 64*1024))
	for i := range data {
		_, _ = w.Write(data[i : i+1])
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if logs.Len() != 0 {
		t.Fatal("unfinished line was logged")
	}
	w.flush()
	if calls != 1 {
		t.Fatal("duplicate callback on flush")
	}
	if strings.Contains(logs.String(), want) || strings.Contains(logs.String(), "ABCD-EFGH") {
		t.Fatal("synthetic credentials leaked")
	}
}

func TestLoginOutputUnknownEscapeDefersUntilFlush(t *testing.T) {
	p := NewDeviceFlowParser(nil)
	w := &loginOutput{client: &Client{}, parser: p, cancel: func() {}}
	for _, s := range []string{"\x1b", "]unknown ", "https://example.test/device?user_code=ABCD-EFGH "} {
		_, _ = w.Write([]byte(s))
	}
	if p.complete() {
		t.Fatal("unknown escape must defer incremental parsing")
	}
	w.flush()
	if !p.complete() {
		t.Fatal("whole-line parser behavior changed")
	}
}

func BenchmarkLoginOutputBytewiseLongLine(b *testing.B) {
	for _, n := range []int{4096, 16384, 65536} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			data := bytes.Repeat([]byte("x"), n)
			b.SetBytes(int64(n))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				w := &loginOutput{client: &Client{}, parser: NewDeviceFlowParser(nil), cancel: func() {}}
				for j := range data {
					_, _ = w.Write(data[j : j+1])
				}
			}
		})
	}
}
