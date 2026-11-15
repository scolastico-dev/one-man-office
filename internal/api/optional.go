package api

import "encoding/json"

// Optional distinguishes the three states a PATCH body field can be in: the
// key is absent (unchanged), the key is null (clear or inherit), or the key
// carries a value (set). Use it with the `omitzero` JSON option so an absent
// field is not marshaled.
type Optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// Some returns an Optional that sets v.
func Some[T any](v T) Optional[T] { return Optional[T]{Set: true, Value: v} }

// Null returns an Optional that clears the field.
func Null[T any]() Optional[T] { return Optional[T]{Set: true, Null: true} }

// IsZero reports whether the field is absent. encoding/json uses it for omitzero.
func (o Optional[T]) IsZero() bool { return !o.Set }

// UnmarshalJSON is only called when the key is present.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	var zero T
	o.Set = true
	if string(data) == "null" {
		o.Null = true
		o.Value = zero
		return nil
	}
	o.Null = false
	o.Value = zero
	return json.Unmarshal(data, &o.Value)
}

// MarshalJSON writes null for a cleared field and the value otherwise.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Null {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}
