package configlang

import "fmt"

// cascadeWholeTableFields replace wholesale rather than merge: workflows.md
// calls out `tick` and `healthcheck` as runtime tuning tables a deeper
// cascade layer overwrites entirely.
var cascadeWholeTableFields = map[string]bool{
	"tick":        true,
	"healthcheck": true,
}

// MergeLayer combines a shallower layer's definitions with a deeper layer's,
// applying declarations.md's Namespaces rules: a deeper whole-definition-kind
// definition replaces a same-id, same-kind shallower one; a deeper workflow
// merges into a same-id shallower workflow by the cascade rule; a same id
// under different kinds is a load error. Each input is assumed already free
// of in-layer duplicates (DiscoverRoot enforces that).
func MergeLayer(shallower, deeper []*Definition) ([]*Definition, error) {
	byID := make(map[string]*Definition, len(shallower))
	order := make([]string, 0, len(shallower))
	for _, d := range shallower {
		byID[d.ID] = d
		order = append(order, d.ID)
	}
	for _, d := range deeper {
		prior, exists := byID[d.ID]
		if !exists {
			byID[d.ID] = d
			order = append(order, d.ID)
			continue
		}
		if prior.Kind != d.Kind {
			return nil, newDiag(CodeIDDuplicate, LayerSemantic, Position{File: d.File, Path: d.ID},
				fmt.Sprintf("id %q is declared as kind %q in %s and kind %q in %s", d.ID, prior.Kind, prior.File, d.Kind, d.File))
		}
		if d.Kind == KindWorkflow {
			body, err := mergeCascade(prior.Body, d.Body)
			if err != nil {
				return nil, fmt.Errorf("workflow %q: %w", d.ID, err)
			}
			byID[d.ID] = &Definition{ID: d.ID, Kind: KindWorkflow, Body: body, File: d.File}
			continue
		}
		// A whole-definition kind: the deeper definition replaces the
		// shallower one outright.
		byID[d.ID] = d
	}
	merged := make([]*Definition, len(order))
	for i, id := range order {
		merged[i] = byID[id]
	}
	return merged, nil
}

// mergeCascade merges a deeper workflow fragment's fields into a shallower
// workflow's, per workflows.md: a cascade layer may add a field the
// shallower layer did not set, but not redeclare one it did — except `tick`
// and `healthcheck`, which it replaces wholesale, and `nodes`, an
// array-of-tables field where a deeper layer's entries append (the same rule
// declarations.md states for one layer's own split files, extended across
// the cascade so an overlay can add nodes without being able to modify the
// base layer's).
func mergeCascade(shallower, deeper map[string]any) (map[string]any, error) {
	merged := make(map[string]any, len(shallower)+len(deeper))
	for k, v := range shallower {
		merged[k] = v
	}
	for k, dv := range deeper {
		sv, exists := merged[k]
		switch {
		case !exists:
			merged[k] = dv
		case cascadeWholeTableFields[k]:
			merged[k] = dv
		case k == "nodes":
			sArr, sOK := asTableArray(sv)
			dArr, dOK := asTableArray(dv)
			if !sOK || !dOK {
				return nil, fmt.Errorf("field %q: expected an array of tables in both layers", k)
			}
			combined := make([]any, 0, len(sArr)+len(dArr))
			for _, n := range sArr {
				combined = append(combined, n)
			}
			for _, n := range dArr {
				combined = append(combined, n)
			}
			merged[k] = combined
		default:
			return nil, fmt.Errorf("field %q is set by a shallower layer; a cascade layer may add fields but not redeclare one", k)
		}
	}
	return merged, nil
}
