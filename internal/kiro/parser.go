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

const (
	deviceURLWeak = iota + 1
	deviceURLCued
	deviceURLWithCode
)

type deviceURLCandidate struct {
	flow     DeviceFlow
	strength int
}

type DeviceFlowParser struct {
	mu          sync.Mutex
	flow        DeviceFlow
	urlStrength int
	sent        bool
	callback    func(DeviceFlow) error
	err         error
}

func NewDeviceFlowParser(callback func(DeviceFlow) error) *DeviceFlowParser {
	return &DeviceFlowParser{callback: callback}
}

// deviceURLCandidates is shared with log redaction: every URL accepted by the
// parser must also be treated as a credential by the logger. Candidates are
// ordered by evidence: an embedded code, an explicit cue, then /device fallback.
func deviceURLCandidates(clean string) []deviceURLCandidate {
	var ranked [3][]deviceURLCandidate
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
		candidate := deviceURLCandidate{flow: DeviceFlow{Code: code, URL: raw}}
		switch {
		case code != "":
			candidate.strength = deviceURLWithCode
			ranked[0] = append(ranked[0], candidate)
		case cued:
			candidate.strength = deviceURLCued
			ranked[1] = append(ranked[1], candidate)
		case strings.Contains(strings.ToLower(raw), "/device"):
			candidate.strength = deviceURLWeak
			ranked[2] = append(ranked[2], candidate)
		}
	}
	var candidates []deviceURLCandidate
	for _, group := range ranked {
		candidates = append(candidates, group...)
	}
	return candidates
}

func deviceURLs(clean string) []DeviceFlow {
	candidates := deviceURLCandidates(clean)
	flows := make([]DeviceFlow, 0, len(candidates))
	for _, candidate := range candidates {
		flows = append(flows, candidate.flow)
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
	if candidates := deviceURLCandidates(clean); len(candidates) > 0 {
		best := candidates[0]
		if best.strength > p.urlStrength {
			p.flow.URL = best.flow.URL
			p.urlStrength = best.strength
		}
		if best.flow.Code != "" {
			p.flow.Code = best.flow.Code
		}
	}
	// A weak /device heuristic is useful for final fallback and redaction, but
	// must not trigger a notification while stronger URL evidence may still
	// arrive on a later line.
	if p.urlStrength >= deviceURLCued {
		p.emitLocked()
	}
}

// Finalize is called after both login output streams are drained. Only then is
// a weak /device-only URL allowed to act as a fallback.
func (p *DeviceFlowParser) Finalize() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sent || p.err != nil {
		return
	}
	p.emitLocked()
}

func (p *DeviceFlowParser) emitLocked() {
	if p.flow.Code == "" || p.flow.URL == "" {
		return
	}
	if p.callback != nil {
		if err := p.callback(p.flow); err != nil {
			p.err = fmt.Errorf("device-flow callback: %w", err)
			return
		}
	}
	p.sent = true
}

func (p *DeviceFlowParser) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
