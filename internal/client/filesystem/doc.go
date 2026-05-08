// Package filesystem is the filesystem-backed implementation of the unified
// muster client interface defined in the parent client package. Each muster
// CRD is stored as a YAML file under per-resource-type folders rooted at
// basePath. See ../doc.go for the full architecture.
package filesystem
