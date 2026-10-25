package authn

// ReauthenticationMachineRevoke binds a fresh, single-use browser proof to the
// machine identifier. Fleet consumes it in the revocation transaction.
const ReauthenticationMachineRevoke = "machine_revoke"

// ReauthenticationSystemEventReplay binds a fresh browser proof to a subscriber.
const ReauthenticationSystemEventReplay = "system_event_replay"
