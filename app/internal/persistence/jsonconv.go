package persistence

import (
	"database/sql"
	"encoding/json"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// marshalJSONObject writes a nil map as {} too, so a NOT NULL object column
// has one spelling of "no entries".
func marshalJSONObject[V any](m map[string]V) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// unmarshalJSONObject reads {} back as a nil map, not an empty one: readers
// test a map against nil to tell "never produced" from "produced".
func unmarshalJSONObject[V any](s string) (map[string]V, error) {
	var m map[string]V
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}

// marshalJSONPointer takes a typed pointer rather than any: a typed-nil
// pointer boxed in an interface is a non-nil interface, and would be stored
// as the JSON text null instead of SQL NULL.
func marshalJSONPointer[T any](p *T) (sql.NullString, error) {
	if p == nil {
		return sql.NullString{}, nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}

// unmarshalJSONValue leaves *out untouched (its zero value) when s is NULL.
func unmarshalJSONValue(s sql.NullString, out any) error {
	if !s.Valid {
		return nil
	}
	return json.Unmarshal([]byte(s.String), out)
}

func processFromColumn(s sql.NullString) (*contract.ProcessIdentity, error) {
	var p *contract.ProcessIdentity
	if err := unmarshalJSONValue(s, &p); err != nil {
		return nil, err
	}
	return p, nil
}
