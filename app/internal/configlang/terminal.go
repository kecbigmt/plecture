package configlang

// ValidatePlan checks the terminal capabilities one workflow's plan
// consumes. A capability resolves only when some effect in that plan
// declares the verb, so a channel or an effect that consumes one outside
// such a plan is a load error. A reference the registry cannot resolve is
// left to reference resolution, which reports it in its own layer.
func (v Validation) ValidatePlan(workflow *Definition, r *Registry) error {
	var members []*Definition
	nodes, err := tableArray(workflow.Body, "nodes", Position{File: workflow.File, Path: workflow.ID})
	if err != nil {
		return err
	}
	offered := map[string]bool{}
	for _, node := range nodes {
		ref, ok := node["uses"].(string)
		if !ok {
			continue
		}
		effect, err := r.Resolve(ref, v.From)
		if err != nil {
			continue
		}
		members = append(members, effect)
		for _, verb := range terminalVerbsOffered(effect) {
			offered[verb] = true
		}
	}
	for _, channel := range v.eventChannels(workflow) {
		ref, ok := channel["uses"].(string)
		if !ok {
			continue
		}
		def, err := r.Resolve(ref, v.From)
		if err != nil {
			continue
		}
		members = append(members, def)
	}
	for _, member := range members {
		for _, verb := range terminalVerbsConsumed(member.Body) {
			if offered[verb] {
				continue
			}
			return newDiag(CodeTerminalUnavailable, LayerSemantic,
				Position{File: member.File, Path: member.ID},
				"no effect in the plan declares the terminal verb "+verb)
		}
	}
	return nil
}

// terminalVerbsOffered reports the verbs an effect's interactive endpoint
// declares.
func terminalVerbsOffered(def *Definition) []string {
	tbl, ok := def.Body["terminal"].(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for _, verb := range terminalVerbOrder {
		if _, ok := tbl[verb]; ok {
			out = append(out, verb)
		}
	}
	return out
}

// terminalVerbsConsumed reports the verbs a definition consumes anywhere in
// its body. The scan is shape-driven rather than surface-driven because
// which surfaces may consume a capability is already settled by value
// validation, and availability is a property of the whole definition.
func terminalVerbsConsumed(body any) []string {
	var out []string
	switch v := body.(type) {
	case map[string]any:
		if verb, ok := v["terminal"].(string); ok && len(v) == 1 {
			return []string{verb}
		}
		for _, key := range sortedKeys(v) {
			out = append(out, terminalVerbsConsumed(v[key])...)
		}
	case []any:
		for _, item := range v {
			out = append(out, terminalVerbsConsumed(item)...)
		}
	case []map[string]any:
		for _, item := range v {
			out = append(out, terminalVerbsConsumed(item)...)
		}
	}
	return out
}
