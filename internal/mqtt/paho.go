// This file is the only one in the package that touches
// github.com/eclipse/paho.mqtt.golang. It owns connect, reconnect,
// backoff and the last-will-and-testament (LWT) — all things paho already
// does well (SetWill, SetAutoReconnect, SetConnectRetry, OnConnect) — and
// contains no bridge policy of its own: it does not know what a device,
// deadband, or discovery config is. bridge.go depends only on the narrow
// Client interface (mqtt.go), which is why bridge.go's tests run against a
// fake instead of a real broker.
package mqtt

import (
	"fmt"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/sviniabanditka/pinctl/internal/config"
)

// pahoClient adapts a paho.Client to this package's narrow Client
// interface.
type pahoClient struct {
	c paho.Client
}

func (p *pahoClient) Publish(topic string, qos byte, retained bool, payload []byte) error {
	token := p.c.Publish(topic, qos, retained, payload)
	token.Wait()
	return token.Error()
}

func (p *pahoClient) Subscribe(topic string, qos byte, cb func(topic string, payload []byte)) error {
	token := p.c.Subscribe(topic, qos, func(_ paho.Client, msg paho.Message) {
		cb(msg.Topic(), msg.Payload())
	})
	token.Wait()
	return token.Error()
}

func (p *pahoClient) Disconnect() {
	// 250ms quiesce: long enough to flush the LWT-cancelling DISCONNECT
	// packet, short enough that shutdown never visibly hangs.
	p.c.Disconnect(250)
}

// buildClientOptions renders cfg into paho's ClientOptions, including the
// LWT (docs/15-mqtt-bridge.md "Availability": "last will of offline on
// pinctl/status, retained") and the auto-reconnect-forever policy
// (docs/15's "Failure modes": "retry with backoff forever"). Factored out
// from NewPahoClient so a test can inspect the will/reconnect settings
// without dialing a broker.
func buildClientOptions(cfg config.MQTT, onConnect func(paho.Client)) *paho.ClientOptions {
	opts := paho.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	opts.SetClientID(cfg.ClientID)
	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
	}
	if cfg.Password != "" {
		opts.SetPassword(string(cfg.Password))
	}

	opts.SetBinaryWill(statusTopic(cfg.BaseTopic), []byte("offline"), 1, true)

	// Retry the initial connect, and reconnect after a drop, forever with
	// backoff — the bridge is never on the critical path
	// (docs/15's "Broker unreachable at startup").
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(2 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(time.Minute)

	opts.SetOnConnectHandler(onConnect)

	return opts
}

// NewPahoClient is the production ClientFactory: it builds a paho client
// for cfg, starts connecting (asynchronously — Connect returns
// immediately; ConnectRetry means it keeps trying in the background
// rather than the returned Token ever failing terminally), and arranges
// for onConnect to be called, with a Client wrapping the same
// paho.Client, on every successful (re)connect.
func NewPahoClient(cfg config.MQTT, onConnect func(Client)) (Client, error) {
	pc := &pahoClient{}
	opts := buildClientOptions(cfg, func(c paho.Client) {
		pc.c = c
		onConnect(pc)
	})

	pc.c = paho.NewClient(opts)
	token := pc.c.Connect()
	// ConnectRetry means Connect() itself returns almost immediately; this
	// just surfaces a synchronous config error (e.g. an unparsable
	// broker) rather than one only ever seen in a log line.
	if !token.WaitTimeout(5*time.Second) && token.Error() == nil {
		// Still trying (broker unreachable) — that's fine, expected, and
		// exactly what ConnectRetry is for; proceed without blocking Run
		// any further.
		return pc, nil
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("mqtt: connect: %w", err)
	}
	return pc, nil
}
