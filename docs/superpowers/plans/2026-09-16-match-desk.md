# Match operations implementation plan

**Goal:** Give captains match cards and a readiness deadline, and give administrators a persistent queue of matches needing help.

**Design approved in chat:** implement the suggested functions in dependency order. First ship judge requests, attention, and pause controls together with the readiness state they need. Keep Challonge as the source of pairings and results. Do not change the in-progress result submission integration.

**Architecture:** A separate match desk consumes the existing bracket cache. Its state and pending notifications are committed together in PostgreSQL. Telegram callbacks carry the match incarnation to reject stale buttons. Telegram delivery retries pending notifications after failures and restarts. All times are UTC internally.

## Tasks

- [ ] Add scenario tests for readiness, authorization, duplicate judge requests, overlapping global/local pauses, and changed pairs.
- [ ] Implement the persistent match operations state machine in `internal/application/match_desk.go` and types in `internal/models/match_desk.go`.
- [ ] Add a transactional PostgreSQL store and migration 000028. Serialize operations and read the bracket under a shared lock; do not modify the existing bracket cache writer.
- [ ] Add Telegram commands, buttons, notifications and background processing in a separate delivery file; wire before bot startup.
- [ ] Verify focused tests, the full Go suite and SQL integration where a disposable database is available. Document commands and operational limits.

## Behaviour

Readiness lasts ten minutes, beginning at the later of pair announcement and tournament start. Only participating captains may act. Alert administrators on expiry, with no automatic forfeits. Both captains receive updated cards. Judge requests have a reason and remain visible until resolved by an administrator. Administrators can pause a match or all match timers; overlapping pauses must count once. Paused time does not consume readiness or match monitoring time. Replaced/reopened matches receive new callback tokens and readiness state. Notifications are durable, delivered at least once, and acknowledged only after Telegram accepts them.

## Follow-on order

Once the operational core is verified: lobby creation details, one mutually agreed five-minute extension, long-match status checks, and match history. Result confirmation and upcoming-opponent notices depend on the completed bracket/report integration and must preserve its result semantics.
