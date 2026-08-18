# Unofficial HPE iLO Integrated Remote Console Wire Protocol

## Independent Interoperability Specification — Draft 00

| Field | Value |
|---|---|
| Document status | Informational, reverse-engineered interoperability draft |
| Version | 00 |
| Date | 2026-07-18 |
| Intended audience | Authors of independent open-source iLO remote-console clients |
| Scope | HTTPS bootstrap, IRC protocol v2 transport, KVM, command channel, DVC video, virtual media, and partial legacy v1 negotiation |

> [!IMPORTANT]
> This is not an HPE or IETF document. It is an independent description of observed wire behavior, published to enable compatible open-source implementations. The authors are not affiliated with, sponsored by, endorsed by, authorized by, or otherwise connected to Hewlett Packard Enterprise. HPE and iLO are trademarks of Hewlett Packard Enterprise; all rights in those marks remain with their owner.

## Abstract

HPE Integrated Lights-Out (iLO) exposes an Integrated Remote Console (IRC) service for remote keyboard, video, mouse, power, status, clipboard, and virtual-media operations. Public HPE documentation identifies the configurable TCP services but does not define their application-layer wire formats.

This document specifies an independently reconstructed form of that protocol. It covers the HTTPS login and endpoint-discovery exchange, the version 2 key-derivation and encrypted channel handshake, client-to-server KVM records, the server-to-client command channel, the DVC video bitstream, and the virtual CD/DVD SCSI tunnel. A partial description of the older version 1 negotiation is included for implementers who need to investigate older iLO generations.

The specification deliberately distinguishes verified wire layouts from behavior inferred from interoperability work. Unknown fields are preserved and identified rather than assigned invented meanings.

## 1. Status, scope, and requirements language

