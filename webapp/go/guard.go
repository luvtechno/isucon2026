package isureal

import (
	"sync"
	"time"
)

// Guard は攻撃を繰り返す IP を見つけて BAN する（元の isureal-guard と同じ基準）。
type failureKind int

const (
	failLogin failureKind = iota
	failInvalidImage
	failInvalidSignature
	numFailKinds
)

const guardBanDuration = 2 * time.Minute

var guardRules = [numFailKinds]struct {
	threshold int
	window    time.Duration
}{
	failLogin:            {10, 10 * time.Second},
	failInvalidImage:     {2, 5 * time.Second},
	failInvalidSignature: {3, 5 * time.Second},
}

type Guard struct {
	mu       sync.Mutex
	failures [numFailKinds]map[string][]time.Time
	sweeps   [numFailKinds]time.Time
	ban      func(ip string, until int64)
}

func NewGuard(ban func(ip string, until int64)) *Guard {
	g := &Guard{ban: ban}
	for i := range g.failures {
		g.failures[i] = map[string][]time.Time{}
	}
	return g
}

func (g *Guard) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range g.failures {
		g.failures[i] = map[string][]time.Time{}
	}
}

func (g *Guard) Observe(ip string, kind failureKind) {
	if ip == "" {
		return
	}
	now := time.Now()
	rule := guardRules[kind]
	g.mu.Lock()
	if now.Sub(g.sweeps[kind]) >= rule.window {
		g.sweeps[kind] = now
		for k, times := range g.failures[kind] {
			if now.Sub(times[len(times)-1]) >= rule.window {
				delete(g.failures[kind], k)
			}
		}
	}
	kept := g.failures[kind][ip][:0]
	for _, at := range g.failures[kind][ip] {
		if now.Sub(at) < rule.window {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	banned := len(kept) >= rule.threshold
	if banned {
		delete(g.failures[kind], ip)
	} else {
		g.failures[kind][ip] = kept
	}
	g.mu.Unlock()
	if banned {
		g.ban(ip, now.Add(guardBanDuration).UnixMicro())
	}
}
