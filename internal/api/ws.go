// WebSocket endpoint GET /api/v1/ws, per docs/10-api.md's "WebSocket
// /api/v1/ws": a single multiplexed connection per client, topics
// distinguished by the message envelope's "topic" field. This file holds
// the hub/client plumbing and message framing; publish.go drives the
// boards/devices/pipelines topics from polled state, and internal/engine's
// Manager.Subscribe drives the per-pipeline debug stream.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sviniabanditka/pinctl/internal/engine"
)

const (
	// wsSendBuffer is the per-client outbound queue depth. A client whose
	// queue is still full when a new message arrives is dropped rather
	// than allowed to block a publisher — docs/03-architecture.md's
	// "slow-consumer drop" policy.
	wsSendBuffer = 64

	wsWriteWait    = 10 * time.Second
	wsPongWait     = 60 * time.Second
	wsPingInterval = (wsPongWait * 9) / 10
	wsReadLimit    = 4096 // client messages are tiny (subscribe/unsubscribe/ping)
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// No origin check: pinctl is an unauthenticated LAN tool per
	// docs/00-design-brief.md, same trust model as every REST endpoint.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Broadcast topics fanned out to every subscribed client via Hub.
// "pipeline:{id}" is not one of these — it's a per-client, per-pipeline
// stream sourced from engine.Manager.Subscribe, handled in
// wsClient.subscribePipeline.
const (
	topicBoards    = "boards"
	topicDevices   = "devices"
	topicPipelines = "pipelines"
)

func isBroadcastTopic(topic string) bool {
	switch topic {
	case topicBoards, topicDevices, topicPipelines:
		return true
	}
	return false
}

// wsEnvelope is the docs/10-api.md message envelope, server -> client.
type wsEnvelope struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
	Data  any    `json:"data"`
}

// wsInMessage is the envelope shape accepted from the client: only
// subscribe/unsubscribe/ping carry meaning, per docs/10-api.md ("Aside
// from subscribe/unsubscribe/ping, the client never sends anything").
type wsInMessage struct {
	Type  string          `json:"type"`
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

// wsErrorData is the data payload of a server -> client "error" frame.
type wsErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Hub fans out boards/devices/pipelines topic events to every client
// currently subscribed to that topic. It holds no per-pipeline debug
// state — that lives on each wsClient via engine.Manager.Subscribe.
type Hub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

func newHub() *Hub { return &Hub{clients: make(map[*wsClient]struct{})} }

func (h *Hub) register(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *wsClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// broadcast delivers data to every client subscribed to topic. Delivery to
// each client is via wsClient.enqueue, which drops (closes) a client whose
// send buffer is already full rather than blocking here.
func (h *Hub) broadcast(topic string, data any) {
	h.mu.Lock()
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		if c.subscribedTo(topic) {
			c.enqueue(wsEnvelope{Type: "event", Topic: topic, Data: data})
		}
	}
}

// closeAll sends every connected client a normal WS close frame and tears
// down its connection — part of the graceful shutdown sequence (docs/10:
// "the server closes every WebSocket cleanly... not a dropped connection").
func (h *Hub) closeAll() {
	h.mu.Lock()
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		c.shutdown()
	}
}

// pipeSub tracks one client's subscription to a "pipeline:{id}" debug
// stream: unsub detaches from the engine's snapshotHub, done stops the
// relay goroutine forwarding snapshots onto the client's send queue.
type pipeSub struct {
	unsub func()
	done  chan struct{}
}

// wsClient is one connected WebSocket, with its own subscription set.
type wsClient struct {
	conn   *websocket.Conn
	server *Server
	send   chan wsEnvelope

	mu       sync.Mutex
	closed   bool
	topics   map[string]bool
	pipeSubs map[string]pipeSub

	closeOnce sync.Once
}

func newWSClient(conn *websocket.Conn, s *Server) *wsClient {
	return &wsClient{
		conn:     conn,
		server:   s,
		send:     make(chan wsEnvelope, wsSendBuffer),
		topics:   make(map[string]bool),
		pipeSubs: make(map[string]pipeSub),
	}
}

func (c *wsClient) subscribedTo(topic string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.topics[topic]
}

// enqueue delivers msg to the client's outbound queue, non-blocking. A full
// queue means a slow consumer; per policy, the client is dropped rather
// than the sender blocking on it.
func (c *wsClient) enqueue(msg wsEnvelope) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	select {
	case c.send <- msg:
		c.mu.Unlock()
	default:
		c.mu.Unlock()
		c.close()
	}
}

// close tears down the client: detaches every pipeline debug subscription,
// closes the send queue (stopping writeLoop) and the underlying connection.
// Idempotent and safe to call from either loop or from enqueue.
func (c *wsClient) close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		subs := c.pipeSubs
		c.pipeSubs = nil
		c.mu.Unlock()

		for _, sub := range subs {
			sub.unsub()
			close(sub.done)
		}
		close(c.send)
		_ = c.conn.Close()
	})
}

// shutdown sends a normal WS close frame (best-effort) before tearing the
// client down, used for the server-initiated graceful shutdown path only —
// a client-initiated disconnect or a slow-consumer drop just closes.
func (c *wsClient) shutdown() {
	_ = c.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
		time.Now().Add(wsWriteWait))
	c.close()
}