This document is intended to be sufficiently precise for an independent implementation. It is not a claim that every iLO generation or firmware revision behaves identically.

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and **OPTIONAL** in this document are to be interpreted as described in BCP 14, [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119.html) and [RFC 8174](https://www.rfc-editor.org/rfc/rfc8174.html), when and only when they appear in all capitals.

The normative language applies to an independent client claiming compatibility with the protocol described here. It does not impose requirements on HPE products.

### 1.1 Evidence labels

Each material claim belongs to one of these classes:

| Label | Meaning |
|---|---|
| **Verified** | Reproduced by this project's implementation and regression tests, or directly determined from an unambiguous packed wire structure. |
| **Observed** | Present in behavioral analysis of a legacy client or captured control flow, but not necessarily exercised against every firmware generation. |
| **Inferred** | Best explanation consistent with observed behavior; implementations should tolerate alternatives. |
| **Unknown** | Field or behavior is visible but its semantics have not been established. |

Unless a subsection says otherwise, protocol-v2 byte layouts are **Verified**, legacy-v1 details are **Observed**, and compatibility recommendations are **Inferred**.

### 1.2 Supported generations and limitations

- Protocol version 2 uses the encrypted, multi-channel hello described in Sections 6–9. The analyzed clients route values greater than 1 through the same path, but compatibility with a future protocol version is not established.
- The version number is an IRC protocol number, not the marketing generation number of the iLO device.
- Protocol version 1 uses a different negotiation described only partially in Section 14.
- Firmware, license level, security mode, and configured ports can change availability.
- The video codec described in Section 11 is sometimes called DVC in client implementations. That name is used here only as a convenient protocol label.
- Virtual floppy, USB-key, virtual-folder, playback, and peer-to-peer shared-console behavior are less completely validated than KVM and virtual CD/DVD.

## 2. Conventions and binary notation

Octet offsets are zero-based. Multi-octet values are unsigned unless stated otherwise.

| Notation | Meaning |
|---|---|
| `u8` | One octet |
| `u16le`, `u32le` | 16- or 32-bit integer in little-endian order |
| `u16be`, `u32be` | 16- or 32-bit integer in big-endian order |
| `ASCII[n]` | Fixed-width ASCII octet field, normally NUL-padded |
| `bytes[n]` | Exactly `n` uninterpreted octets |
| `C2S` | Client to iLO |
| `S2C` | iLO to client |

All packed structures in this document have one-octet alignment and contain no implicit padding.

TCP is a byte stream. A client MUST use exact-length reads for fixed structures and MUST NOT assume that one socket read returns one complete protocol object.

## 3. Protocol architecture

An IRC session normally uses one HTTPS connection context and several independent TCP connections:

```text
                HTTPS, normally TCP 443
Client  -------------------------------------->  iLO
        login_session; RcInfo discovery

                RcPort, normally TCP 17990
Client  ======================================>  iLO
        channel 0: KVM video and input

                RcPort, normally TCP 17990
Client  ======================================>  iLO
        channel 1: commands, status, clipboard

                VmPort, normally TCP 17988
Client  ======================================>  iLO
        channels 2..5: virtual media
```

HPE documents TCP 17990 as the default remote-console port and TCP 17988 as the default virtual-media port. Both are configurable, so an implementation MUST use the values returned by endpoint discovery rather than hard-code the defaults.

Each protocol-v2 TCP connection has its own hello, IVs, encryption state, and channel number. Encryption state MUST NOT be shared between sockets.

## 4. HTTPS authentication

### 4.1 Login

The client sends:

```http
POST /json/login_session HTTP/1.1
Accept: application/json
Content-Type: application/json
```

```json
{
  "method": "login",
  "user_login": "<username>",
  "password": "<password>"
}
```

A successful JSON response contains the IRC session token in either `key` or `session_key`. The response can also establish cookies. A client SHOULD retain both the cookie jar and the returned token for the lifetime of the session.

For authenticated HTTP requests, compatible clients send:

```http
X-Auth-Token: <session-token>
```

The same session token is placed in the binary channel hello. It is therefore credential material and MUST NOT be written to normal logs or diagnostics.

### 4.2 Logout

The client sends the following body to the same endpoint:

```json
{
  "method": "logout",
  "session_key": "<session-token>"
}
```

An implementation SHOULD attempt logout when the final channel closes, but it SHOULD also tolerate an unreachable iLO or an already-expired session.

### 4.3 TLS

The HTTPS phase authenticates the management controller and protects the credentials and bootstrap keys in transit. Clients SHOULD validate the iLO certificate. If a user explicitly enables an insecure/self-signed compatibility mode, the client MUST make the loss of server authentication clear. See Section 16.

## 5. Remote-console endpoint discovery

### 5.1 Redfish-style endpoint

The preferred request is:

```http
GET /redfish/v1/Managers/1/RcInfo/ HTTP/1.1
Accept: application/json
X-Auth-Token: <session-token>
```

Relevant response fields are:

```json
{
  "Enabled": true,
  "MasterKey": "<hexadecimal bytes>",
  "ProtocolVersion": 2,
  "RcPort": 17990,
  "VmPort": 17988
}
```

| Field | Meaning |
|---|---|
| `Enabled` | Whether remote-console access is enabled. |
| `MasterKey` | Hex-encoded master secret for the protocol-v2 KDF. |
| `ProtocolVersion` | IRC wire-protocol version. |
| `RcPort` | TCP port for KVM and command channels. |
| `VmPort` | TCP port for virtual-media channels; zero can mean unavailable. |

`MasterKey` MUST be hex-decoded before use. Clients MUST reject malformed hexadecimal data and invalid port values.

### 5.2 Legacy JSON fallback

If and only if the preferred endpoint returns HTTP 404, a compatible client can request:

```http
GET /json/rc_info HTTP/1.1
Accept: application/json
X-Auth-Token: <session-token>
```

Fields observed in this response include:

| Field | Encoding and purpose |
|---|---|
| `https_port` | HTTPS port, often represented as a JSON string. |
| `enc_key` | Hex-encoded encryption/master key. |
| `enc_type` | Legacy encryption selection. Exact use varies. |
| `rc_port` | Remote-console TCP port. |
| `vm_key` | Legacy virtual-media key string. |
| `vm_port` | Virtual-media TCP port. |
| `cmd_enc_key` | Legacy command-channel AES key in hexadecimal; not present on all systems. |
| `protocol_version` | Number; observed values can contain a decimal fraction. |
| `optional_features` | Semicolon-separated capability tokens. |

Observed optional feature tokens include:

| Token | Observed meaning |
|---|---|
| `OFF_VIRTUAL_MEDIA` | Hide or disable virtual-media functions. |
| `OFF_ILO_PLAYBACK` | Disable console playback. |
| `ENCRYPT_KEY` | XOR-obfuscate the legacy login key and set a command bit. |
| `ENCRYPT_VMKEY` | Select the alternate legacy encrypted-key command bit. |
| `ENCRYPT_CMD` | Enable legacy command-stream AES and its start marker. |
| `REMOTE_CLIPBOARD_SUPPORT` | Remote clipboard extension is available. |

Some legacy clients truncate a fractional `protocol_version` to its integer part. New implementations SHOULD preserve the original value for diagnostics and select behavior conservatively.

### 5.3 Discovery error policy

A client SHOULD fall back to `/json/rc_info` only for a 404 response. Authentication failures, TLS failures, server errors, and malformed JSON SHOULD be surfaced instead of being silently converted into a fallback request.

## 6. Protocol-v2 key derivation

### 6.1 KDF definition

The protocol uses a stateful HMAC-SHA-512 derivation stream.

Constants:

```text
PRF       = HMAC-SHA-512
Label     = ASCII("iLO IRC")
Context   = ASCII("key derivation")
Separator = 0x00
```

The master HMAC key is the decoded `MasterKey` or `enc_key`. Let `LE32(x)` encode `x` as a four-octet little-endian integer.

```text
previous = empty
i        = 1
count    = 512
output   = empty

repeat until enough output exists:
    message  = previous || LE32(i) || Label || 0x00 || Context || LE32(count)
    previous = HMAC-SHA-512(master_key, message)
    output   = output || previous
    i        = i + 1
    count    = count + 512
```

The inclusion of the previous 64-octet digest starts with the second iteration. `count` is little-endian and changes on every generated digest. This is protocol-specific and MUST NOT be replaced by a superficially similar standard KDF.

### 6.2 Directional key pairs

Each channel connection consumes two 16-octet AES keys:

```text
pair[n].C2S = output[n*32     .. n*32+15]
pair[n].S2C = output[n*32+16  .. n*32+31]
```

The labels “inbound” and “outbound” in older client code are ambiguous. Implementations SHOULD use the explicit `C2S` and `S2C` names.

The commonly observed connection order is:

| Pair | Common consumer |
|---:|---|
| 0 | KVM channel |
| 1 | Command channel |
| 2 | First virtual-media channel |
| 3+ | Additional virtual-media channels in creation order |

The original behavior is a process-wide, sequential KDF stream. The pair is therefore tied more strongly to connection creation order than to a permanent channel-number registry. A client opening channels in a different order SHOULD be prepared for firmware differences. A practical first virtual-CD strategy is pair 2, with carefully bounded compatibility retries using pairs 1 and 0 only when the server rejects the hello.

### 6.3 Synthetic test vector

For the 16-octet ASCII master key `0123456789abcdef`, the first three pairs are:

```text
pair 0 C2S = ceb3bd2cfc102433aeedbfb9d0f7c2c9
pair 0 S2C = 3a6e6e9a395bebd6ab035ee49102caff
pair 1 C2S = 45f0279bab684eeafcfb70868483a2b5
pair 1 S2C = fec2916c76cc287a18bed3291c817920
pair 2 C2S = e77679d193415b1ed8b869b40737ebe1
pair 2 S2C = b39694be2196a77d1581accc9eebc59f
```

This vector contains no device secret and MAY be used in conformance tests.

## 7. Protocol-v2 stream cipher

Each direction uses AES as a continuous output-feedback-like stream transform. Valid AES key sizes are 16, 24, or 32 octets; the observed KDF supplies 16-octet AES-128 keys.

For one direction:

```text
feedback[0] = IV
keystream[n] = AES-ENC(key, feedback[n])
feedback[n+1] = keystream[n]
ciphertext = plaintext XOR concatenated_keystream
```

This is equivalent to full-block AES-OFB. The historical implementation obtains the same sequence by encrypting zero blocks with a stateful AES-CBC encryptor. A compatible implementation MAY use a conventional OFB primitive if its byte output matches the definition above.

Requirements:

- C2S and S2C MUST use independent keys, IVs, and stream positions.
- The transform is symmetric: the same XOR operation encrypts and decrypts.
- Stream state MUST continue across the hello body and all later application bytes.
- Stream state MUST continue across socket write and read boundaries.
- Implementations MUST serialize writes per connection; concurrent transforms on one stream will corrupt the keystream position.
- There is no observed per-record IV, padding, authentication tag, or post-handshake MAC.

### 7.1 Synthetic ClientHello cipher vector

Using the Section 6.3 pair-0 C2S key, IV `000102030405060708090a0b0c0d0e0f`, command New (`0`), channel KVM (`0`), zero flags, and session token `abc`, the complete 54-octet ClientHello on the wire is:

```text
000102030405060708090a0b0c0d0e0f
a4507e932744462d0db17b24d6eb7b76f
8c100fcde9fd3e31f9056419dabdbbcc1
ed9330455d
```

The first line is the plaintext IV. Concatenating all lines produces the wire object; the remaining 38 octets are encrypted. This vector also verifies that encryption starts at offset 16 and that zero padding in the session field participates in the stream transform.

## 8. Protocol-v2 channel handshake

### 8.1 Channel and command registries

| Channel | Name | TCP endpoint |
|---:|---|---|
| 0 | KVM | `RcPort` |
| 1 | Command | `RcPort` |
| 2 | Floppy | `VmPort` |
| 3 | Disc / CD/DVD | `VmPort` |
| 4 | USB key | `VmPort` |
| 5 | Virtual folder | `VmPort` |

| Command | Name | Purpose |
|---:|---|---|
| 0 | New | Create a normal connection. |
| 1 | Acquire | Take over a busy console after user approval. |
| 2 | Share | Request a shared session after user approval. |

### 8.2 ClientHello

The client creates a fresh random 16-octet C2S IV and sends exactly 54 octets:

| Offset | Size | Type | Field | Protection |
|---:|---:|---|---|---|
| 0 | 16 | `bytes[16]` | C2S IV | Plaintext |
| 16 | 1 | `u8` | Command | Encrypted |
| 17 | 1 | `u8` | Channel | Encrypted |
| 18 | 4 | `u32le` | Flags, normally zero | Encrypted |
| 22 | 32 | `ASCII[32]` | Session key | Encrypted |

The session-key field is fixed-width. A short value is NUL-padded. A client MUST NOT overflow the field; values longer than 32 octets SHOULD be rejected rather than silently made ambiguous.

The client initializes the C2S stream with the pair's C2S key and the generated IV, encrypts offsets 16–53, and sends the structure. The next application byte uses keystream position 38 because 38 encrypted hello octets have already been consumed.

### 8.3 ServerHello

The server returns exactly 53 octets:

| Offset | Size | Type | Field | Protection |
|---:|---:|---|---|---|
| 0 | 16 | `bytes[16]` | S2C IV | Plaintext |
| 16 | 1 | `u8` | Status | Encrypted |
| 17 | 4 | `u32le` | Flags | Encrypted |
| 21 | 32 | `ASCII[32]` | Session-key field | Encrypted |

The client initializes the S2C stream with the pair's S2C key and the received IV, then decrypts offsets 16–52. The next S2C application byte uses keystream position 37.

The exact semantics of the returned flags and session-key field are **Unknown**. Existing compatible clients do not rely on them. A defensive implementation SHOULD retain them for diagnostics without logging the key field.

### 8.4 Status registry

| Status | Meaning | Client action |
|---:|---|---|
| 0 | Success | Continue on the established encrypted stream. |
| 1 | Denied | Close and report denial. |
| 2 | Busy | Close; optionally ask the user whether to reconnect with Acquire or Share. |
| 3 | Not supported | Close and report unsupported operation/channel. |
| 4 | Bad request | Close and inspect version, key pair, hello layout, and channel. |
| 5 | Internal error | Close and report an iLO-side failure. |
| 6 | No sessions | Close and report that no session slot is free. |
| 7 | Not licensed | Close and report the license requirement. |

Acquire and Share retries use a new TCP connection and a new random C2S IV. They MUST NOT reuse the failed connection's cipher state.

## 9. KVM client-to-server records

After a successful channel-0 hello, all records are encrypted with the continuing C2S stream. The KVM channel is not framed by a general length prefix; record size follows from the first octet and operation.

### 9.1 Keyboard report

A keyboard report is exactly 10 octets:

| Offset | Size | Field |
|---:|---:|---|
| 0 | 1 | Record type `0x01` |
| 1 | 1 | Reserved, zero |
| 2 | 1 | USB HID modifier bitmap |
| 3 | 1 | Reserved, zero |
| 4 | 6 | Up to six USB HID Keyboard/Keypad usage IDs; unused slots are zero |

Modifier bits:

| Bit | Modifier |
|---:|---|
| 0 | Left Control |
| 1 | Left Shift |
| 2 | Left Alt |
| 3 | Left GUI/Windows |
| 4 | Right Control |
| 5 | Right Shift |
| 6 | Right Alt / AltGr |
| 7 | Right GUI/Windows |

Usage IDs are USB HID Keyboard/Keypad usages, not operating-system virtual-key codes and not Unicode characters. A client SHOULD send an all-keys-up report when input capture begins, ends, or focus is lost:

```text
01 00 00 00 00 00 00 00 00 00
```

The report carries at most six concurrent non-modifier keys. Implementations MUST define a deterministic overflow policy and MUST NOT write beyond the report.

### 9.2 Ctrl+Alt+Delete sequence

An observed compatible sequence is:

```text
01 00 05 00 4c 00 00 00 00 00   ; Left Ctrl + Left Alt + Delete
wait approximately 250 ms
01 00 05 00 00 00 00 00 00 00   ; release Delete, retain modifiers
wait approximately 5 ms
01 00 00 00 00 00 00 00 00 00   ; release all
```

`0x4c` is the USB HID Delete Forward usage. The delays are client behavior rather than fields on the wire.

### 9.3 Mouse report

A mouse report is exactly 10 octets:

| Offset | Size | Type | Field |
|---:|---:|---|---|
| 0 | 1 | `u8` | Record type `0x02` |
| 1 | 1 | `u8` | Reserved, zero |
| 2 | 2 | `u16le` | Absolute X, nominal range 0–3000 |
| 4 | 2 | `u16le` | Absolute Y, nominal range 0–3000 |
| 6 | 1 | custom | Relative X |
| 7 | 1 | custom | Relative Y |
| 8 | 1 | bitmap | Buttons |
| 9 | 1 | signed/low byte | Wheel delta |

Absolute coordinates are normally calculated as:

```text
wire_x = round(3000 * local_x / framebuffer_width)
wire_y = round(3000 * local_y / framebuffer_height)
```

Inputs SHOULD be clamped to the displayed framebuffer bounds before conversion.

Relative values are clamped to `[-127, 127]` and encoded as:

```text
v >= 0: octet = v
v <  0: octet = 0x80 + abs(v)
```

This is not two's complement. In the observed desktop client, relative Y is calculated as previous Y minus current Y, so its visual sign convention is inverted relative to X.

Button bits:

| Bit mask | Button |
|---:|---|
| `0x01` | Left |
| `0x02` | Right |
| `0x04` | Middle |
| `0x08` | XButton1 |
| `0x10` | XButton2 |

The wheel field carries the low octet of the platform wheel delta; treating it as signed 8-bit is interoperable with the current implementation.

### 9.4 Power record

Power operations are four octets:

```text
00 00 <option> 00
```

| Option | Meaning |
|---:|---|
| 0 | Momentary power-button press |
| 1 | Press and hold |
| 2 | Cold boot / power cycle |
| 3 | Reset |

These operations can disrupt a running server. A user-facing client SHOULD require deliberate confirmation for options 1–3.

### 9.5 Additional KVM control records

The following records are **Observed**:

| Bytes | Meaning |
|---|---|
| `05 00` | Request screen refresh |
| `07 00 <mode>` | Select capture/color mode |
| `08 00` | Select startup capture image |
| `09 00` | Select pre-failure capture image |
| `0b 00` | Shutdown/close indication |
| `0c 00` | Acknowledge firmware/DVC command |
| `0f 00` | Idle/keepalive variant |
| `0f` | One-octet keepalive variant, observed every 240 seconds |

Capture modes map as follows:

| Mode | Nominal total bits per pixel |
|---:|---:|
| 0 | 15 |
| 1 | 12 |
| 2 | 9 |
| 3 | 6 |
| 4 | 5 |
| 5 | 4 |
| 6 | 3 |
| 7 | 2 |

### 9.6 Shared-session peer records

These records implement legacy peer-to-peer shared-console operation and are **Observed**:

| First octet | Length | Meaning |
|---:|---:|---|
| `0x03` | 10 | Closing notification to peer-session leader |
| `0x04` | 10 | Opening notification to peer-session leader |
| `0x0d` | 10 | IPv4 peer closed; address begins at offset 2 |
| `0x0e` | 10 | IPv4 peer opened; address begins at offset 2 |
| `0x10` | 20 | IPv6 peer opened; bytes 2–3 are `18 00`, address at offset 4 |
| `0x11` | 20 | IPv6 peer closed; bytes 2–3 are `18 00`, address at offset 4 |

Unused bytes are zero-padded. A joining client sends record `0x04` after accepting the reverse connection and record `0x03` before closing it. The leader forwards peer records `0x01` (keyboard) and `0x02` (mouse) to iLO, and forwards the already-decoded DVC byte stream to every peer. The leader translates peer open/close records into the address-bearing IPv4/IPv6 records above before writing them to iLO.

## 10. Command channel

Channel 1 is a separate encrypted connection to `RcPort`, normally using KDF pair 1. Its cipher streams continue from its own hello.

### 10.1 Server-to-client packet

Each packet has a 12-octet header followed by `size` data octets:

| Offset | Size | Type | Field |
|---:|---:|---|---|
| 0 | 4 | `u32le` | Command ID |
| 4 | 4 | `u32le` | Payload size |
| 8 | 2 | `u16le` | Sequence number |
| 10 | 2 | `u16le` | Flags / operation-specific value |
| 12 | `size` | bytes | Payload |

The reference behavior rejects payloads larger than 1500 octets. Independent clients SHOULD impose a comparable upper bound before allocating memory.

Known S2C command IDs:

| ID | Meaning | Payload / flags |
|---:|---|---|
| 2 | No operation / ignored | Unknown |
| 3 | Server power state | `data[0]`: 0 off, 1 on |
| 4 | Health state | `data[0]`: 0 normal/green, 1 caution/yellow, 2 critical/red; other values inactive/off |
| 5 | POST code | Two octets, displayed as `data[1]` then `data[0]` in hexadecimal |
| 6 | Acquire/seize request | `flags`: timeout; 64-octet user plus 64-octet address, ASCII/NUL-padded |
| 7 | Scripted/URL virtual-media acknowledgement | First four bytes: signed result, drive group, device type, connected flag |
| 8 | Console playback buffer | Chunked capture data; `seq` participates in transfer ordering |
| 9 | Shared-session join request | Same 128-octet user/address layout as command 6; `flags`: timeout |
| 10 | Firmware update notification | Client closes the console |
| 11 | Permission revoked/denied | `flags`: 2 console, 3 virtual media, 4 power; other values unknown |
| 12 | Clipboard payload | Section 10.3 |
| 13 | Virtual-media connection update | `data[0]`: implementation-specific switch code |
| 14 | Remote console not licensed | Client stops video and closes |
| 15 | iLO reset notification | Client closes |
| 16 | Remote helper status | Eight-octet helper structure, Section 10.3 |
| 17 | SMIF/clipboard failure | `flags` identifies the failed operation; full registry incomplete |

Payload lengths MUST be validated before indexing operation-specific fields.

### 10.2 Client-to-server raw command words

Some C2S command-channel operations are not wrapped in the 12-octet S2C header. They begin with a four-octet little-endian command word:

| Bytes | Meaning | Following bytes |
|---|---|---|
| `01 00 00 00` | Connect URL as floppy/removable media | `ASCII[256]` URL, NUL-padded |
| `02 00 00 00` | Connect URL as CD/DVD | `ASCII[256]` URL, NUL-padded |
| `03 00 00 00` | Reject acquire/share request | None |
| `04 00 00 00` | Accept acquire/share request | None |
| `05 00 00 00` | Disconnect URL floppy/removable media | None |
| `06 00 00 00` | Disconnect URL CD/DVD | None |
| `07 00 00 00` | Send clipboard/SMIF object | One 1456-octet client object |

These command words are encrypted by the continuing C2S command stream.

### 10.3 Clipboard / SMIF extension

This optional extension is advertised by `REMOTE_CLIPBOARD_SUPPORT`. It transfers UTF-16LE text, file metadata/data, image data, and machine names over command 12 and raw C2S command 7.

S2C clipboard payload (`SMIF_0091`, 1448 octets):

| Offset | Size | Type | Field |
|---:|---:|---|---|
| 0 | 1 | `u8` | Clipboard type |
| 1 | 1439 | bytes | Data |
| 1440 | 4 | `u32le` | Total object size |
| 1444 | 4 | `u32le` | Bytes valid in this chunk |

C2S clipboard object (`SMIF_0092`, 1456 octets), sent after raw command 7:

| Offset | Size | Type | Field |
|---:|---:|---|---|
| 0 | 1 | `u8` | Clipboard type |
| 1 | 1447 | bytes | Data |
| 1448 | 4 | `u32le` | Total object size |
| 1452 | 4 | `u32le` | Bytes valid in this chunk |

Known clipboard types:

| Type | Direction/name |
|---:|---|
| 1 | Client-to-host text data |
| 2 | Host-to-client text data |
| 3 | Client-to-host file name |
| 4 | Host-to-client file keepalive/request |
| 5 | Host-to-client continue file transfer |
| 6 | Host-to-client cancel file transfer |
| 7 | Client-to-host file data |
| 8 | Host-to-client file start |
| 9 | Host-to-client keepalive |
| 10 | Host-to-client file name |
| 11 | Host-to-client continue |
| 12 | Host-to-client cancel |
| 13 | Host-to-client file data |
| 14 | Client-to-host image data |
| 15 | Client-to-host image cancel |
| 16 | Host-to-client image data |
| 17 | Host-to-client image cancel |
| 18 | Client machine name |
| 19 | Host machine name |

Text, file names, and machine names are observed as UTF-16LE without an added protocol terminator; the sizes determine the content length. `ThisSize` MUST NOT exceed the data-array capacity or the remaining declared `TotalSize`.

Command 16 carries an eight-octet helper-status object:

| Offset | Size | Type | Field |
|---:|---:|---|---|
| 0 | 2 | `u16le` | OS: 0 none, 1 Windows, 2 Linux |
| 2 | 2 | `u16le` | Helper protocol version, observed value 1 |
| 4 | 2 | `u16le` | Action: 0 not running, 1 running; observed lifecycle values also include 3 launched and 4 closed |
| 6 | 2 | `u16le` | Reserved |

Clipboard parsing is security-sensitive. A client MUST bound sizes, sanitize received filenames, avoid path traversal, and require explicit user approval before writing files.

## 11. DVC server-to-client video bitstream

### 11.1 Stream model

After the channel-0 ServerHello, S2C KVM bytes form one continuous encrypted and compressed video bitstream. There is no outer length-prefixed video-packet layer.

The decoder consumes bits least-significant-bit first from each incoming octet, but reverses each requested field so the first transmitted bit becomes that field's logical most-significant bit. One equivalent field reader is:

```text
accumulator |= next_octet << available_bit_count
raw          = accumulator & ((1 << field_width) - 1)
accumulator >>= field_width
value        = reverse_low_n_bits(raw, field_width)
```

Field widths are at most eight bits in the observed state machine.

More than 30 consecutive zero bits trigger resynchronization. The decoder discards zero octets until a nonzero octet appears, clears the bit accumulator, and re-enters RESET. Implementations SHOULD cap work per input buffer to prevent malicious streams from causing unbounded CPU use.

### 11.2 Framebuffer and blocks

The framebuffer is divided into 16-pixel-wide blocks. Normal block height is 16 pixels. If the header advertises half-height capability and the horizontal resolution exceeds 1616 pixels, block height becomes 8 and several coordinate/repeat fields widen from 7 to 8 bits.

Video mode fields encode:

```text
width  = size_x * 16
height = size_y * 16 + size_y_remainder
```

Zero dimensions are commonly treated as an 800×600 fallback.

### 11.3 Color representation

The effective bits per color channel are:

```text
bits_per_color = 5 - (bpc_parameter & 3)
```

Thus the effective width is 5, 4, 3, or 2 bits per red, green, and blue component. A packed color is:

```text
color = (red << (2 * bits_per_color))
      | (green << bits_per_color)
      | blue
```

Each component is expanded to eight bits by left-shifting `8 - bits_per_color`; the observed decoder does not replicate low bits.

### 11.4 Decoder state table

Each row reads `Bits` and branches to `On 0` or `On nonzero`. State 35 dynamically redirects to the color-cache code state, and state 31's nonzero target is updated dynamically after cache changes.

| ID | Name | Bits | On 0 | On nonzero |
|---:|---|---:|---:|---:|
| 0 | RESET | 0 | 1 | 1 |
| 1 | START | 1 | 2 | 15 |
| 2 | PIXELS | 1 | 31 | 3 |
| 3 | PIXLRU1 | 1 | 2 | 11 |
| 4 | PIXLRU0 | 1 | 2 | 11 |
| 5 | PIXCODE1 | 1 | 10 | 10 |
| 6 | PIXCODE2 | 2 | 10 | 10 |
| 7 | PIXCODE3 | 3 | 10 | 10 |
| 8 | PIXGREY | BPC | 10 | 10 |
| 9 | PIXRGBR | BPC | 41 | 41 |
| 10 | PIXRPT | 1 | 2 | 11 |
| 11 | PIXRPT1 | 1 | 33 | 12 |
| 12 | PIXRPTSTD1 | 3 | 2 | 2 |
| 13 | PIXRPTSTD2 | 3 | 2 | 2 |
| 14 | PIXRPTNSTD | 8 | 2 | 2 |
| 15 | CMD | 1 | 16 | 17 |
| 16 | CMD0 | 1 | 19 | 18 |
| 17 | MOVEXY0 | 7/8 | 39 | 39 |
| 18 | EXTCMD | 1 | 22 | 23 |
| 19 | CMDX | 1 | 20 | 21 |
| 20 | MOVESHORTX | 3 | 1 | 1 |
| 21 | MOVELONGX | 7/8 | 1 | 1 |
| 22 | BLKRPT | 1 | 34 | 28 |
| 23 | EXTCMD1 | 1 | 25 | 24 |
| 24 | FIRMWARE | 8 | 46 | 46 |
| 25 | EXTCMD2 | 1 | 26 | 27 |
| 26 | MODE0 | 7 | 40 | 40 |
| 27 | TIMEOUT | 0 | 1 | 1 |
| 28 | BLKRPT1 | 1 | 29 | 30 |
| 29 | BLKRPTSTD | 3 | 1 | 1 |
| 30 | BLKRPTNSTD | 7/8 | 1 | 1 |
| 31 | PIXFAN | 1 | 36 | dynamic cache state |
| 32 | PIXCODE4 | 4 | 10 | 10 |
| 33 | PIXDUP | 0 | 2 | 2 |
| 34 | BLKDUP | 0 | 1 | 1 |
| 35 | PIXCODE | 0 | dynamic | dynamic |
| 36 | PIXSPEC | 1 | 8 | 9 |
| 37 | EXIT | 0 | 37 | 37 |
| 38 | LATCHED | 1 | 38 | 38 |
| 39 | MOVEXY1 | 7/8 | 1 | 1 |
| 40 | MODE1 | 7 | 47 | 47 |
| 41 | PIXRGBG | BPC | 42 | 42 |
| 42 | PIXRGBB | BPC | 10 | 10 |
| 43 | HUNT | 1 | 43 | 0 |
| 44 | PRINT0 | 8 | 45 | 45 |
| 45 | PRINT1 | 8 | 45 | 45 |
| 46 | CORP | 1 | 1 | 24 |
| 47 | MODE2 | 4 | 1 | 1 |

### 11.5 Pixel and block operations

- `MOVESHORTX` sets `last_x = (last_x + code + 1) & 0x7f`.
- `MOVELONGX` replaces `last_x`; the 16-high-block mode masks it to seven bits.
- `MOVEXY0` stores the new X and `MOVEXY1` stores Y, then commits both as the current block position.
- `PIXDUP` emits the last color once.
- `PIXRPTSTD1` codes 0–5 emit the last color `code + 2` times. Code 6 reads `PIXRPTSTD2` and adds 8; code 7 reads an eight-bit `PIXRPTNSTD` count.
- `BLKDUP` commits/copies one block. `BLKRPTSTD` commits `code + 2` blocks. `BLKRPTNSTD` commits `code` blocks.
- When `block_width * block_height` pixels have been accumulated, the decoder commits the block, prunes the color cache, and advances the block coordinate.
- `TIMEOUT` aligns to the next octet boundary. A repeated timeout at the same stream position moves the decoder to LATCHED.
- LATCHED is a recovery state. The historical client requests a refresh after 32768 iterations.
- HUNT consumes zero bits until a one bit appears, then resets bit alignment and returns to RESET.

### 11.6 Color cache

The cache contains at most 17 packed colors and uses an LRU usage number where zero is most recently used. A color referenced in the current block has its block counter set. At the end of a block, entries not referenced in that block are removed and referenced entries age for the next prune.

The active-entry count selects the cache-code state:

| Active entries | State | Code width/behavior |
|---:|---:|---|
| 0–1 | 38 | LATCHED/special handling |
| 2 | 4 | One-bit path forced to usage 0 |
| 3 | 5 | One bit; nonzero code is incremented |
| 4–5 | 6 | Two-bit code |
| 6–9 | 7 | Three-bit code |
| 10–17 | 32 | Four-bit code |

Cache codes identify LRU usage values, not stable array indexes. A successful lookup makes the chosen color most recently used and increments more-recent usage values as necessary.

### 11.7 Embedded firmware commands

State FIRMWARE reads one command octet at a time. State CORP reads a continuation bit: one returns to FIRMWARE for another octet; zero executes the final octet as the opcode and treats preceding octets as parameters.

Known opcodes:

| Opcode | Meaning |
|---:|---|
| 1 | Exit video decoder |
| 2 | Begin NUL-terminated printable/status string; next state is PRINT0/PRINT1 |
| 3 | Frame-rate notification; parameter 0 is the rate |
| 4 | Server power changed to on |
| 5 | Server power changed to off |
| 6 | Clear screen |
| 7, 8 | Observed no-op/reserved |
| 9 | Align decoder to next octet boundary |
| 10 | Seize notification |
| 11 | Set bits-per-color from parameter 0 |
| 12 | Set legacy video encryption selection from parameter 0 |
| 13 | Stream header; parameters are BPC, encryption, license bits, flags |
| 16 | Request client ACK record `0c 00` |
| 128 | Frame complete / repaint notification |

For opcode 13, flag bit `0x08` advertises half-height capability. The observed license parameter uses bits for remote console, playback, multi-user remote console, and scripted virtual media, but its exact cross-generation semantics SHOULD be treated as advisory.

PRINT0 reads a string type and PRINT1 reads octets until NUL. Observed types 1–3 update status text and type 4 requests a message box. Implementations MUST bound the string length and MUST treat it as untrusted display text.

## 12. Virtual media protocol v2

### 12.1 Channel setup

Virtual media opens a separate TCP connection to `VmPort` and performs the generic protocol-v2 handshake. Channel 3 selects virtual CD/DVD. The first such connection commonly uses KDF pair 2.

Once connected, the iLO sends fixed 12-octet SCSI command descriptor blocks (CDBs). Standard SCSI fields inside these CDBs use big-endian order even though the surrounding reply header uses little-endian fields.

### 12.2 Reply header

Every normal client reply starts with 16 octets:

| Offset | Size | Type | Field |
|---:|---:|---|---|
| 0 | 4 | bytes | Magic `de c0 ad 0b` |
| 4 | 4 | `u32le` | Flags |
| 8 | 1 | `u8` | Media present: 0 absent, 1 present |
| 9 | 1 | `u8` | SCSI sense key |
| 10 | 1 | `u8` | Additional Sense Code (ASC) |
| 11 | 1 | `u8` | Additional Sense Code Qualifier (ASCQ) |
| 12 | 4 | `u32le` | Payload length |
| 16 | `length` | bytes | Optional payload |

Known flag bits:

| Bit | Value | Meaning |
|---:|---:|---|
| 0 | 1 | Write protected |
| 1 | 2 | Keepalive |
| 2 | 4 | Disconnect |

A keepalive is a zero-length header with flag 2. Observed clients send one approximately every second while media is active. Keepalives and SCSI replies share one encryption stream, so all writers MUST be serialized.

### 12.3 Synchronization request

If the first request octet is `0xfe`, the 12-octet request is a synchronization message rather than a SCSI CDB. The client returns a 16-octet keepalive header, then copies request offsets 4–11 into reply offsets 8–15. Those copied bytes intentionally replace the normal media/sense/length area.

### 12.4 Supported virtual-CD SCSI commands

The following command set is sufficient for the currently implemented ISO-backed CD/DVD path:

| Opcode | Command | Behavior |
|---:|---|---|
| `0x00` | TEST UNIT READY | Reports no media, one-time unit attention, or ready. |
| `0x1b` | START STOP UNIT | Always sends success. An eject request (`LOEJ=1`, `START=0`) ends the media session. |
| `0x1d` | SEND DIAGNOSTIC | Observed implementation sends no reply; this behavior requires more validation. |
| `0x1e` | PREVENT/ALLOW MEDIUM REMOVAL | Success with no payload. |
| `0x25` | READ CAPACITY(10) | Eight-byte payload: last LBA and block length, both `u32be`. |
| `0x28` | READ(10) | Reads ISO sectors. |
| `0x43` | READ TOC/PMA/ATIP | Returns a synthetic single-track data-CD TOC. |
| `0x4a` | GET EVENT STATUS NOTIFICATION | Returns media-class event state. |
| `0x5a` | MODE SENSE(10) | Returns `00 08 01 00 00 00 00 00`. |
| `0xa8` | READ(12) | Reads ISO sectors. |
| other | Unsupported | Sense key 5, ASC 36, ASCQ 0. |

The virtual-CD logical block size is 2048 octets.

For START STOP UNIT, `cdb` denotes the complete 12-octet request and `cdb[4]` denotes its fifth octet (offset 4; indexes are zero-based). The two least-significant bits of that octet are:

| Offset | Bit | Mask | Name | Meaning |
|---:|---:|---:|---|---|
| 4 | 1 | `0x02` | `LOEJ` | Load/eject operation requested |
| 4 | 0 | `0x01` | `START` | Start/load when set; stop/unload when clear |

The four possible low-bit values are therefore:

| `cdb[4] & 0x03` | `LOEJ` | `START` | Requested operation | Implemented session behavior |
|---:|---:|---:|---|---|
| `0x00` | 0 | 0 | Stop without eject | Reply success and continue |
| `0x01` | 0 | 1 | Start without load/eject | Reply success and continue |
| `0x02` | 1 | 0 | Unload/eject | Reply success, mark the medium ejected, and end the media session |
| `0x03` | 1 | 1 | Load and start | Reply success and continue |

In source-code notation, the eject test is `(cdb[4] & 0x03) == 0x02`. The expression as a whole is Boolean, but `cdb[4] & 0x03` first extracts the two protocol bits; binary `10` means `LOEJ=1` and `START=0`.

READ(10):

```text
LBA          = BE32(cdb[2:6])
block_count  = BE16(cdb[7:9])
```

READ(12):

```text
LBA          = BE32(cdb[2:6])
block_count  = BE32(cdb[6:10])
```

The client replies with one header announcing the complete byte length, followed by exactly that many ISO bytes. Large transfers MAY be written to the TCP stream in smaller chunks; a 131072-octet write chunk is known to interoperate. No additional per-chunk header is inserted.

An out-of-range read returns sense key 5, ASC 33, ASCQ 0.

### 12.5 Media state and sense responses

Observed states:

| Condition | Sense key | ASC | ASCQ |
|---|---:|---:|---:|
| Success / ready | 0 | 0 | 0 |
| No medium | 2 | 58 | 0 |
| Medium changed / initial unit attention | 6 | 40 | 0 |
| LBA out of range | 5 | 33 | 0 |
| Unsupported/invalid operation | 5 | 36 | 0 |

With a newly attached ISO, the first readiness/capacity interaction can report unit attention before the device becomes ready. Clients serving media MUST keep this state consistent across commands.

READ CAPACITY returns:

```text
BE32(number_of_2048_byte_blocks - 1) || BE32(2048)
```

### 12.6 READ TOC and event details

The implemented READ TOC path supports format 0 and format 1, honors the MSF bit, caps its generated payload at 412 octets, and respects the allocation length in CDB offsets 7–8. It presents one data track and a lead-out entry. For MSF responses, the lead-out position is derived using 75 frames per second and a two-second offset.

GET EVENT STATUS requires the `IMMED` flag in CDB offset 1, bit 0, to be set. If the media-event-class flag in CDB offset 4, bit 4 (`0x10`) is absent, the response indicates supported class `0x10`. Otherwise the response reports inserted, unchanged, or ejected media and advances its event state only when enough response bytes were allocated.

These synthetic responses are compatibility behavior, not a complete implementation of the SCSI Multimedia Commands standard.

## 13. Session lifecycle and recommended connection order

A robust protocol-v2 client commonly performs:

1. Establish HTTPS with certificate validation where possible.
2. POST login and retain the session token and cookies.
3. Fetch RcInfo and validate the enabled flag, protocol version, ports, and master key.
4. Derive KDF pair 0 and open channel 0 (`New`) on `RcPort`.
5. If status is Busy, close the socket and, only after user choice, retry using `Acquire` or `Share` with a fresh IV.
6. Derive pair 1 and open channel 1 (`New`) on `RcPort`.
7. Start exact-length command-packet reads.
8. Decrypt and feed KVM S2C bytes continuously to the DVC decoder.
9. Wait for DVC header opcode 13 before enabling ordinary input; then send all-keys-up.
10. If mounting an ISO, derive/try the appropriate next pair and open channel 3 on `VmPort`.
11. Serialize input, command, and virtual-media writes independently per socket.
12. On shutdown, release input, close media and command channels, close KVM, and attempt HTTPS logout.

Clients SHOULD use bounded connect/read deadlines but MUST preserve stream-cipher state when a read times out without consuming bytes.

## 14. Legacy protocol version 1 (partial)

This section is intentionally non-normative and **Observed**. It is included to reduce duplicated research, but it is not complete enough to claim full v1 interoperability.

### 14.1 KVM and command negotiation

The client opens TCP to `rc_port`. For KVM it expects initial server byte `0x50` (`P`). For the command channel, the historical client scans up to 30 bytes for the same marker.

The client then sends one of these packed request structures:

```text
u16le command
ASCII[32] or ASCII[56] session_key
```

The 56-octet form is used by an observed Integrity-iLO variant; the usual form is 34 octets total.

Base command values:

| Value | Meaning |
|---:|---|
| `0x2001` | KVM connection |
| `0x2002` | Command connection |

If `ENCRYPT_KEY` is active, every session-key octet is XORed with successive characters/octets of the `enc_key` representation, wrapping at its length. The command is ORed with:

| Bit | Condition |
|---:|---|
| `0x8000` | Encrypted key, normal legacy selection |
| `0x4000` | Encrypted key when `ENCRYPT_VMKEY` is selected |

Observed one-octet server responses:

| Byte | ASCII | Meaning |
|---:|---|---|
| `0x51` | Q | Failure/session limit/denial, depending on state |
| `0x52` | R | Success |
| `0x53` | S | Busy; offer seize/share negotiation |
| `0x57` | W | Success without remote-console license indication |
| `0x58` | X | No free sessions |
| `0x59` | Y | Busy variant with multi-user-console licensing difference |

For busy negotiation, the client sends `0x0055` to seize or `0x0056` to share and waits for `Q` or `R`. After a successful share response, the joining client closes the iLO socket, listens locally on `rc_port`, and waits up to ten seconds for the existing session leader to connect back. The leader learns the joiner's IP address through command-channel command 9, asks the user for approval, sends raw command word 4 on approval, and then connects to the joiner's listener. Firewalls, NAT, VPNs, or management-network isolation can therefore prevent V1 sharing even when both clients can reach iLO.

### 14.2 Legacy command encryption

If `ENCRYPT_CMD` is enabled, the client initializes independent AES stream transforms for send and receive using `cmd_enc_key` and an all-zero 16-octet IV. It first writes the unencrypted marker:

```text
08 00 00 00
```

Subsequent command-channel bytes use the AES stream transform described in Section 7.

### 14.3 Legacy video encryption selection

Embedded DVC header/command values select:

| Value | Cipher |
|---:|---|
| 0 | None |
| 1 | RC4-compatible keystream |
| 2 | AES-128 stream transform |
| 3 | AES-256 stream transform |

Legacy RC4 uses separate send and receive keystream states. Exact key initialization and all negotiation variants remain under-documented.

### 14.4 Legacy virtual-media hello

The version-1 virtual-media client connects to `vm_port` and sends 34 octets:

| Offset | Size | Field |
|---:|---:|---|
| 0 | 1 | Constant `0x10` |
| 1 | 1 | Device: 1 floppy, 2 CD-ROM, 3 USB key/virtual folder |
| 2 | 32 | Virtual-media/session key |

The server returns four octets. The first two indicate success when they are `20 00`. Known first-octet errors are:

| Value | Meaning |
|---:|---|
| 33 | Drive already connected |
| 34 | Invalid key |
| 35 | Virtual media not licensed |
| 36 | Key expired |
| 37 | Drive not configured |
| 38 | Permission required |

After success, the existing 12-octet SCSI request and 16-octet reply machinery is used directly. The V1 virtual-media socket does not use the protocol-v2 channel hello or stream encryption. If `ENCRYPT_VMKEY` is advertised, the actual ASCII session-key octets in the 32-octet hello field are XORed with repeating octets of the textual hexadecimal `enc_key`; the remaining field is NUL-padded. ISO/CD-ROM uses device value 2.

## 15. Error handling and defensive parsing

Independent implementations SHOULD apply all of the following:

- Treat every network length, coordinate, count, sequence, and allocation size as untrusted.
- Use exact-length reads for ClientHello/ServerHello counterparts, command headers, SCSI CDBs, and fixed clipboard objects.
- Reject unknown protocol versions unless a deliberately compatible path exists.
- Reject malformed hex keys, invalid ports, oversized session keys, impossible command sizes, and arithmetic overflow.
- Cap video decoder steps per received byte and retain incomplete state for the next read.
- Bound firmware strings, clipboard totals, file paths, playback buffers, and ISO reads.
- Serialize all encrypted writes on each socket.
- On any cipher desynchronization, close and reconnect; stream state cannot be repaired by skipping arbitrary bytes.
- Do not reinterpret a failed v2 hello as plaintext v1 traffic on the same socket.
- Preserve unknown flags and status values in diagnostics, but do not expose secrets.
- Make Acquire, Share, power operations, file writes, and media mounting explicit user decisions.

## 16. Security considerations

The protocol exposes complete keyboard, video, mouse, power, clipboard, and virtual-media control of a server. It MUST be treated as a privileged management protocol.

### 16.1 Authentication bootstrap

HTTPS transports the username, password, session token, and master key. Disabling certificate verification permits active interception of all of them. Compatibility with self-signed certificates SHOULD be implemented through explicit trust, certificate pinning, or a clearly labeled user override—not silent acceptance.

### 16.2 Channel cryptography

Protocol-v2 channels provide observed confidentiality through AES-OFB-like encryption, but no authenticated-encryption tag or per-record MAC has been identified. Stream-cipher ciphertext is malleable: changing a ciphertext bit predictably changes the corresponding plaintext bit. Network isolation, a trusted management VLAN/VPN, and authenticated HTTPS bootstrap remain important.

IVs for C2S MUST be generated with a cryptographically secure random source and MUST NOT be reused with the same key. S2C IVs are supplied by iLO.

### 16.3 Secrets and diagnostics

Passwords, session tokens, `MasterKey`, `enc_key`, `cmd_enc_key`, `vm_key`, derived keys, plaintext clipboard content, and decrypted console traffic MUST NOT appear in ordinary logs. Debug capture requires explicit user consent and secure storage.

### 16.4 Input and power safety

Synthetic input can alter firmware settings, credentials, boot configuration, and the running OS. Power operations can cause data loss. Clients SHOULD visibly distinguish local UI shortcuts from keys sent to the server and SHOULD release all keys when focus changes.

### 16.5 Clipboard and virtual media

Clipboard filenames and data are remote-controlled input. Receivers MUST prevent path traversal, reserved-device names, unintended overwrite, and oversized allocation. Virtual media exposes local file contents to the managed server; clients SHOULD mount read-only by default and SHOULD show the active image path.

## 17. Interoperability and conformance checklist

### 17.1 Bootstrap

- [ ] Login accepts either `key` or `session_key`.
- [ ] Cookie state and `X-Auth-Token` are retained.
- [ ] RcInfo fallback occurs only after HTTP 404.
- [ ] Hex keys and port ranges are validated.
- [ ] Secrets are redacted from logs.

### 17.2 Cryptography and handshake

- [ ] The Section 6.3 KDF vector passes exactly.
- [ ] C2S and S2C use the correct half of each pair.
- [ ] ClientHello is exactly 54 octets; only offsets 16–53 are encrypted.
- [ ] ServerHello is exactly 53 octets; only offsets 16–52 are decrypted.
- [ ] Application cipher state continues after the encrypted hello bytes.
- [ ] Every retry uses a new socket and C2S IV.
- [ ] Writes are serialized per channel.
- [ ] Legacy KVM and command negotiation covers encrypted keys, busy acquire/share, and the reverse-listener handoff.

### 17.3 KVM and video

- [ ] Keyboard, all-keys-up, mouse, power, refresh, and ACK records match the defined bytes.
- [ ] HID usage IDs are not confused with Unicode or platform key codes.
- [ ] The bit reader handles fields spanning TCP reads.
- [ ] Video mode changes resize and clear the framebuffer safely.
- [ ] Color cache, repeats, block commits, and resynchronization are bounded.
- [ ] Input remains disabled until a valid DVC header has been processed.

### 17.4 Command and virtual media

- [ ] Command payloads are length-bounded before allocation/indexing.
- [ ] POST and power notifications tolerate short/unknown payloads safely.
- [ ] Clipboard totals and filenames are validated.
- [ ] Virtual-media headers use little-endian fields while SCSI CDB fields use big-endian.
- [ ] READ(10), READ(12), capacity, initial unit attention, out-of-range sense, and eject are tested.
- [ ] Keepalive and SCSI writes cannot interleave within the cipher transform.
- [ ] Legacy virtual media sends its 34-octet hello, validates the four-octet status, and applies `ENCRYPT_VMKEY` only to actual session-key octets.
- [ ] Legacy share leaders relay decrypted DVC bytes to peers and forward peer input/open/close records to iLO.

## 18. Known unknowns and research priorities

The following topics need additional captures across iLO firmware generations:

1. Exact generation/firmware matrix for the Redfish-style RcInfo endpoint and protocol versions.
2. Semantics of protocol-v2 ClientHello and ServerHello flags.
3. Whether the ServerHello session-key field is always an echo, a replacement, or reserved.
4. Formal channel-to-KDF-pair assignment when channels are opened out of the usual order or concurrently.
5. Complete version-1 key setup, RC4 initialization, and Integrity-iLO variations.
6. Virtual floppy, USB-key, and virtual-folder SCSI/device behavior.
7. SEND DIAGNOSTIC reply expectations in the virtual-CD path.
8. Complete playback-buffer and capture-file framing.
9. Full command 7, 13, 16, and 17 flag/status registries across firmware.
10. Peer-to-peer shared-console interoperability across firmware revisions, multiple simultaneous peers, IPv6, and routed or NATed networks.
11. DVC PRINT string encoding outside ASCII and all license/header flag meanings.
12. Whether any firmware adds integrity protection, rekeying, or alternative ciphers.

Contributors should publish sanitized packet lengths, state transitions, firmware versions, and synthetic test cases. They MUST NOT publish live credentials, session tokens, master keys, personal clipboard data, or proprietary firmware/code.

## 19. Implementation notes for this repository

The independent implementation and regression evidence corresponding to this draft are organized as follows:

| Area | Source |
|---|---|
| HTTPS login and RcInfo | `internal/ilo` |
| KDF, AES stream, hello, KVM input, command packets, DVC decoder | `internal/kvm` |
| ISO and virtual-CD SCSI tunnel | `internal/vmedia` |
| Session/channel orchestration | `internal/app` |

The source code is authoritative for what the current client actually supports; this document also records observed legacy behavior not yet implemented. A change to a verified wire layout SHOULD add or update a regression test and this document in the same change.

## 20. IANA considerations

This document requests no IANA actions. Channel numbers, command IDs, status codes, and other registries described here are private protocol values observed on iLO connections; they are not IANA assignments.

## 21. Publication and license

This independent document is distributed under the repository's [MIT License](LICENSE). It describes observed interoperability facts and does not grant rights in HPE products, firmware, documentation, or trademarks.

Corrections and sanitized interoperability evidence are welcome. Contributions should preserve the evidence labels in Section 1.1 and avoid incorporating proprietary source code.

## 22. References

### 20.1 Normative style and external formats

- S. Bradner, [RFC 2119: Key words for use in RFCs to Indicate Requirement Levels](https://www.rfc-editor.org/rfc/rfc2119.html), March 1997.
- B. Leiba, [RFC 8174: Ambiguity of Uppercase vs Lowercase in RFC 2119 Key Words](https://www.rfc-editor.org/rfc/rfc8174.html), May 2017.
- USB Implementers Forum, [Human Interface Device specifications and HID Usage Tables](https://www.usb.org/hid).

### 20.2 Public HPE port documentation

- Hewlett Packard Enterprise, [Ports used by iLO features — HPE iLO 5 User Guide](https://support.hpe.com/hpesc/public/docDisplay?docId=a00105236en_us&docLocale=en_US&page=GUID-D7147C7F-2016-0901-0930-0000000004B9.html).
- Hewlett Packard Enterprise, [Remote console access setting details — HPE iLO 6 User Guide](https://support.hpe.com/hpesc/public/docDisplay?docId=sd00002007en_us&docLocale=en_US&page=GUID-A634B9CB-088E-4BB9-A370-4547F6CF0AA7.html).

These HPE references document product configuration and public port assignments. They do not endorse this reverse-engineered wire specification.

## Appendix A. Minimal protocol-v2 pseudocode

```text
https = connect_https(ilo_address, verify_certificate=true)
session = POST /json/login_session {method:"login", user_login:user, password:pass}
rc = GET /redfish/v1/Managers/1/RcInfo/
if rc.status == 404:
    rc = GET /json/rc_info

master = hex_decode(rc.MasterKey or rc.enc_key)
kdf_stream = ilo_irc_kdf(master)

kvm_c2s = take(kdf_stream, 16)
kvm_s2c = take(kdf_stream, 16)
cmd_c2s = take(kdf_stream, 16)
cmd_s2c = take(kdf_stream, 16)

kvm = tcp_connect(rc.RcPort)
kvm = v2_hello(kvm, NEW, KVM, session.key, kvm_c2s, kvm_s2c)

cmd = tcp_connect(rc.RcPort)
cmd = v2_hello(cmd, NEW, CMD, session.key, cmd_c2s, cmd_s2c)

parallel:
    decrypt_kvm_bytes_and_feed_dvc(kvm)
    read_and_dispatch_command_packets(cmd)

when dvc_header_received:
    write_encrypted(kvm, ALL_KEYS_UP)
    enable_local_input()
```

## Appendix B. Protocol-v2 hello hex layout template

Before encryption, with zero flags and a three-octet session token `abc`, the ClientHello body layout is:

```text
00..15  <16-octet random IV>
16      <command>
17      <channel>
18..21  00 00 00 00
22..24  61 62 63
25..53  00 ... 00
```

Only offsets 16–53 are transformed. The IV remains visible on the wire.

## Appendix C. Change policy

This draft favors reproducibility over apparent completeness. New values SHOULD be added only when supported by one or more of:

- a sanitized packet capture with byte offsets and direction;
- a deterministic interoperability test;
- behavior reproduced on multiple firmware versions;
- an unambiguous packed structure plus a matching live observation.

Guesses SHOULD remain in “Known unknowns” until evidence exists. This prevents an invented constant from becoming de facto protocol folklore.
