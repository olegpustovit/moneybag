package expressions

import (
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"encoding/json"
)

type referenceValue struct {
	traits.Mapper
	id     string
	fields map[string]any
}

// Reference exposes joined fields without losing the original id's CEL equality.
func Reference(id string, fields map[string]any) ref.Val {
	return referenceValue{types.NewStringInterfaceMap(decimalAdapter{}, fields), id, fields}
}
func (v referenceValue) ReferenceID() string { return v.id }
func (v referenceValue) Equal(other ref.Val) ref.Val {
	if text, ok := other.(types.String); ok {
		return types.Bool(v.id == string(text))
	}
	if linked, ok := other.(interface{ ReferenceID() string }); ok {
		return types.Bool(v.id == linked.ReferenceID())
	}
	return v.Mapper.Equal(other)
}
func (v referenceValue) MarshalJSON() ([]byte, error) { return json.Marshal(v.fields) }
