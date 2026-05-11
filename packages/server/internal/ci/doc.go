// Package ci contains lint-style tests that assert structural
// properties of CI workflow files. Production code does not import
// this package; it exists so `go test ./...` exercises the assertions
// from the same toolchain that ships the server.
package ci
