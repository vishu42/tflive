package strval

import (
	"strings"
	"unicode"
)

// FirstNonEmpty returns the first value that is not the empty string, or "" if
// there is none. It is how a display label is chosen from optional identity
// claims: fall back until something is present.
//
//	("", "alice", "a@b.c") → "alice"
//	("", "")               → ""
//	()                     → ""
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// SafeOpaque reports whether value is usable as an opaque identifier or
// credential in a URL path, a header, or a request body: non-empty and free of
// whitespace and control characters, so it cannot terminate a header or split a
// request.
//
// It says nothing about the value being correct, only about it being safe to
// transmit uninterpreted.
//
//	"01H8XV"      → true
//	"has space"   → false
//	"has\nnewline" → false
//	""            → false
func SafeOpaque(value string) bool {
	return value != "" && strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character)
	}) == -1
}

// Redact removes every occurrence of secret from value. It is applied to
// anything a remote service echoes back before that text reaches an error or a
// log, because a service that quotes the request can otherwise carry a token
// into somewhere it is retained.
//
// An empty secret redacts nothing, rather than matching everywhere: "no secret
// is configured" must not turn a message into a row of markers.
//
//	("token abc leaked", "abc") → "token [REDACTED] leaked"
//	("nothing here", "abc")     → "nothing here"
//	("anything", "")            → unchanged
func Redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
