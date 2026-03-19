// Package workflow implements the elevation request state machine.
//
// States: PENDING -> APPROVED | REJECTED -> ACTIVE -> EXPIRED | REVOKED
//
// Every state transition writes an immutable audit log entry before updating
// the request state (write-ahead audit log pattern). Transitions use
// database row-level locking to prevent races in HA deployments.
//
// License: Elastic License 2.0 (ELv2)
package workflow
