// Package pairing contains deterministic pairing and due-round scheduling.
//
// The engine never mutates caller-owned member maps. Its only nondeterminism is
// supplied by RandomIntGenerator; member and candidate traversal is otherwise
// lexicographically stable. Scheduler delivery intentionally precedes the
// atomic persistence call so a rejected message cannot commit pairing state.
package pairing
