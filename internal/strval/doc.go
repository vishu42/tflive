// Package strval contains the small string predicates and transforms that more
// than one boundary needs. It imports nothing beyond the standard library, so
// any package may depend on it without acquiring a direction.
//
// Each function here replaced two or three identical copies living in separate
// packages. Nothing belongs in this package until a second package needs it.
package strval
