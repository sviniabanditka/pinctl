package engine

// registry is the package-level NodeType registry keyed by the JSON "type"
// discriminator, per docs/07-pipeline-engine.md "unknown types are caught
// at load time". Populated once at init() with the full v0.4 node catalog
// (docs/08-node-catalog.md).
var registry = map[string]NodeType{}

func register(nt NodeType) {
	registry[nt.Name()] = nt
}

func init() {
	// Sources.
	register(deviceReadType{})
	register(constantType{})
	register(timerType{})
	register(clockType{})

	// Transforms.
	register(compareType{})
	register(thresholdHysteresisType{})
	register(scaleType{})
	register(andType{})
	register(orType{})
	register(notType{})
	register(delayType{})
	register(debounceType{})
	register(edgeDetectType{})
	register(expressionType{})

	// Sinks.
	register(deviceWriteType{})
	register(logType{})
}

// lookupNodeType returns the registered NodeType for typ, or false if
// unknown.
func lookupNodeType(typ string) (NodeType, bool) {
	nt, ok := registry[typ]
	return nt, ok
}
