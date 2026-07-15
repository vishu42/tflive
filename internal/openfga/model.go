package openfga

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

type AuthorizationModel struct {
	ID              string                     `json:"id,omitempty"`
	SchemaVersion   string                     `json:"schema_version"`
	TypeDefinitions []TypeDefinition           `json:"type_definitions"`
	Conditions      map[string]json.RawMessage `json:"conditions,omitempty"`
}

type TypeDefinition struct {
	Type      string                     `json:"type"`
	Relations map[string]json.RawMessage `json:"relations,omitempty"`
	Metadata  TypeDefinitionMetadata     `json:"metadata,omitempty"`
}

type TypeDefinitionMetadata struct {
	Relations map[string]RelationMetadata `json:"relations,omitempty"`
}

type RelationMetadata struct {
	DirectlyRelatedUserTypes []RelationReference `json:"directly_related_user_types,omitempty"`
}

type RelationReference struct {
	Type      string          `json:"type"`
	Relation  string          `json:"relation,omitempty"`
	Wildcard  json.RawMessage `json:"wildcard,omitempty"`
	Condition string          `json:"condition,omitempty"`
}

func ParseAuthorizationModel(data []byte) (AuthorizationModel, error) {
	var model AuthorizationModel
	if err := json.Unmarshal(data, &model); err != nil {
		return AuthorizationModel{}, fmt.Errorf("decode authorization model: %w", err)
	}
	if model.SchemaVersion != "1.1" {
		return AuthorizationModel{}, fmt.Errorf("authorization model schema_version must be 1.1")
	}
	if len(model.TypeDefinitions) == 0 {
		return AuthorizationModel{}, fmt.Errorf("authorization model type_definitions must not be empty")
	}
	seen := make(map[string]struct{}, len(model.TypeDefinitions))
	for _, definition := range model.TypeDefinitions {
		if definition.Type == "" {
			return AuthorizationModel{}, fmt.Errorf("authorization model type must not be empty")
		}
		if _, exists := seen[definition.Type]; exists {
			return AuthorizationModel{}, fmt.Errorf("authorization model has duplicate type %q", definition.Type)
		}
		seen[definition.Type] = struct{}{}
		for relation, rewrite := range definition.Relations {
			if relation == "" || !json.Valid(rewrite) {
				return AuthorizationModel{}, fmt.Errorf("authorization model type %q has invalid relation %q", definition.Type, relation)
			}
		}
	}
	return model, nil
}

func (model AuthorizationModel) TypeDefinition(name string) (TypeDefinition, bool) {
	for _, definition := range model.TypeDefinitions {
		if definition.Type == name {
			return definition, true
		}
	}
	return TypeDefinition{}, false
}

func CanonicalJSON(model AuthorizationModel) ([]byte, error) {
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("copy authorization model: %w", err)
	}
	normalized, err := ParseAuthorizationModel(encoded)
	if err != nil {
		return nil, fmt.Errorf("validate authorization model: %w", err)
	}
	normalized.ID = ""
	sort.Slice(normalized.TypeDefinitions, func(i, j int) bool {
		return normalized.TypeDefinitions[i].Type < normalized.TypeDefinitions[j].Type
	})
	for index := range normalized.TypeDefinitions {
		definition := &normalized.TypeDefinitions[index]
		for name, rewrite := range definition.Relations {
			canonical, err := canonicalRawJSON(rewrite)
			if err != nil {
				return nil, fmt.Errorf("canonicalize %s#%s: %w", definition.Type, name, err)
			}
			definition.Relations[name] = canonical
		}
		for name, metadata := range definition.Metadata.Relations {
			sort.Slice(metadata.DirectlyRelatedUserTypes, func(i, j int) bool {
				left, _ := json.Marshal(metadata.DirectlyRelatedUserTypes[i])
				right, _ := json.Marshal(metadata.DirectlyRelatedUserTypes[j])
				return bytes.Compare(left, right) < 0
			})
			definition.Metadata.Relations[name] = metadata
		}
	}
	for name, condition := range normalized.Conditions {
		canonical, err := canonicalRawJSON(condition)
		if err != nil {
			return nil, fmt.Errorf("canonicalize condition %s: %w", name, err)
		}
		normalized.Conditions[name] = canonical
	}
	return json.Marshal(normalized)
}

func ModelsEqual(left, right AuthorizationModel) (bool, error) {
	leftJSON, err := CanonicalJSON(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := CanonicalJSON(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func canonicalRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
