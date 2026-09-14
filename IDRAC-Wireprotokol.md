# Unofficial Dell iDRAC Virtual Console and Virtual Media Wire Protocol

## Independent Interoperability Specification, Draft 00

| Field | Value |
|---|---|
| Document status | Informational, reverse-engineered interoperability draft |
| Version | 00 |
| Date | 2026-09-14 |
| Intended audience | Authors of independent open-source iDRAC console clients |
| Scope | Vendor detection, HTTPS session bootstrap, console ticketing, the WebSocket console channel, Dell's control messages inside RFB, and the virtual media channel |

> [!IMPORTANT]
> This is not a Dell or IETF document. It is an independent description of observed wire behavior, published to enable compatible open-source implementations. The authors are not affiliated with, sponsored by, endorsed by, authorized by, or otherwise connected to Dell Technologies. Dell, PowerEdge and iDRAC are trademarks of Dell Inc.; all rights in those marks remain with their owner.

## Abstract

Dell Integrated Dell Remote Access Controller (iDRAC) firmware serves a browser-based virtual console for remote keyboard, video, mouse, power and virtual-media operations. Unlike the HPE console described in the companion document, the transport is not a Dell invention: the firmware speaks the Remote Framebuffer protocol of [RFC 6143](https://www.rfc-editor.org/rfc/rfc6143.html) over a WebSocket, and the client it ships is a build of noVNC.

What is Dell-specific is everything around that: how a client authenticates, how it obtains the one-shot ticket that admits it to the console, a second key exchange that gates the RFB stream, a family of vendor control messages multiplexed into the RFB byte stream, and a virtual-media channel of Dell's own design.

This document specifies those parts, together with the deviations from RFC 6143 that an implementation has to accommodate. Every byte layout here was reproduced against a PowerEdge R750 running iDRAC9 firmware 7.10.30.00.

## 1. Status, scope, and requirements language

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and **OPTIONAL** in this document are to be interpreted as described in BCP 14, [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119.html) and [RFC 8174](https://www.rfc-editor.org/rfc/rfc8174.html), when and only when they appear in all capitals.

The normative language applies to an independent client claiming compatibility with the protocol described here. It does not impose requirements on Dell products.

### 1.1 Evidence labels

| Label | Meaning |
|---|---|
| **Verified** | Reproduced by this project's implementation against live hardware, and covered by its tests. |
| **Observed** | Read from the console application the firmware serves, and consistent with captured traffic, but not exercised on every path. |
| **Inferred** | Best explanation consistent with observed behavior; implementations should tolerate alternatives. |
| **Unknown** | Field or behavior is visible but its semantics have not been established. |

Unless a subsection says otherwise, layouts in Sections 4 to 9 are **Verified** and the message registries in Sections 7 and 9 are **Observed**.

### 1.2 Analyzed generation and limitations

- All measurements come from one controller: iDRAC9, model string `15G Monolithic`, firmware `7.10.30.00`, in a PowerEdge R750 with an Enterprise licence.
- Virtual Console and Virtual Media require an Enterprise or Datacenter licence. Without one the endpoints in Section 5 refuse.
- Older iDRAC7 and iDRAC8 generations ship a different console client. No measurement in this document comes from one, so whether their firmware speaks this protocol, a variant of it, or something unrelated is **Unknown**.
- The controller allows six concurrent console sessions and exactly one virtual-media session.
- Removable-disk media, vFlash partitions and the console chat feature are described only as far as their message numbers; their payloads were not reconstructed.

## 2. Conventions and binary notation

Octet offsets are zero-based. Multi-octet values are unsigned unless stated otherwise.

| Notation | Meaning |
|---|---|
| `u8` | One octet |
| `u16le`, `u32le` | 16- or 32-bit integer in little-endian order |
| `u16be`, `u32be` | 16- or 32-bit integer in big-endian order |
| `ASCII[n]` | Fixed-width ASCII octet field, NUL-padded |
| `bytes[n]` | Exactly `n` uninterpreted octets |
| `C2S` | Client to iDRAC |
| `S2C` | iDRAC to client |

Two byte orders meet in this protocol and confusing them is the most likely source of an implementation fault:

- RFB message fields are **big-endian**, as RFC 6143 requires.
- Every Dell-defined structure, in both the console and the media channel, is **little-endian**.

## 3. Protocol architecture

A complete console session uses three connections to the same TCP port, normally 443.

| Stage | Transport | Purpose |
|---|---|---|
| Session bootstrap | HTTPS | Log in, obtain the ticket pair, read and change management state |
| Console channel | WebSocket, subprotocol `binary` | Key exchange, then RFB 3.8 with Dell control messages interleaved |
| Media channel | WebSocket, subprotocol `binary` | Key exchange, then block service for an attached image |

All three run **outbound from the client**. The controller never opens a connection back, which is what allows a client to serve a local ISO image across a firewall or a NAT boundary.

The two WebSocket channels are independent: either can end without taking the other down.

## 4. Vendor detection

A client that supports more than one controller family needs to know which one answers before it asks for credentials.

**Verified.** An unauthenticated `GET /redfish/v1` returns the Redfish service root. A Dell controller answers with:

```json
{
  "Vendor": "Dell",
  "Product": "Integrated Dell Remote Access Controller",
  "Oem": { "Dell": { "ServiceTag": "…", "ManagerMACAddress": "…" } }
}
```

A client **SHOULD** treat `Vendor` equal to `Dell`, or the presence of an `Oem.Dell` object, as identifying an iDRAC.

**Verified.** `GET /xmldata?item=All` returns `404` on an iDRAC. HPE iLO answers that path with a RIMP document, so it serves as a second, independent discriminator for controllers whose service root omits the vendor.

Neither request carries credentials.

## 5. HTTPS session bootstrap

### 5.1 Login

```
POST /sysmgmt/2015/bmc/session
user: "<username>"
password: "<password>"
```

**Verified.** The credentials travel in two request headers, each enclosed in double quotes; the body is empty. A successful login answers `201 Created` with:

- a body of `{"authResult": 0}`,
- an `XSRF-TOKEN` response header holding a 32-character hexadecimal token,
- a `Set-Cookie` for `-http-session-`.

A client **MUST** send both the cookie and the `XSRF-TOKEN` header on every later request, and on both WebSocket upgrades. Redfish paths accept the same token in an `X-AUTH-TOKEN` header.

A non-zero `authResult` indicates a rejected login even when the status code is 2xx, so a client **MUST** check it.

### 5.2 Logout

```
DELETE /sysmgmt/2015/bmc/session
```

**Verified.** A client **MUST** log out when it is finished. The controller keeps a small pool of sessions, and a console session belongs to the web session that requested its ticket: an abandoned login keeps a console slot occupied.

### 5.3 Sessions

**Verified.** `GET /redfish/v1/SessionService/Sessions?$expand=*($levels=1)` lists open sessions with `Id`, `UserName`, `ClientOriginIPAddress` and `SessionType`. Console sessions do not appear as entries of their own; they are visible only through the `WebUI` session that owns them.

`DELETE /redfish/v1/SessionService/Sessions/<id>` ends a session. **Verified:** the web session disappears immediately. **Observed:** the console slot it held is released some time later, not synchronously, so a client **MUST NOT** treat this as an immediate hand-over of the console.

### 5.4 A note on the static web assets

**Verified.** Files below `/restgui/` are served only to requests that offer `Accept-Encoding: gzip`; without it the controller answers `404`. This matters for anyone reading the shipped console application, not for a protocol client, and it is recorded here because it is a confusing dead end otherwise.

## 6. Console ticket

```
GET /sysmgmt/2015/server/vconsole
```

**Verified.** The response is a JSON object with one member:

```json
{ "Location": "https://<ip>:443/restgui/vconsole/index.html?ip=<ip>&kvmport=443&title=…&VCSID=<key1>&VCSID2=<key2>" }
```

| Query parameter | Meaning |
|---|---|
| `ip` | Address to open the console channel on |
| `kvmport` | Port for both WebSocket channels, normally the web port |
| `title` | Window caption the web interface would use |
| `VCSID` | First key, 24 characters. Admits the client to a channel |
| `VCSID2` | Second key, 24 characters. Answers the challenge in Section 7.1 |

**Verified.** Each call returns a fresh pair, and a pair is redeemed once. A client **MUST** obtain a new ticket for every connection attempt. Both keys are printable digit strings and are used as characters, never parsed as numbers.

An empty `Location` indicates that Virtual Console is disabled or unlicensed.

A client **MAY** use one ticket for both the console and the media channel; the shipped client does exactly that.

## 7. Console channel

```
GET /vnc/vconsole?vck=<key1>
Upgrade: websocket
Sec-WebSocket-Protocol: binary
Cookie: -http-session-=…
```

**Verified.** The upgrade uses the ordinary RFC 6455 handshake. The server selects the `binary` subprotocol. Every message is a binary frame.

A missing or unparseable `vck` parameter makes the upgrade fail with `502`, because the reverse proxy in front of the console service cannot reach its backend. A syntactically valid but unknown key completes the upgrade and the connection is then closed within a few tens of milliseconds.

### 7.1 Key challenge

**Verified.** The firmware sends a two-octet message before anything else:

```
f7 b3
```

That is `247` (the vendor message type of Section 8) with subtype `179`, `KEY2_REQUEST_FROM_SERVER`. **The firmware sends no RFB data until this is answered.** A client that waits for the RFB version string deadlocks until the server gives up, which it does after about seven seconds.

The answer is one message of 34 octets:

| Offset | Type | Value |
|---|---|---|
| 0 | `u8` | `247` |
| 1 | `u8` | `179` |
| 2 | `ASCII[24]` | `VCSID2` from the ticket |
| 26 | `bytes[8]` | Zero |

**Observed.** The challenge may be sent unprompted. A client that does so immediately after the upgrade is accepted as well, which is a useful fallback for a firmware revision that does not open the conversation.

**Verified.** The firmware later re-issues the same challenge during a session, carrying a refreshed key in the message of Section 8.2. A client **MUST** answer with the most recent key it has been given.

### 7.2 RFB

**Verified.** After the challenge the firmware speaks RFB 3.8 exactly as RFC 6143 describes:

1. Server sends `RFB 003.008\n`; the client answers with its own version.
2. Server offers security types; the only one offered is `1`, None. The ticket is the authentication.
3. `SecurityResult` is zero.
4. The client sends `ClientInit`; `ServerInit` follows.

**Verified.** `ServerInit` reports `1024x768` with a desktop name of `OpenVNC` on the analyzed controller.

A client **MUST** be prepared for a Dell control message to arrive between any two steps of this exchange. On the analyzed firmware, `CLIENT_LIST_AUTH_KEY` arrives between the version exchange and the security-type list.

### 7.3 Pixel format

This is the one place where the firmware's own announcement is wrong, and an implementation that trusts it produces a picture with wildly wrong colours.

**Verified.** `ServerInit` announces:

```
bits-per-pixel 16, depth 16, big-endian 0, true-colour 1
red-max 31, green-max 31, blue-max 31
red-shift 11, green-shift 6, blue-shift 0
```

That cannot describe a real layout. A five-bit green channel starting at bit 6 ends at bit 10, which leaves bit 5 unused and overlaps nothing. Measured against the wire, green is **six** bits wide and starts at bit 5. The real format is ordinary RGB 5-6-5:

| Channel | Bits | Max | Shift |
|---|---|---|---|
| Red | 15–11 | 31 | 11 |
| Green | 10–5 | 63 | 5 |
| Blue | 4–0 | 31 | 0 |

**Verified.** A client **SHOULD** send `SetPixelFormat` requesting exactly that. The firmware then delivers every encoding correctly.

**Verified.** A client that requests something else gets an inconsistent result: the Raw encoder converts to the requested format, while the Hextile encoder emits the native 5-6-5 regardless. A client that requests the 5-5-5 layout noVNC uses for depth 16 therefore renders Raw rectangles correctly and Hextile rectangles with corrupted colour, which is a confusing fault to chase because the geometry stays perfect.

### 7.4 Encodings

**Verified.** The firmware honours `SetEncodings`. With the list the shipped client offers, the first full update arrives as a single Hextile rectangle covering the whole screen.

A client **SHOULD** offer at least:

| Encoding | Number |
|---|---|
| CopyRect | 1 |
| Hextile | 5 |
| Raw | 0 |
| DesktopSize | -223 |
| LastRect | -224 |

**Observed.** RRE (2) is accepted. Tight is not offered by the shipped client at 16-bit colour and was not tested.

**Verified.** The firmware sends no further updates once the client stops asking. A client **MUST** keep a `FramebufferUpdateRequest` outstanding, normally by sending an incremental request for the full screen after each completed update.

## 8. Dell control messages

**Verified.** Dell multiplexes its own traffic into the RFB stream under message type `247`, a number RFC 6143 leaves to vendors. Each control message arrives in a WebSocket frame of its own, never mixed with RFB data.

Server-to-client messages carry the subtype in the second octet:

| Subtype | Name | Payload |
|---|---|---|
| 161 | `HOST_POWER_STATE` | `u8`, non-zero when the host is on |
| 162 | `NEW_CLIENT_JOINED` | Unknown |
| 163 | `CHAT_MESSAGE` | Length-prefixed UTF-16 text |
| 164 | `SESSION_IS_SHARED` | 260 octets; announces that another viewer is present |
| 165 | `SHARING_REQUEST_RECEIVED` | `u32le` client id, `ASCII[256]` name |
| 166 | `SHARING_REQUEST_APPROVED` | Unknown |
| 167 | `FIRST_BOOT_DEVICE` | `u8` device index |
| 168 | `SYSTEM_LOCKDOWN_STATE` | Unknown |
| 169 | `GRACEFULL_SHUTDOWN` | Unknown |
| 170 | `STATISTICS_DATA` | Unknown |
| 171 | `FPS_DATA` | `u8` frames per second |
| 172 | `CLIENT_LIST_AUTH_KEY` | See Section 8.2 |
| 173 | `CLIENT_EXIT` | Unknown |
| 174 | `FRAME_BUFFER_UPDATE` | Unknown |
| 175 | `VFLASH_PARTITION_LIST` | Unknown |
| 177 | `VFLASH_BOOT_SELECTION` | Unknown |
| 178 | `KEYBOARD_LED_STATUS` | `u8` LED mask |
| 179 | `KEY2_REQUEST_FROM_SERVER` | None; see Section 7.1 |

### 8.1 Client-to-server messages

Except for the key answer of Section 7.1, client messages carry a zero octet between the type and the subcommand:

| Layout | Subcommand | Meaning |
|---|---|---|
| `247 00 F0 <u8>` | First boot device | Select the one-time boot device |
| `247 00 F1 <u8>` | Power | See the operation table below |
| `247 00 F3 <u16le len>` | Chat | Followed by the text |
| `247 00 F5 <u8> <u32le>` | Sharing answer | 1 grants, 0 denies; then the client id |
| `247 00 F6` | Heartbeat | Keep-alive |
| `247 00 F8` | USB reset | Re-enumerate the virtual keyboard and mouse |
| `247 00 F9` | Window close | The viewer is leaving |
| `247 00 FA` | Pixel format change | Announces a following `SetPixelFormat` |
| `247 00 FB <u8 len>` | vFlash boot | Followed by the partition name |
| `247 00 FC` | Focus out | The viewer lost keyboard focus |

Power operations:

| Value | Operation |
|---|---|
| 0 | Graceful shutdown |
| 1 | Power off |
| 2 | Warm boot |
| 3 | Cold boot |
| 4 | Power on |

**Verified.** The heartbeat is sent every 20 seconds. A client **SHOULD** send it: the console timeout is disabled in the factory configuration, so a session whose keep-alive stops is held until something else clears it.

**Verified.** A client **MUST** send the window-close message before it closes the socket. Without it the controller keeps the console slot registered, and with only six slots and no timeout, a handful of clients that exited badly will block the console entirely.

### 8.2 Client list and key refresh

Subtype `172` carries the session identity and a refreshed key. After the two header octets:

| Offset | Type | Field |
|---|---|---|
| 0 | `u32le` | Auth key |
| 4 | `ASCII[32]` | Refreshed `key2`, NUL-padded |
| 36 | `u32le` | This client's id |
| 40 | `u32le` | Privilege mask |
| 44 | `ASCII[256]` | This client's user name, NUL-padded |
| 300 | `u32le` | Number of further viewers |
| 304 | `ASCII[n]` | This client's own address |
| 304+n | record × count | One record per further viewer |

Each viewer record is 324 octets: `u32le` client id, `ASCII[256]` name, `ASCII[64]` address.

**Verified.** The address field at offset 304 is 63 octets on firmware 7.10.30.00, while the same field inside a viewer record is 64. A client **SHOULD** derive its length from the message size rather than assuming either number.

Privilege bits, **Observed**:

| Bit | Privilege |
|---|---|
| 0 | Login |
| 1 | Configure |
| 2 | Configure users |
| 3 | Clear logs |
| 4 | Execute commands |
| 5 | Virtual console |
| 6 | Virtual media |

### 8.3 Session sharing

**Verified.** When another viewer already holds the console, the joining session receives `SESSION_IS_SHARED` and the firmware withholds video until the current viewer allows access. The leader receives `SHARING_REQUEST_RECEIVED` and answers with the sharing message of Section 8.1.

**Verified.** The default action on timeout comes from the iDRAC attribute `VirtualConsole.1.AccessPrivilege`, which ships as `Deny Access`. In that configuration a joining client that nobody answers for never receives a frame.

A client **SHOULD** therefore treat a console that produces no framebuffer update within about ten seconds of a successful handshake as held by another session, and say so, rather than presenting an empty window.

## 9. Virtual media channel

```
GET /vnc/vmedia?vmk=<key1>
Upgrade: websocket
Sec-WebSocket-Protocol: binary
```

The client holds the image and answers block reads. Nothing is uploaded ahead of time and the controller never connects back.

### 9.1 Common header

**Verified.** Every message in both directions begins with five little-endian 32-bit words:

| Offset | Type | Field |
|---|---|---|
| 0 | `u32le` | Message type |
| 4 | `u32le` | Image type: 1 for CD/DVD, 2 for removable disk |
| 8 | `u32le` | Payload length |
| 12 | `u32le` | Session token |
| 16 | `u32le` | Status code |

The token is issued by the firmware in the ready message and **MUST** be echoed in every later client message.

### 9.2 Message types

| Value | Direction | Meaning |
|---|---|---|
| `0x1` | Both | Block read request, and the answer to one |
| `0x8` | Both | Eject: from the host, or the client unmapping |
| `0x10` | S2C | Channel ready; carries the token and a refreshed key |
| `0x20` | C2S | Inquiry: announces the medium and its geometry |
| `0x40` | S2C | A read the controller no longer needs |
| `0x100` | S2C | Status message |
| `0x200` | C2S | Reset the virtual device |
| `0x400` | C2S | Orderly client shutdown |
| `0x800` | C2S | Heartbeat, every 20 seconds |
| `0x2000` | Both | Key challenge and answer |
| `0x4000` | S2C | Challenge for the retired Java client |

### 9.3 Key exchange

**Verified.** The firmware opens with type `0x2000` and a zero token. The answer is twelve words, 48 octets:

| Offset | Type | Value |
|---|---|---|
| 0 | `u32le` | `0x2000` |
| 4 | `u32le` | 0 |
| 8 | `u32le` | `0x20` |
| 12 | `u32le` | Token from the challenge |
| 16 | `u32le` × 6 | `VCSID2`, four characters per word, little-endian |
| 40 | `u32le` × 2 | Zero |

The firmware answers with type `0x10`, which carries the session token in word 3 and a refreshed key from word 4 onwards.

### 9.4 Attaching an image

**Verified.** Once the channel is ready the client announces the medium with an inquiry of eleven words, 44 octets:

| Offset | Type | Field |
|---|---|---|
| 0 | `u32le` | `0x20` |
| 4 | `u32le` | 1, CD/DVD |
| 8 | `u32le` | 28 |
| 12 | `u32le` | Token |
| 16 | `u32le` | Total blocks, image size divided by 2048, rounded up |
| 20 | `u32le` | Block size, 2048 |
| 24 | `u32le` | Cylinders, zero for optical media |
| 28 | `u32le` | Heads, zero |
| 32 | `u32le` | Sectors, zero |
| 36 | `u32le` | Flags; 1 marks the medium read-only |
| 40 | `u32le` | Table-of-contents length, 20 |

**Observed.** The shipped client declares a 20-octet table of contents and never sends it, and the firmware does not ask. An independent client **SHOULD** do the same: declaring the length and sending nothing is the behavior that works.

Immediately after the inquiry the client pushes the first 32 blocks unprompted, using the read answer of Section 9.5. A shorter image sends as many blocks as it has.

### 9.5 Block service

A read request from the firmware carries three further words:

| Offset | Type | Field |
|---|---|---|
| 16 | `u32le` | First block |
| 20 | `u32le` | Block count |
| 24 | `u32le` | Block size |

The answer is two WebSocket messages: a header of nine words, 36 octets, followed by the raw payload.

| Offset | Type | Field |
|---|---|---|
| 0 | `u32le` | `0x1` |
| 4 | `u32le` | Image type |
| 8 | `u32le` | 28 |
| 12 | `u32le` | Token |
| 16 | `u32le` | First block |
| 20 | `u32le` | Block count |
| 24 | `u32le` | First block plus count |
| 28 | `u32le` | Block size |
| 32 | `u32le` | Payload length in octets |

**Observed.** The payload-length field of the header says 28 although the header is 36 octets long. This is what the shipped client sends and what the firmware expects.

**Verified.** The firmware reads past the end of the medium while it works out the geometry. A client **SHOULD** trim such a request to the blocks that exist rather than refusing it.

### 9.6 Status codes

Status messages of type `0x100` carry the code in word 4. The values an implementation is most likely to meet:

| Code | Name | Meaning |
|---|---|---|
| 0 | `MSD_SUCCESS` | Success |
| 4 | `UNSUPPORTED_IMAGE_TYPE` | Image type refused |
| 23 | `VMEDIA_DISABLED` | Virtual media switched off |
| 25 | `VMEDIA_EJECT_REQUEST_SUCCESS` | Eject completed |
| 38 | `VMEDIA_MAPPED` | Medium mapped |
| 39 | `LICENSE_ERR` | Licence does not cover virtual media |
| 40 | `VMEDIA_MODE_DETACHED` | Virtual media is detached |
| 42 | `VMEDIA_MAX_SESSION_CNT` | Session limit reached; the limit is one |
| 70 | `VEMDIA_PARTITION_MOUNTED` | Medium attached |
| 84 | `LICENSE_DISABLED` | Virtual media disabled by licence |
| 89 | `LOGIN_DENIED` | Login refused |
| 98 | `VMEDIA_USER_NOT_PRIVILIGED` | Account lacks the virtual-media privilege |

**Verified.** Firmware 7.10.30.00 confirms a successful attach with code 70, not with 38. A client that waits only for 38 never sees its image acknowledged. The misspelled constant names in this table are Dell's own.

### 9.7 Detaching

**Verified.** The client sends an eject message, type `0x8`, with the image type, and then the orderly shutdown, type `0x400`, before closing the socket. A host-initiated eject arrives as type `0x8` and **MUST** be answered with the same type.

## 10. Management over Redfish

The console channel carries power state and boot device, but the full management surface is ordinary Redfish and needs no reverse engineering.

| Purpose | Endpoint |
|---|---|
| Power state, boot override | `GET /redfish/v1/Systems/System.Embedded.1` |
| Set one-time boot | `PATCH` the same resource with `Boot.BootSourceOverrideTarget` and `BootSourceOverrideEnabled: "Once"` |
| Power actions | `POST …/Actions/ComputerSystem.Reset` |
| Controller and console settings | `GET /redfish/v1/Managers/iDRAC.Embedded.1` and its `Oem/Dell/DellAttributes/iDRAC.Embedded.1` |
| Virtual drive state | `GET /redfish/v1/Managers/iDRAC.Embedded.1/VirtualMedia/CD` |
| Screen preview | `GET /capconsole/scapture0.png` |

**Verified.** The virtual drive resource reports `Inserted: true` and `ConnectedVia: "Applet"` while an image attached over the channel of Section 9 is in place, which makes it a useful independent check.

**Verified.** The attributes most relevant to a console client:

| Attribute | Meaning |
|---|---|
| `VirtualConsole.1.Enable` | Whether the console is available at all |
| `VirtualConsole.1.ActiveSessions` | Slots in use |
| `VirtualConsole.1.MaxSessions` | Slots available, six on the analyzed firmware |
| `VirtualConsole.1.AccessPrivilege` | Default action on a sharing-request timeout |
| `VirtualConsole.1.TimeoutEnable` | Disabled in the factory configuration |
| `VirtualMedia.1.Enable` | Whether virtual media is available |
| `VirtualMedia.1.MaxSessions` | One on the analyzed firmware |

## 11. Implementation notes

The faults below cost this project the most time and are recorded so the next implementation does not repeat them.

1. **The server speaks second, not first.** The RFB specification has the server open with its version string. Here the firmware opens with the key challenge and stays silent until it is answered. A client that waits for twelve octets of version receives two octets and blocks.
2. **Control messages arrive during the handshake.** The client list lands between the version exchange and the security-type list. A demultiplexer that only becomes active once RFB is running reads it as protocol data.
3. **The announced pixel format is wrong.** See Section 7.3. Requesting 5-6-5 explicitly avoids the problem entirely.
4. **Two opcodes are easy to transpose.** `0xF0` selects the boot device and `0xF1` performs a power operation, not the other way round.
5. **Sessions do not time out.** Without the heartbeat and the window-close message, abandoned sessions accumulate until all six slots are gone and every new connection joins as a shared session that never receives video.

## 12. Trademark and non-affiliation notice

Dell, PowerEdge, iDRAC and OpenManage are trademarks of Dell Inc. or its subsidiaries. This document and the implementation it describes are independent work, not affiliated with, sponsored by, endorsed by, authorized by, or otherwise connected to Dell Technologies. The trademarks are used only to identify the systems the described protocol interoperates with.
