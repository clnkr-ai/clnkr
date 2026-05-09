package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/clnkr-ai/clnkr"
)

const modelWaitQueryDebugMessage, modelWaitSummaryDebugMessage = "querying model...", "step limit reached, requesting summary"

var modelWaitFrames = [...]string{"╵[-_-]╵", "╶[o_o]╴", "╷[O_O]╷", "╴[o_o]╶"}
var modelWaitDots = [...]string{"    ", ".   ", "..  ", "... "}

type modelWaitIndicator struct {
	mu          sync.Mutex
	out         interface{ Write([]byte) (int, error) }
	delay, tick time.Duration
	now         func() time.Time
	stop, done  chan struct{}
}

func (i *modelWaitIndicator) Start() {
	i.mu.Lock()
	if i.done != nil {
		i.mu.Unlock()
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	i.stop, i.done = stop, done
	start := i.now()
	i.mu.Unlock()

	go i.run(stop, done, start)
}

func (i *modelWaitIndicator) Stop() {
	if i == nil {
		return
	}
	i.mu.Lock()
	if i.done == nil {
		i.mu.Unlock()
		return
	}
	stop, done := i.stop, i.done
	if stop != nil {
		close(stop)
		i.stop = nil
	}
	i.mu.Unlock()

	<-done
	i.mu.Lock()
	if i.done == done {
		i.done = nil
	}
	i.mu.Unlock()
}

func (i *modelWaitIndicator) run(stop <-chan struct{}, done chan<- struct{}, start time.Time) {
	defer close(done)

	delay := time.NewTimer(i.delay)
	defer delay.Stop()

	select {
	case <-stop:
		return
	case <-delay.C:
	}
	i.render(0, start)

	ticker := time.NewTicker(i.tick)
	defer ticker.Stop()
	for frame := 1; ; frame++ {
		select {
		case <-stop:
			_, _ = fmt.Fprint(i.out, "\r\x1b[2K")
			return
		case <-ticker.C:
			i.render(frame, start)
		}
	}
}

func (i *modelWaitIndicator) render(frame int, start time.Time) {
	elapsed := int(i.now().Sub(start).Seconds())
	_, _ = fmt.Fprintf(i.out, "\r[clnkr] %s waiting for model%s%ds", modelWaitFrames[frame%len(modelWaitFrames)], modelWaitDots[(frame+len(modelWaitDots)-1)%len(modelWaitDots)], elapsed)
}

func updateModelWaitForAgentEvent(indicator *modelWaitIndicator, event clnkr.Event) {
	if indicator == nil {
		return
	}
	switch event := event.(type) {
	case clnkr.EventDebug:
		switch event.Message {
		case modelWaitQueryDebugMessage, modelWaitSummaryDebugMessage:
			indicator.Start()
		default:
			indicator.Stop()
		}
	case clnkr.EventResponse, clnkr.EventProtocolFailure, clnkr.EventCommandStart, clnkr.EventCommandDone:
		indicator.Stop()
	}
}
