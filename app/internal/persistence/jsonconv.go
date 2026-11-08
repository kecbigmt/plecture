package persistence

import (
	"database/sql"
	"encoding/json"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// marshalJSONMap encodes m for a nullable _json column: a nil/empty map is
// SQL NULL, matching formatTimeNull/nullString's absence convention.
func marshalJSONMap(m map[string]any) (sql.NullString, error) {
	if len(m) == 0 {
		return sql.NullString{}, nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}

// unmarshalJSONMap is marshalJSONMap's inverse.
func unmarshalJSONMap(s sql.NullString) (map[string]any, error) {
	if !s.Valid {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s.String), &m); err != nil {
		return nil, err
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

// nullInt always reports Valid: a layer's heartbeat counters are a
// legitimate 0, not an absent fact worth a NULL.
func nullInt(v int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

func processFromColumn(s sql.NullString) (*contract.ProcessIdentity, error) {
	var p *contract.ProcessIdentity
	if err := unmarshalJSONValue(s, &p); err != nil {
		return nil, err
	}
	return p, nil
}
