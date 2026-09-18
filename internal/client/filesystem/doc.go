// Package filesystem is the filesystem-backed implementation of the unified
// muster client interface defined in the parent client package. Each muster
// CRD is stored as a YAML file under per-resource-type folders rooted at
// basePath; the status muster records for it is kept apart, under status/ at
// the same relative path, so that no status write ever touches a definition
// file. See ../doc.go for the full architecture.
package filesystem
