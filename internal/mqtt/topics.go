package mqtt

import "strings"

// Topic layout, per docs/15-mqtt-bridge.md "Topics". base is
// config.MQTT.BaseTopic (default "pinctl").

func statusTopic(base string) string { return base + "/status" }

func stateTopic(base, deviceID string) string { return base + "/" + deviceID + "/state" }

func availableTopic(base, deviceID string) string { return base + "/" + deviceID + "/available" }

func setTopic(base, deviceID string) string { return base + "/" + deviceID + "/set" }

// commandFilter is the subscription filter matching every device's set
// topic: "<base>/+/set".
func commandFilter(base string) string { return base + "/+/set" }

// deviceIDFromSetTopic extracts "<device>" from a topic matching
// "<base>/<device>/set", or reports ok=false if it doesn't match that
// shape.
func deviceIDFromSetTopic(base, topic string) (deviceID string, ok bool) {
	prefix := base + "/"
	const suffix = "/set"
	if !strings.HasPrefix(topic, prefix) || !strings.HasSuffix(topic, suffix) {
		return "", false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(topic, prefix), suffix)
	if mid == "" || strings.Contains(mid, "/") {
		return "", false
	}
	return mid, true
}

// discoveryTopic is "<prefix>/<component>/pinctl_<device>/config", per
// docs/15's Topics table.
func discoveryTopic(prefix, component, deviceID string) string {
	return prefix + "/" + component + "/pinctl_" + deviceID + "/config"
}
