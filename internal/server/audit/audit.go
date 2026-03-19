// Package audit implements the append-only, tamper-evident audit log.
// Each entry includes a SHA-256 hash of the previous entry, forming a
// hash chain. Entries are never updated or deleted.
//
// License: Elastic License 2.0 (ELv2)
package audit
