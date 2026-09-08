package githubapp

import "errors"

// ErrAppNotInstalled reports that the GitHub App has no installation covering
// the requested repository.
//
// It is the single most likely failure in practice -- someone registers a
// template before an org admin installs the App on that repo -- so callers
// match on it to produce an actionable message instead of surfacing a raw HTTP
// status or, worse, a git authentication error several steps later.
var ErrAppNotInstalled = errors.New("github app is not installed on the repository")
