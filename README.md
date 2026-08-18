# iLO-KVM

> [!IMPORTANT]
> iLO-KVM is an independent community open-source project. It is **not affiliated with, sponsored by, endorsed by, authorized by, or otherwise connected to Hewlett Packard Enterprise (HPE)**. It is not an official HPE product, and HPE does not provide support for it.

iLO-KVM is a native Windows remote-console client for servers managed through HPE Integrated Lights-Out (iLO). It is intended as a community-maintained successor to the legacy `HPLOCONS` client, which is no longer actively maintained, and as a foundation for features that go beyond the original client.

The references to HPE and iLO exist only to explain which systems this software interoperates with.

## Goals

- Provide a maintained, open-source iLO remote-console client.
- Preserve access to systems for which the legacy client is no longer a practical option.
- Offer a small, standalone Windows application without requiring the original HPE client.
- Make keyboard translation, clipboard input, virtual media, and future language support extensible.
- Document and test the implementation so that it can be maintained by the community.

## Features

- Native Windows GUI with a persistent multi-session launcher.
- Standalone MCP bridge for LLM-controlled iLO console sessions over stdio or stateless Streamable HTTP.
- Remote KVM video, keyboard, and mouse input.
- Legacy remote-console protocol V1 for iLO 4, including KVM/command encryption, shared sessions, and ISO virtual media.
- Shared-session and seize-session connection modes.
- Server power controls and display of power/POST status where supported.
- Clipboard-to-HID text input with unsupported-character reporting.
- Modular JSON keyboard maps with built-in US and German mappings.
- Exportable German keyboard-map template with an English LLM authoring guide.
- External keyboard maps loaded from the `keyboard-maps` directory beside the executable.
- ISO virtual-media mounting for supported iLO remote-console protocol versions.
- Optional TLS certificate verification.
- Locally saved credentials protected with Windows DPAPI.

## Current scope and limitations

- Windows is currently the supported desktop platform.
- Remote KVM supports legacy protocol V1 (used by iLO 4) and protocol V2 or newer. Compatibility can still vary between firmware revisions.
- Legacy V1 takeover and shared sessions are supported. Joining a shared V1 session opens a listener on the returned remote-console port; Windows Firewall and the network path must allow the current session leader to connect back to that port. Join requests shown to the session leader are denied automatically before the reverse-listener window expires.
- Virtual media supports ISO images as virtual CD/DVD media on both protocol V1 and V2-or-newer connections.
- A client that joins a legacy shared session receives KVM/input access only; power controls and virtual media remain owned by the session leader.
- Keyboard maps translate a local source layout to a remote US layout. Characters without a defined HID sequence are skipped during clipboard input.
- Compatibility can vary by iLO generation, firmware, licensing, and server configuration.
- This is an independent implementation. Test carefully before relying on it for production recovery workflows.

## Building from source

Requirements:

- Windows
- Go 1.25 or newer

From PowerShell:

```powershell
git clone https://github.com/ThatIsCraZy/iLO-KVM.git
Set-Location iLO-KVM
go test ./...
go build -trimpath -ldflags '-H=windowsgui' -o iLO-KVM.exe ./cmd/ilo-kvm
go build -trimpath -o iLO-KVM-mcp.exe ./cmd/ilo-kvm-mcp
```

The generated executables are self-contained. Build products are intentionally not stored in the source tree; published binaries should be attached to GitHub Releases.

## Running

Start `iLO-KVM.exe` without arguments to open the launcher. Saved passwords are encrypted for the current Windows user with DPAPI.

The client can also be started directly:

```powershell
.\iLO-KVM.exe -addr 192.0.2.10 -name Administrator -password 'your-password'
```

Common options:

| Option | Purpose |
|---|---|
| `-addr` | iLO hostname or IP address, optionally followed by the HTTPS port |
| `-name` | iLO user name |
| `-password` | iLO password |
| `-share` | Request a shared remote-console session when the console is busy |
| `-seize` | Request takeover of a busy remote-console session |
| `-iso` | Mount a local ISO as virtual CD/DVD media after connecting |
| `-verify-cert` | Verify the iLO HTTPS certificate |
| `-debug` | Enable verbose protocol and input logging |
| `-log` | Write verbose logging to a specified file |

`-share` and `-seize` are mutually exclusive.

> [!WARNING]
> A password supplied on the command line may be visible to other local processes. Prefer the interactive launcher on shared systems. TLS certificate verification is disabled by default because many iLO installations use self-signed certificates; use `-verify-cert` whenever the iLO certificate is trusted by the local system.

## MCP bridge

