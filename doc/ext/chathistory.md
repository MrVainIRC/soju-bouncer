# CHATHISTORY synchronization

Soju stores and replays reaction events as raw IRC messages. A react, unreact,
or legacy react-toggle event is not consolidated into a server-side snapshot.
Its original `msgid`, `time`, `+reply`, sender, and relative order are preserved.

Soju advertises `MSGREFTYPES=msgid,timestamp`. Clients should prefer `msgid`
references for `AFTER`, `BEFORE`, `BETWEEN`, `AROUND`, and bounded `LATEST`
queries. A message ID identifies an exact event even when multiple events have
the same timestamp. Cursor resolution is scoped to the authenticated user's
network and target; a cursor from another buffer is rejected.

The two `BETWEEN` bounds are independent. Each may be either `msgid` or
`timestamp`, including mixed `msgid`/`timestamp` ranges in either direction.
Soju resolves both bounds to internal store positions before selecting events.
Both bounds are exclusive. The limit is counted away from the first selector,
whether the range runs forwards or backwards; returned events remain in the
store's stable ascending order. Internal database IDs and file offsets provide
a deterministic tie-breaker for events with identical timestamps.

Timestamp references remain available as a fallback. They identify a position
in time rather than one unique event, so queries around identical timestamps
cannot express an event-specific boundary. File-store timestamps retain
millisecond precision for new entries.

History windows may overlap. Clients should deduplicate live and replayed
events by their event `msgid`, especially after reconnecting or when paginating
through overlapping ranges. Reaction state remains a client responsibility:
clients reconstruct it by processing react, unreact, and legacy toggle events
in the replayed order.

React and unreact `TAGMSG` events are available to clients that negotiated
`message-tags` without requiring `draft/event-playback`. Other playback events
remain gated by `draft/event-playback`.
