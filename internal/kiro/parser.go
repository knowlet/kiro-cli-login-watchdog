package kiro

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

var (
	codeRE = regexp.MustCompile(`(?i)\bcode\s*:\s*([A-Z0-9][A-Z0-9-]{2,})`)
	urlRE  = regexp.MustCompile(`https?://[^\s]+`)
)

type DeviceFlowParser struct {
	mu       sync.Mutex
	flow     DeviceFlow
	sent     bool
	callback func(DeviceFlow) error
	err      error
}

func NewDeviceFlowParser(callback func(DeviceFlow) error) *DeviceFlowParser {
	return &DeviceFlowParser{callback: callback}
}

func (p *DeviceFlowParser) Feed(line string) {
	clean := ansiRE.ReplaceAllString(line, "")

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sent || p.err != nil {
		return
	}

	if m := codeRE.FindStringSubmatch(clean); len(m) == 2 {
		p.flow.Code = m[1]
	}
	if raw := urlRE.FindString(clean); raw != "" {
		raw = strings.TrimRight(raw, ".,;)]}")
		parsed, parseErr := url.Parse(raw)
		isDeviceURL := strings.Contains(strings.ToLower(clean), "open this url") ||
			(parseErr == nil && parsed.Query().Get("user_code") != "") ||
			strings.Contains(strings.ToLower(raw), "/device")
		if isDeviceURL {
			p.flow.URL = raw
			if p.flow.Code == "" && parseErr == nil {
				p.flow.Code = parsed.Query().Get("user_code")
			}
		}
	}

	if p.flow.Code != "" && p.flow.URL != "" {
		if p.callback != nil {
			if err := p.callback(p.flow); err != nil {
				p.err = fmt.Errorf("device-flow callback: %w", err)
				return
			}
		}
		p.sent = true
	}
}

func (p *DeviceFlowParser) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
