// Package encryption owns symmetric encryption of values at rest: the AES-GCM
// Cipher, its key decoding, and nothing else. Callers that need a value sealed
// in the database go through Cipher rather than reaching for crypto/aes
// themselves.
//
// It deliberately does not own "security helpers" generally -- scrubbing a
// secret out of text is strval.Redact, and minting an unguessable token is
// authn.NewOpaqueToken.
package encryption