`iLO-KVM-mcp.exe` is a standalone translation layer between an MCP client and the existing iLO/KVM protocol implementation. It does not import the desktop application or its `internal/login` credential store, does not enumerate saved launcher sessions, and does not read the current Windows user's DPAPI-protected `cred.json`.

The MCP client must pass an iLO address or DNS name, username, and password to `ilo_console_open`. These values are used only to establish that live console connection. The password and username are not written to disk or retained in the console object, and a disconnected console is not automatically reconnected. The live iLO session key, network connections, framebuffer, and retry records exist in process memory only until the handle is closed, expires, or the bridge exits.

The default stdio transport is suitable for a locally launched MCP server:

```json
{
  "mcpServers": {
    "hpe-ilo": {
      "command": "C:\\Program Files\\iLO-KVM\\iLO-KVM-mcp.exe"
    }
  }
}
```

For clients that require Streamable HTTP, start the bridge explicitly:

```powershell
.\iLO-KVM-mcp.exe -transport http -listen 127.0.0.1:8765 -endpoint /mcp
```

Virtual media is disabled by default. To allow ISO mounting for this MCP
process, configure one explicit root directory, for example the demo root:

```powershell
.\iLO-KVM-mcp.exe -iso-root Z:\Source\vme
```

Only non-empty regular `.iso` files whose canonical path remains below that
root are accepted. The bridge rejects directory traversal, symlinks and
Windows reparse points. Keep the root writable only by trusted local
administrators: path validation cannot protect against an attacker who can
replace files concurrently after validation.

The HTTP transport uses MCP's stateless mode: every MCP HTTP request has an independent protocol connection and no MCP transport session is stored. A live remote console is necessarily stateful at the iLO protocol level, so tools refer to it through the opaque `console_handle` returned by `ilo_console_open`. Multiple handles can control multiple iLO systems concurrently. Idle handles expire after 15 minutes by default; adjust this with `-session-ttl`.

Available tools:

| Tool | Purpose |
|---|---|
| `ilo_console_open` | Open an ephemeral console from client-supplied iLO connection parameters |
| `ilo_console_observe` | Return connection state and the latest framebuffer, optionally waiting up to 30 seconds for a newer `frame_revision` |
| `ilo_console_type_text` | Type text using a built-in US or German keyboard map |
| `ilo_console_press_keys` | Send a symbolic chord such as `CTRL+ALT+DELETE` or `F12` |
| `ilo_console_mouse` | Move, click, hold, release, or scroll the remote pointer |
| `ilo_console_power` | Send a confirmed power-button, cold-boot, or reset action |
| `ilo_console_management_status` | Read `PowerState` and the current boot override through the existing authenticated iLO session |
| `ilo_console_set_one_time_boot` | Set and verify a confirmed one-time virtual CD/DVD boot override without resetting the server |
| `ilo_console_close` | Release input, close the KVM channels, and log out from iLO |
| `ilo_console_mount_iso` | Mount one confirmed ISO from `-iso-root` as virtual CD/DVD media |
| `ilo_console_virtual_media_status` | Return safe mount, transport, device-ready, ISO-byte-counter, and filename state |
| `ilo_console_unmount_iso` | Confirmed removal of the current virtual-media ISO |

Mutating tools and `ilo_console_open` require a client-generated `operation_id`. Reuse an ID only when retrying the exact same call; this prevents a transport retry from typing, clicking, changing the boot override, resetting, mounting, or unmounting twice. Power, one-time boot override, ISO mount, and ISO unmount additionally require `confirm: true`. The one-time boot tool currently accepts only `device: "cd"`, changes only the Redfish next-boot override, and never resets or power-cycles the server. MCP annotations describe destructive operations, but the MCP client remains responsible for its own approval policy. MCP console opens always use `busy_mode: fail`; the bridge never requests a shared or seize session.

For waiting observation, pass the previous `state.frame_revision` as `after_revision` and a `wait_ms` value from 1 through 30000. The call returns when a real framebuffer change arrives, the console disconnects, the request is cancelled, or the wait expires. A zero `wait_ms` always returns the current state immediately. Virtual-media `read_bytes` counts ISO payload loaded from disk, while `delivered_bytes` counts ISO payload accepted by the iLO transport. `mounted` alone means that the local media session exists; `transport_alive` and `device_ready` distinguish an active connection from firmware having recognized the virtual CD device.

Security notes:

