package realtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/prometheus/client_golang/prometheus"
)

type Signal struct {
	Topic     string
	StreamSeq int64
	EventType string
}
type subscription struct {
	topics map[string]bool
	signal chan Signal
}
type connection struct {
	principal guid.ID
	topics    map[string]bool
	queue     chan events.StreamEvent
	stop      chan string
	stopped   bool
	interrupt func()
}

// Hub owns one replica's bounded replay ring and fanout. Business modules can
// subscribe to signals, including server-only named substreams, without HTTP.
type Hub struct {
	mu          sync.Mutex
	ring        []events.StreamEvent
	high        int64
	connections map[*connection]bool
	principals  map[guid.ID]int
	subs        map[*subscription]bool
	byTopic     map[string]map[*connection]bool
	cancel      context.CancelFunc
	done        chan struct{}
	closing     bool
	dropped     *prometheus.CounterVec
	streams     prometheus.Gauge
	frames      *prometheus.CounterVec
	latency     prometheus.Histogram
	resync      *prometheus.CounterVec
	denied      *prometheus.CounterVec
}

func newHub() *Hub {
	return &Hub{streams: prometheus.NewGauge(prometheus.GaugeOpts{Name: "realtime_streams", Help: "Active SSE connections."}), frames: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "realtime_frames_sent_total", Help: "Successfully flushed SSE frames."}, []string{"kind"}), latency: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "realtime_delivery_latency_seconds", Help: "Event occurrence to SSE write latency.", Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}}), resync: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "realtime_resync_total", Help: "Stream resynchronization requests."}, []string{"reason"}), denied: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "realtime_topic_denied_total", Help: "Topic authorization rejections."}, []string{"kind"}), connections: map[*connection]bool{}, principals: map[guid.ID]int{}, subs: map[*subscription]bool{}, byTopic: map[string]map[*connection]bool{}, dropped: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "realtime_dropped_total", Help: "Realtime connections closed because the consumer could not keep up."}, []string{"reason"})}
}
func (h *Hub) Subscribe(ctx context.Context, topics []string) (<-chan Signal, func()) {
	s := &subscription{topics: map[string]bool{}, signal: make(chan Signal, 1)}
	for _, topic := range topics {
		if _, _, err := parseTopic(topic); err == nil {
			s.topics[topic] = true
		}
	}
	h.mu.Lock()
	if !h.closing {
		h.subs[s] = true
	} else {
		close(s.signal)
	}
	h.mu.Unlock()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			h.mu.Lock()
			if h.subs[s] {
				delete(h.subs, s)
				close(s.signal)
			}
			h.mu.Unlock()
		})
	}
	// context.AfterFunc avoids a goroutine per idle internal subscription.
	unschedule := context.AfterFunc(ctx, stop)
	return s.signal, func() { unschedule(); stop() }
}
func (h *Hub) start(ctx context.Context, tail events.Tail, flush func(context.Context), wakeups ...events.Wakeups) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return ErrUnavailable
	}
	if h.cancel != nil {
		return nil
	}
	page, err := tail(ctx, 0, 10000, true)
	if err != nil {
		return err
	}
	h.high = page.HighWater
	for _, e := range page.Events {
		if time.Since(e.SequencedAt) <= 15*time.Minute {
			h.ring = append(h.ring, e)
		}
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	var wake <-chan struct{}
	if len(wakeups) > 0 && wakeups[0] != nil {
		wake, err = wakeups[0](runCtx)
		if err != nil {
			cancel()
			return err
		}
	}
	h.cancel = cancel
	h.done = make(chan struct{})
	go func() {
		defer close(h.done)
		poll := time.NewTicker(250 * time.Millisecond)
		broadcast := time.NewTicker(100 * time.Millisecond)
		defer poll.Stop()
		defer broadcast.Stop()
		delay := 250 * time.Millisecond
		for {
			select {
			case <-runCtx.Done():
				return
			case <-broadcast.C:
				flush(runCtx)
				continue
			case <-poll.C:
			case _, open := <-wake:
				if !open {
					wake = nil
				}
			}
			h.mu.Lock()
			after := h.high
			h.mu.Unlock()
			for {
				page, err := tail(runCtx, after, 1000, false)
				if err != nil {
					if runCtx.Err() == nil && !gormx.IsSQLiteBusy(err) {
						slog.ErrorContext(runCtx, "tail realtime events", "error", err)
					}
					delay = min(2*delay, 2*time.Second)
					poll.Reset(delay)
					break
				}
				if len(page.Events) == 0 {
					delay = min(2*delay, 2*time.Second)
				} else {
					delay = 250 * time.Millisecond
				}
				poll.Reset(delay)
				for _, e := range page.Events {
					h.deliver(e)
					after = e.Seq
				}
				if len(page.Events) < 1000 {
					h.mu.Lock()
					if page.HighWater > h.high {
						h.high = page.HighWater
					}
					h.mu.Unlock()
					break
				}
				if runCtx.Err() != nil {
					return
				}
			}
		}
	}()
	return nil
}
func (h *Hub) deliver(e events.StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing || e.Seq <= h.high {
		return
	}
	h.high = e.Seq
	h.ring = append(h.ring, e)
	cut := 0
	for cut < len(h.ring) && (len(h.ring)-cut > 10000 || time.Since(h.ring[cut].SequencedAt) > 15*time.Minute) {
		cut++
	}
	if cut > 0 {
		h.ring = append([]events.StreamEvent(nil), h.ring[cut:]...)
	}
	if e.Mode == events.RealtimeNone {
		return
	}
	for s := range h.subs {
		for _, topic := range e.Topics {
			if s.topics[topic] {
				select {
				case s.signal <- Signal{Topic: topic, StreamSeq: e.Seq, EventType: e.Type}:
				default:
				}
				break
			}
		}
	}
	matched := map[*connection]bool{}
	for _, topic := range e.Topics {
		for c := range h.byTopic[topic] {
			matched[c] = true
		}
	}
	for c := range matched {
		if c.stopped {
			continue
		}
		select {
		case c.queue <- e:
		default:
			c.stopped = true
			c.stop <- "slow_consumer"
			h.dropped.WithLabelValues("slow_consumer").Inc()
		}
	}
}
func (h *Hub) connect(principal guid.ID, topics []string, after *int64) (*connection, []events.StreamEvent, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing || h.cancel == nil {
		return nil, nil, "", ErrUnavailable
	}
	if len(h.connections) >= 5000 || h.principals[principal] >= 10 {
		h.dropped.WithLabelValues("limit").Inc()
		return nil, nil, "", ErrLimit
	}
	c := &connection{principal: principal, topics: map[string]bool{}, queue: make(chan events.StreamEvent, 256), stop: make(chan string, 1)}
	for _, topic := range topics {
		c.topics[topic] = true
		if h.byTopic[topic] == nil {
			h.byTopic[topic] = map[*connection]bool{}
		}
		h.byTopic[topic][c] = true
	}
	h.connections[c] = true
	h.streams.Inc()
	h.principals[principal]++
	var replay []events.StreamEvent
	reason := ""
	if after != nil {
		if *after > h.high {
			reason = "cursor_unknown"
		} else if *after < h.high && (len(h.ring) == 0 || *after < h.ring[0].Seq-1) {
			reason = "cursor_too_old"
		} else {
			for _, e := range h.ring {
				if e.Seq > *after && matches(c.topics, e) {
					replay = append(replay, e)
				}
			}
		}
	}
	if reason != "" {
		h.resync.WithLabelValues(reason).Inc()
	}
	return c, replay, reason, nil
}
func matches(topics map[string]bool, e events.StreamEvent) bool {
	if e.Mode == events.RealtimeNone {
		return false
	}
	for _, t := range e.Topics {
		if topics[t] {
			return true
		}
	}
	return false
}
func (h *Hub) disconnect(c *connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.connections[c] {
		return
	}
	delete(h.connections, c)
	h.streams.Dec()
	h.principals[c.principal]--
	if h.principals[c.principal] == 0 {
		delete(h.principals, c.principal)
	}
	for topic := range c.topics {
		delete(h.byTopic[topic], c)
		if len(h.byTopic[topic]) == 0 {
			delete(h.byTopic, topic)
		}
	}
}
func (h *Hub) revoke(c *connection, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(c.topics, topic)
	delete(h.byTopic[topic], c)
	if len(h.byTopic[topic]) == 0 {
		delete(h.byTopic, topic)
	}
}

func (h *Hub) interruptWith(c *connection, fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.interrupt = fn
	if c.stopped {
		fn()
	}
}
func (h *Hub) quiesce(ctx context.Context) error {
	h.mu.Lock()
	h.closing = true
	for c := range h.connections {
		if c.interrupt != nil {
			c.interrupt()
		}
		if !c.stopped {
			c.stopped = true
			c.stop <- "reconnect"
			h.dropped.WithLabelValues("shutdown").Inc()
		}
	}
	for s := range h.subs {
		delete(h.subs, s)
		close(s.signal)
	}
	cancel, done := h.cancel, h.done
	h.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
