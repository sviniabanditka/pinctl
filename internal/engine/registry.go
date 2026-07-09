package engine

// registry is the package-level NodeType registry keyed by the JSON "type"
// discriminator, per docs/07-pipeline-engine.md "unknown types are caught
// at load time". Populated once at init() with the v0.2 minimal node set
// (docs/08-node-catalog.md): device-read, constant, compare, device-write.
var registry = map[string]NodeType{}

func register(nt NodeType) {
	registry[nt.Name()] = nt
}

func init() {
	register(deviceReadType{})
	register(constantType{})
	register(compareType{})
	register(deviceWriteType{})
}

// lookupNodeType returns the registered NodeType for typ, or false if
// unknown.
func lookupNodeType(typ string) (NodeType, bool) {
	nt, ok := registry[typ]
	return nt, ok
}