func (c *wsClient) writeLoop() {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	defer c.close()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteJSON(msg); err != nil {
				return
			}
		case <-ticker.C:
			// Transport-level keepalive, distinct from the app-level
			// ping/pong docs/10-api.md documents for clients that prefer
			// to drive liveness themselves.
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *wsClient) readLoop() {
	defer c.close()

	c.conn.SetReadLimit(wsReadLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var in wsInMessage
		if err := json.Unmarshal(data, &in); err != nil {
			continue // malformed frame: ignore and keep the connection open
		}
		c.handleMessage(in)
	}
}

func (c *wsClient) handleMessage(in wsInMessage) {
	switch in.Type {
	case "ping":
		c.enqueue(wsEnvelope{Type: "pong", Topic: "", Data: struct{}{}})
	case "subscribe":
		c.subscribe(in.Topic)
	case "unsubscribe":
		c.unsubscribe(in.Topic)
	default:
		// Unknown message type from the client: docs/10-api.md's client ->
		// server vocabulary is exactly {subscribe, unsubscribe, ping}, so
		// anything else is silently ignored rather than erroring the
		// connection.
	}
}

func (c *wsClient) subscribe(topic string) {
	if isBroadcastTopic(topic) {
		c.mu.Lock()
		c.topics[topic] = true
		c.mu.Unlock()
		return
	}
	if id, ok := strings.CutPrefix(topic, "pipeline:"); ok {
		c.subscribePipeline(topic, id)
		return
	}
	c.enqueue(wsEnvelope{Type: "error", Topic: topic, Data: wsErrorData{
		Code:    "not_found",
		Message: fmt.Sprintf("unknown topic %q", topic),
	}})
}

// subscribePipeline attaches to pipelineID's engine.Manager debug stream,
// after checking the pipeline actually exists (docs/10-api.md's "Invalid
// subscribe (unknown pipeline)" example) — Manager.Subscribe itself never
// fails, so the existence check has to happen here.
func (c *wsClient) subscribePipeline(topic, pipelineID string) {
	if _, err := c.server.store.GetPipeline(pipelineID); err != nil {
		c.enqueue(wsEnvelope{Type: "error", Topic: topic, Data: wsErrorData{
			Code:    "not_found",
			Message: fmt.Sprintf("pipeline %q does not exist", pipelineID),
		}})
		return
	}

	c.mu.Lock()
	if _, already := c.pipeSubs[topic]; already || c.closed {
		c.mu.Unlock()
		return
	}
	ch, unsub := c.server.mgr.Subscribe(pipelineID)
	done := make(chan struct{})
	c.pipeSubs[topic] = pipeSub{unsub: unsub, done: done}
	c.mu.Unlock()

	go c.relaySnapshots(topic, pipelineID, ch, done)
}

// pipelineTickEvent is the docs/10-api.md "pipeline:{id}" topic payload.
// Log is populated from engine.TickSnapshot.Log (v0.4's "log" node,
// docs/08-node-catalog.md), omitted (omitempty) on a tick with nothing to
// log.
type pipelineTickEvent struct {
	PipelineID string         `json:"pipelineId"`
	Tick       uint64         `json:"tick"`
	Nodes      map[string]any `json:"nodes"`
	Log        []logEntry     `json:"log,omitempty"`
}

type logEntry struct {
	NodeID    string    `json:"nodeId"`
	Label     string    `json:"label"`
	Value     any       `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// snapshotNodesJSON converts an engine.TickSnapshot's Nodes map (typed
// engine.Value) into the plain bool/float64 shape docs/10-api.md's example
// shows (e.g. {"n1": 287, "n5": true}).
func snapshotNodesJSON(nodes map[string]engine.Value) map[string]any {
	out := make(map[string]any, len(nodes))
	for k, v := range nodes {
		out[k] = valueJSON(v)
	}
	return out
}

func valueJSON(v engine.Value) any {
	if v.Type == engine.TypeBool {
		return v.Bool
	}
	return v.Float
}

// snapshotLogJSON converts an engine.TickSnapshot's Log entries into the
// wire shape docs/10-api.md's example shows: {nodeId, label, value,
// timestamp}.
func snapshotLogJSON(entries []engine.LogEntry) []logEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]logEntry, len(entries))
	for i, e := range entries {
		out[i] = logEntry{
			NodeID:    e.NodeID,
			Label:     e.Label,
			Value:     valueJSON(e.Value),
			Timestamp: e.Timestamp,
		}
	}
	return out
}

func (c *wsClient) relaySnapshots(topic, pipelineID string, ch <-chan *engine.TickSnapshot, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case snap, ok := <-ch:
			if !ok {
				return
			}
			c.enqueue(wsEnvelope{Type: "event", Topic: topic, Data: pipelineTickEvent{
				PipelineID: pipelineID,
				Tick:       snap.Tick,
				Nodes:      snapshotNodesJSON(snap.Nodes),
				Log:        snapshotLogJSON(snap.Log),
			}})
		}
	}
}

func (c *wsClient) unsubscribe(topic string) {
	c.mu.Lock()
	delete(c.topics, topic)
	sub, ok := c.pipeSubs[topic]
	if ok {
		delete(c.pipeSubs, topic)
	}
	c.mu.Unlock()

	if ok {
		sub.unsub()
		close(sub.done)
	}
}

// handleWS upgrades the connection and runs it until the client
// disconnects or the connection is dropped, per docs/10-api.md's
// "WebSocket /api/v1/ws".
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote an HTTP error response on failure
	}

	c := newWSClient(conn, s)
	s.hub.register(c)
	defer s.hub.unregister(c)

	go c.writeLoop()
	c.readLoop()
}
