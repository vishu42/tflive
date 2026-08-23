// Package openfgamodel embeds the OpenFGA authorization model that
// cmd/openfga-provisioner writes.
//
// authorization-model.json is generated from authorization-model.fga by
// scripts/openfga-model.sh; edit the DSL and regenerate rather than editing the
// JSON, which TestCommittedJSONMatchesDSL rejects when the two drift.
package openfgamodel

import _ "embed"

//go:embed authorization-model.json
var authorizationModelJSON []byte

// AuthorizationModelJSON returns a defensive copy of the canonical model.
func AuthorizationModelJSON() []byte {
	return append([]byte(nil), authorizationModelJSON...)
}
