# Sojuᶠ

This fork extends soju with current IRCv3 features for replies, reactions,
multiline messages, history, metadata, and bot mode.

## Protocol support

In this table, **client** refers to soju's connection to an upstream IRCd and
**server** refers to IRC clients connecting downstream to soju.

| Feature | Soju as client | Soju as server |
| --- | --- | --- |
| `draft/reply` / `+reply` | Yes | Yes |
| `draft/react` / `+react` | Yes | Yes |
| `draft/unreact` / `+unreact` | Yes | Yes |
| Legacy reaction toggles | Yes | Yes |
| `draft/multiline` | Yes | Yes |
| `draft/chathistory` | Yes | Yes |
| `draft/event-playback` | Stores incoming events | Yes |
| `message-tags` | Yes | Yes |
| `server-time` and `msgid` | Yes | Yes |
| `batch` | Yes | Yes |
| Metadata-2 | Yes | Yes |
| Metadata-3 | Yes | Yes |
| Metadata-Notify-2 | Yes, with Metadata-2 | Via Metadata-2 synchronization |
| `BOT` ISUPPORT and bot mode | Yes | Yes |
| Extended BouncerServ help | Not applicable | Yes |

Soju never requests Metadata-2 and Metadata-3 at the same time. Metadata-3 is
preferred when both drafts are available upstream. A downstream connection can
also enable at most one metadata dialect.

Reply, React/Unreact, and multiline events retain their raw tags, reference
`msgid`, event `msgid`, `time`, ordering, and batch association. This applies
to both database and filesystem history stores.

Reaction events can be replayed to downstream clients that negotiated
`message-tags` or `draft/event-playback`. Other playback events remain gated
by `draft/event-playback`.

Soju does not reconstruct reaction state and never converts React into Unreact
or Unreact into React. Explicit Unreact events are forwarded unchanged. A
second identical React from a legacy client remains a second raw event, and
the client is responsible for applying toggle semantics.

Multiline batches retain their tags and ordering during live forwarding and
history playback. Stored upstream batch IDs are mapped to safe internal IDs,
and replayed batch tags consistently reference the newly generated downstream
batch ID.

Upstream bot mode advertised through the `BOT` ISUPPORT token is reflected
downstream. This includes bot tags on messages from soju's own upstream
identity and bot information in WHO and WHOIS responses.

## Bouncer network configuration

### `bouncer-network-bind true|false`

Controls network selection for clients using `soju.im/bouncer-networks`. The
default is `false`, preserving the standard soju behavior.

When enabled, an authenticated connection without an explicit network remains
on the bouncer root session until `BOUNCER BIND <netid>` selects a network.
Soju exposes network discovery on that root connection. After a connection
uses `BOUNCER BIND`, soju disables `soju.im/bouncer-networks` and
`soju.im/bouncer-networks-notify` on the bound network connection. Discovery
therefore remains available only on the unbound root connection.

If the authentication username already selects a network, for example
`user/network@client`, soju uses that network directly and disables
`soju.im/bouncer-networks` and `soju.im/bouncer-networks-notify` on the
connection. This prevents clients that already selected a network from
starting a second discovery and bind flow.

## Metadata configuration

### `metadata-upstream-policy any|none|last-active`

Controls which stored self-metadata state is published upstream. The default
is `any`.

- `any` publishes metadata changes from eligible downstream clients.
- `none` keeps self metadata local and does not publish it upstream.
- `last-active` stores a separate desired profile for each stable downstream
  client ID and network. Real user activity activates that client's profile.
  Soju publishes only values that differ from the confirmed global network
  state.

Empty and cleared keys are part of the stored profile. When a client becomes
active again, soju restores both its set values and its cleared state.

Real outgoing user actions such as `PRIVMSG`, `NOTICE`, and `TAGMSG` count as
activity. Automatic synchronization, CAP, PING/PONG, CHATHISTORY, and metadata
notifications do not trigger a profile switch.

### `metadata-client-sync true|false`

Controls synchronization of the confirmed self-metadata state to all
metadata-capable downstream clients on the same network. The default is
`true`.

When enabled, synchronization follows the confirmed upstream state:

1. Soju publishes a Metadata value or clear operation upstream.
2. A real asynchronous Metadata notification from the IRCd is treated as
   authoritative. Soju stores the confirmed network state and forwards the
   notification to every subscribed, metadata-capable downstream client on
   that network.
3. Some IRCds return only a labeled `761` or `766` command reply and do not
   send a separate asynchronous notification. Soju stores the confirmed value
   from that reply and waits briefly for a possible IRCd notification.
4. If no matching notification arrives within 75 milliseconds, soju generates
   the corresponding downstream notification as a fallback.
5. If the real notification arrives, it is used instead. Matching values,
   pending deliveries, and the stored confirmed state prevent the fallback
   and the IRCd notification from being delivered twice.

The originating client is included when a notification is needed, because
some clients update their visible Metadata state from notifications rather
than command replies. Per-client desired profiles used by `last-active` remain
separate from this shared confirmed network state.

When `metadata-client-sync` is disabled, soju suppresses locally generated
client-to-client notifications, including the `761`/`766` fallback. Real
asynchronous upstream notifications still follow the normal protocol path and
are forwarded to eligible downstream clients.

### `metadata-root-compat true|false`

Provides a temporary compatibility path for clients that send
`METADATA * SET` or `METADATA * CLEAR` on the bouncer root session instead of
the selected network connection. The default is `false`.

When enabled, soju forwards these commands with the unchanged `*` target once
to each connected, metadata-capable upstream network. Different upstream
nicknames are supported. A failure on one network does not block forwarding
to the other networks.

`GET`, `LIST`, `SUB`, and `UNSUB` are not broadcast globally.

The following local keys remain in the existing Metadata-2 compatibility path:

- `soju.im/pinned`
- `soju.im/muted`
- `soju.im/blocked`

These keys always remain local. Metadata-3-only clients do not receive
Metadata-2 numerics or Metadata-2 notifications for them.

## Known Mango limitation

A real wire-level test used two Mango clients behind the same soju network
connection and an independent client connected directly to the IRCd. It
confirmed that:

- Soju forwards each React or legacy toggle upstream unchanged.
- The authoritative IRCd echo reaches soju.
- Soju sends the same echo to every downstream connection while preserving
  the prefix, reaction key, reference, `msgid`, and `time`.
- The independent client reconstructs the correct global server state.

Despite this, two Mango clients can temporarily display different reaction
states. Both devices share one upstream IRC identity. Mango can therefore
mistake an event initiated by the other device for its own optimistic echo and
discard it.

The required multi-client behavior is:

1. Client A sets a reaction.
2. Client B recognizes it as a reaction from their shared IRC identity.
3. Client B can send the same legacy React to remove the reaction originally
   set by A.
4. Every soju client reconstructs the same state from the same authoritative
   events.

Mango should suppress a self-prefix echo only when it matches a concrete local
pending operation. An authoritative event with the shared upstream prefix but
without a matching local pending operation must be applied as a state change
from another device. History and reconnect deduplication should use the
reaction event's own `msgid`.

Rewriting the IRC prefix in soju is not a valid workaround. It makes the event
look like a reaction from a different user and creates a second logical
sender. Client B would then be unable to toggle the reaction set by A as the
shared identity.

This behavior has only been reproduced with Mango so far. Testing with other
IRC clients is still pending. Mango also sends user metadata on the bouncer
root session, which currently requires `metadata-root-compat true`.
