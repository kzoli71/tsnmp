package main

import (
	"time"
)

const (
	defaultInterval = 5 * time.Second
	minInterval     = 1 * time.Second
	maxInterval     = 300 * time.Second
	rediscoverEvery = 60 * time.Second
	// discoveryRetry is the wait before retrying a failed rediscovery while
	// polling goes on with the interface list already known.
	discoveryRetry = 30 * time.Second
)

// PollResult is delivered to the UI after every poll cycle.
type PollResult struct {
	T        time.Time
	Duration time.Duration // how long the SNMP queries took
	Err      error         // counter poll failed: the data shown is stale
	DiscErr  error         // last (re)discovery failed; polling may still work
}

// Poller periodically queries the agent and fills the store.
type Poller struct {
	client     *Client
	store      *Store
	intervalCh chan time.Duration
	onResult   func(PollResult)

	// discovery state, only used by the poller goroutine
	discovered  bool      // at least one discovery succeeded
	nextDiscAt  time.Time // when the next (re)discovery is due
	lastDiscErr error     // sticky until a discovery succeeds
}

func NewPoller(c *Client, s *Store, onResult func(PollResult)) *Poller {
	return &Poller{client: c, store: s, intervalCh: make(chan time.Duration, 1), onResult: onResult}
}

// SetInterval changes the poll interval; the next poll is rescheduled.
func (p *Poller) SetInterval(d time.Duration) {
	select {
	case <-p.intervalCh:
	default:
	}
	p.intervalCh <- d
}

// discover runs a discovery and schedules the next one: regularly after a
// success, after discoveryRetry after a failure (if a list is known; without
// one nothing can be shown, so it is retried every cycle).
func (p *Poller) discover(now time.Time) {
	infos, err := p.client.Discover()
	if err != nil {
		p.lastDiscErr = err
		if p.discovered {
			p.nextDiscAt = now.Add(discoveryRetry)
		}
		return
	}
	p.store.UpdateInterfaces(infos)
	p.discovered, p.lastDiscErr = true, nil
	p.nextDiscAt = now.Add(rediscoverEvery)
}

// cycle runs one poll: discovery when due, then the counters. A failing
// discovery does not stop polling once an interface list is known.
func (p *Poller) cycle() PollResult {
	start := time.Now()
	res := PollResult{T: start}
	if !start.Before(p.nextDiscAt) {
		p.discover(start)
	}
	if !p.discovered {
		res.Err = p.lastDiscErr
	} else if data, err := p.client.Poll(); err != nil {
		res.Err = err
	} else if p.store.ApplyPoll(data) && p.lastDiscErr == nil {
		// New interface appeared (and no discovery backoff is running):
		// fetch its names now and apply again so it gets its first
		// counter snapshot.
		p.discover(start)
		p.store.ApplyPoll(data)
	}
	res.DiscErr = p.lastDiscErr
	res.Duration = time.Since(start)
	return res
}

func (p *Poller) Run(interval time.Duration) {
	var lastStart time.Time
	timer := time.NewTimer(0)
	for {
		select {
		case <-timer.C:
		case d := <-p.intervalCh:
			interval = d
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			// Next poll at last start + new interval, so repeated +/- presses
			// do not keep postponing it.
			timer.Reset(max(time.Until(lastStart.Add(interval)), 0))
			continue
		}
		lastStart = time.Now()
		p.onResult(p.cycle())
		timer.Reset(max(interval-time.Since(lastStart), 0))
	}
}
