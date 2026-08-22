package config

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// DocumentChain is one `[[chains]]` entry in a task document's frontmatter: a
// deterministic rule for spawning task off that document's instances — once
// this instance reaches this state, run that workflow.
//
// It lives in the declaring document because a chain's `when` judge ids and
// `inputs` projections are references into that same document's completion
// contract and observed keys, which is what lets them be checked at load
// rather than at fire time. Evaluation is scoped the same way: a chain fires
// only against instances of the document that declared it.
type DocumentChain struct {
	ID        string
	Workflow  string
	Placement string
	When      ChainWhen
	// Resource is the resource the spawned session binds to, as a value over
	// the same roots the inputs read. Absent, the spawned session inherits
	// the declaring session's resource — which is what every chain did before
	// one could name its own.
	Resource *lang.Value
	// Inputs are the session inputs a fire hands the spawned workflow, as
	// values over the chain-input roots. A chain's target is topology, so
	// `workflow` beside them is a static reference and never a value.
	Inputs     map[string]*lang.Value
	TaskID     string
	SourcePath string
}

// EffectivePlacement returns the placement, defaulting to sibling.
func (c DocumentChain) EffectivePlacement() string {
	if c.Placement == "" {
		return ChainPlacementSibling
	}
	return c.Placement
}

// InputKeys lists the declared input names in a stable order.
func (c DocumentChain) InputKeys() []string {
	keys := make([]string, 0, len(c.Inputs))
	for key := range c.Inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Validate checks a chain's structural invariants that do not need the rest
// of the layer. The reference checks — the target workflow, the judge ids,
// the keys each projection reads — belong to ValidateTaskDocuments, which has
// the contracts they resolve against.
func (c DocumentChain) Validate() error {
	if !chainIDRE.MatchString(c.ID) {
		return fmt.Errorf("chain id %q is empty or has characters outside %s", c.ID, chainIDRE.String())
	}
	if c.Workflow == "" {
		return fmt.Errorf("chain %q: `workflow` is required (the workflow a fire spawns)", c.ID)
	}
	switch c.Placement {
	case "", ChainPlacementSibling, ChainPlacementChild:
	default:
		return fmt.Errorf("chain %q: placement %q is not %q or %q", c.ID, c.Placement, ChainPlacementSibling, ChainPlacementChild)
	}
	if len(c.When.All) == 0 {
		return fmt.Errorf("chain %q: `when.all` declares no facts; a chain with no trigger would fire unconditionally", c.ID)
	}
	for i, fact := range c.When.All {
		if err := fact.validate(); err != nil {
			return fmt.Errorf("chain %q when.all[%d]: %w", c.ID, i, err)
		}
	}
	return nil
}

// documentChainsFrom reads a document's `[[chains]]` off its parsed
// declaration. `when` goes through the typed decoder that already owns a
// trigger's shape, and each input through the language's own value parser, so
// neither is specified twice.
func documentChainsFrom(def *lang.Definition, path string) ([]DocumentChain, error) {
	raw, ok := def.Body["chains"]
	if !ok {
		return nil, nil
	}
	entries, ok := asTableArray(raw)
	if !ok {
		return nil, fmt.Errorf("`chains` is an array of tables")
	}
	out := make([]DocumentChain, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for i, entry := range entries {
		ch := DocumentChain{TaskID: def.ID, SourcePath: path}
		for _, field := range []struct {
			key    string
			target *string
		}{
			{"id", &ch.ID},
			{"workflow", &ch.Workflow},
			{"placement", &ch.Placement},
		} {
			value, present := entry[field.key]
			if !present {
				continue
			}
			text, isText := value.(string)
			if !isText {
				return nil, fmt.Errorf("chains[%d]: `%s` is a string", i, field.key)
			}
			*field.target = text
		}
		if raw, declared := entry["resource"]; declared {
			resource, err := lang.ParseValue(raw, lang.ClassData, lang.Position{File: path, Path: fmt.Sprintf("%s.chains[%d].resource", def.ID, i)})
			if err != nil {
				return nil, err
			}
			ch.Resource = resource
		}
		when, err := decodeChainWhen(entry)
		if err != nil {
			return nil, fmt.Errorf("chains[%d]: %w", i, err)
		}
		ch.When = when
		if ch.Inputs, err = parseChainInputs(entry, path, fmt.Sprintf("%s.chains[%d].inputs", def.ID, i), i); err != nil {
			return nil, err
		}
		if err := ch.Validate(); err != nil {
			return nil, err
		}
		if seen[ch.ID] {
			return nil, fmt.Errorf("chain id %q is declared more than once", ch.ID)
		}
		seen[ch.ID] = true
		out = append(out, ch)
	}
	return out, nil
}

func decodeChainWhen(entry map[string]any) (ChainWhen, error) {
	var decoded struct {
		When ChainWhen `toml:"when"`
	}
	raw, ok := entry["when"]
	if !ok {
		return ChainWhen{}, nil
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(map[string]any{"when": raw}); err != nil {
		return ChainWhen{}, err
	}
	if _, err := toml.Decode(encoded.String(), &decoded); err != nil {
		return ChainWhen{}, err
	}
	return decoded.When, nil
}

func parseChainInputs(entry map[string]any, file, path string, index int) (map[string]*lang.Value, error) {
	raw, ok := entry["inputs"]
	if !ok {
		return nil, nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("chains[%d]: `inputs` is a table", index)
	}
	out := make(map[string]*lang.Value, len(tbl))
	for key, value := range tbl {
		parsed, err := lang.ParseValue(value, lang.ClassData, lang.Position{File: file, Path: path + "." + key})
		if err != nil {
			return nil, err
		}
		out[key] = parsed
	}
	return out, nil
}

// asTableArray reads a TOML array of tables in either shape the decoder
// produces for one.
func asTableArray(raw any) ([]map[string]any, bool) {
	switch entries := raw.(type) {
	case []map[string]any:
		return entries, true
	case []any:
		out := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			tbl, ok := entry.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, tbl)
		}
		return out, true
	}
	return nil, false
}