- stdio is the preferred transport. HTTP is deliberately restricted to an explicit loopback address because this release does not implement HTTP authentication or remote exposure.
- The bridge verifies the iLO HTTPS certificate by default. `insecure_skip_verify: true` is an explicit per-connection compatibility opt-out and permits man-in-the-middle attacks.
- Treat the returned `console_handle` as a bearer capability. Any MCP caller that possesses it can observe or operate that live console until it is closed or expires.
- The bridge does not log MCP request bodies, passwords, console handles, or full ISO paths. Virtual-media status returns only a safe ISO filename. The MCP client, LLM provider, process supervisor, or a local proxy may still record tool arguments; configure those components accordingly and use a dedicated least-privilege iLO account.
- Run the bridge under a dedicated service account when an operating-system-level boundary from the desktop user's saved sessions is required.

## Keyboard maps

At startup, the application creates a `keyboard-maps` directory beside the executable. Valid JSON files directly inside that directory become selectable keyboard layouts after the next application restart.

The **Keyboard Layout** menu provides:

- `Default` physical input behavior.
- All valid built-in and external maps.
- `Export built-in German map...`, which writes a loadable JSON template and an accompanying English Markdown guide for humans and LLMs.

Generated references are stored under `keyboard-maps/_examples`. See the exported guide and JSON schema for the complete map format, symbolic HID names, modifiers, inheritance rules, and validation checklist.

## Third-party packages and acknowledgements

iLO-KVM is built with a small set of permissively licensed Go modules:

| Package | How it is used | License | Copyright holder / authors |
|---|---|---|---|
| [`github.com/lxn/walk`](https://github.com/lxn/walk) | Native Windows GUI toolkit | [BSD 3-Clause](https://github.com/lxn/walk/blob/c389da54e794/LICENSE) | The Walk Authors |
| [`github.com/lxn/win`](https://github.com/lxn/win) | Win32 API bindings used by the GUI and input handling | [BSD 3-Clause](https://github.com/lxn/win/blob/a377121e959e/LICENSE) | The win Authors |
| [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) | MCP 2026-07-28 server, tool schemas, stdio, and stateless Streamable HTTP | [Apache-2.0 / MIT transition notice](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/LICENSE) | The Go MCP SDK Authors |
| [`golang.org/x/sys`](https://pkg.go.dev/golang.org/x/sys) | Low-level system interfaces, included transitively | [BSD 3-Clause](https://github.com/golang/sys/blob/v0.41.0/LICENSE) | The Go Authors |
| [`gopkg.in/Knetic/govaluate.v3`](https://github.com/Knetic/govaluate) | Expression evaluation, included transitively through Walk | [MIT](https://github.com/Knetic/govaluate/blob/v3.0.0/LICENSE) | George Lester |

The exact versions are recorded in [`go.mod`](go.mod) and reproducibly verified by [`go.sum`](go.sum). “Transitively” means that iLO-KVM does not import the package directly, but a direct dependency requires it.

Our sincere thanks go to the Walk Authors, the win Authors, the Go MCP SDK Authors, the Go Authors, George Lester, and all contributors to these projects. Their work makes this independent open-source client possible. The applicable copyright and license notices are reproduced in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md); those licenses continue to apply to the respective third-party components.

## Development

Run the complete regression suite and static checks before submitting changes:

```powershell
go test ./...
go vet ./...
```

Contributions are welcome. Keep protocol changes focused, add regression tests, and do not submit HPE binaries, firmware, decompiled proprietary sources, credentials, debug logs, or other material that you do not have the right to redistribute.

## License

The original source code and documentation in this repository are released under the permissive [MIT License](LICENSE). In short, the MIT License permits use, copying, modification, publication, distribution, sublicensing, and commercial use, subject to retaining the copyright and license notice.

The MIT License applies only to material owned by this project's contributors. It does **not** grant any patent, copyright, trademark, branding, or other rights belonging to HPE or any other third party.

## Trademark and non-affiliation notice

Hewlett Packard Enterprise, HPE, iLO, Integrated Lights-Out, HPLOCONS, associated product names, and any related logos or marks are trademarks or registered trademarks of Hewlett Packard Enterprise Company and/or its affiliates. **All rights in those marks remain with their respective owners.**

Their names are used in this project solely in a descriptive and nominative manner to identify the systems with which the software is intended to interoperate. That reference does not imply affiliation, sponsorship, certification, endorsement, or approval by HPE.

This project, its authors, maintainers, and contributors are independent of HPE. For official products, documentation, firmware, licensing, and support, consult HPE through its official channels.

## Warranty disclaimer

The software is provided “as is”, without warranty of any kind. Out-of-band management provides powerful access to server hardware; you are responsible for validating the software and protecting credentials, certificates, networks, and managed systems in your environment.
