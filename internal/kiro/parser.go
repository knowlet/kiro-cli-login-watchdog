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
// must also be treated as a credential by the logger. Candidates are ordered
// by evidence: an embedded code, an explicit cue, then the /device fallback.
func deviceURLs(clean string) []DeviceFlow {
	var ranked [3][]DeviceFlow
	previousEnd := 0
	for _, span := range urlRE.FindAllStringIndex(clean, -1) {
		// A cue applies only to the next URL, not every URL on the same line.
		cued := strings.Contains(strings.ToLower(clean[previousEnd:span[0]]), "open this url")
		previousEnd = span[1]
		raw := strings.TrimRight(clean[span[0]:span[1]], ".,;)]}")
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
		flow := DeviceFlow{Code: code, URL: raw}
		switch {
		case code != "":
			ranked[0] = append(ranked[0], flow)
		case cued:
			ranked[1] = append(ranked[1], flow)
		case strings.Contains(strings.ToLower(raw), "/device"):
			ranked[2] = append(ranked[2], flow)
		}
	}
	var flows []DeviceFlow
	for _, candidates := range ranked {
		flows = append(flows, candidates...)
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
	if flows := deviceURLs(clean); len(flows) > 0 {
		// Choose before consulting a previously emitted code; that code must
		// not cause an earlier, weaker URL candidate to win.
		best := flows[0]
		p.flow.URL = best.URL
		if best.Code != "" {
			p.flow.Code = best.Code
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
