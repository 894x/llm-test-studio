// Package credentials keeps secret material outside SQLite, JSON, reports,
// events, and logs. Persistent application state carries only StoreRef values.
//
// The operating-system APIs wrapped by go-keyring are synchronous and cannot
// be interrupted after a call begins. OSStore therefore checks context before
// and after every platform call; cancellation may wait for the platform call
// and a mutating call may have completed even when context cancellation is
// returned. Resolve a credential to one short-lived Lease at a run boundary,
// reuse its bytes for that run, then Close it. Do not fetch it repeatedly in a
// request hot path.
package credentials
