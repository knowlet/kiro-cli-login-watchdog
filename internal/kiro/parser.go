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

// deviceURLs is shared with log redaction: every URL accepted by the parser
// must also be treated as a credential by the logger.
func deviceURLs(clean string) []DeviceFlow {
	var flows []DeviceFlow
	for _, raw := range urlRE.FindAllString(clean, -1) {
		raw = strings.TrimRight(raw, ".,;)]}")
		parsed, err := url.Parse(raw)
		code := ""
		if err == nil {
			code = parsed.Query().Get("user_code")
			// IAM Identity Center puts the router path AND query after '#'.
			if code == "" {
				if fragment, e := url.Parse(parsed.Fragment); e == nil {
					code = fragment.Query().Get("user_code")
				}
			}
		}
		if strings.Contains(strings.ToLower(clean), "open this url") ||
			code != "" || strings.Contains(strings.ToLower(raw), "/device") {
			flows = append(flows, DeviceFlow{Code: code, URL: raw})
		}
	}
	return flows
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
	for _, flow := range deviceURLs(clean) {
		p.flow.URL = flow.URL
		if flow.Code != "" {
			p.flow.Code = flow.Code
		}
		if p.flow.Code != "" {
			break
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
