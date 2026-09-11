// Package githubapp authenticates tflive to GitHub as a GitHub App.
//
// It signs a short-lived App JWT with the App's RSA key, resolves which
// installation covers a given repository, and exchanges the JWT for a
// repo-scoped, read-only installation token that expires within the hour. That
// token is what the worker hands to git to clone a private repository.
//
// Nothing here is persisted: an installation is resolved on demand rather than
// stored, so no schema or lifecycle exists to keep in sync with GitHub.
package githubapp
