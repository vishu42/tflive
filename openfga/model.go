package openfgamodel

import _ "embed"

//go:embed authorization-model.json
var authorizationModelJSON []byte

// AuthorizationModelJSON returns a defensive copy of the canonical model.
func AuthorizationModelJSON() []byte {
	return append([]byte(nil), authorizationModelJSON...)
}
