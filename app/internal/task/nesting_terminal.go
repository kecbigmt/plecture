package task

// TerminalLayer returns the index of the layer that declares `[terminal]`,
// or -1 when none does. At most one layer of a chain may declare it, so the
// lookup needs no tie-break.
func TerminalLayer(layers []ResolvedLayer) int {
	for i, layer := range layers {
		if layer.Terminal.IsDeclared() {
			return i
		}
	}
	return -1
}
