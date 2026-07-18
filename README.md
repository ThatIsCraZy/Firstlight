# HPE-KVM (`hpeirc`)

> [!IMPORTANT]
> HPE-KVM is an independent community open-source project. It is **not affiliated with, sponsored by, endorsed by, authorized by, or otherwise connected to Hewlett Packard Enterprise (HPE)**. It is not an official HPE product, and HPE does not provide support for it.

HPE-KVM is a native Windows remote-console client for servers managed through HPE Integrated Lights-Out (iLO). It is intended as a community-maintained successor to the legacy `HPLOCONS` client, which is no longer actively maintained, and as a foundation for features that go beyond the original client.

The references to HPE and iLO exist only to explain which systems this software interoperates with.

## Goals

- Provide a maintained, open-source iLO remote-console client.
- Preserve access to systems for which the legacy client is no longer a practical option.
- Offer a small, standalone Windows application without requiring the original HPE client.
- Make keyboard translation, clipboard input, virtual media, and future language support extensible.
- Document and test the implementation so that it can be maintained by the community.

## Features

- Native Windows GUI with a persistent multi-session launcher.
- Remote KVM video, keyboard, and mouse input.
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
- The remote-console implementation requires iLO remote-console protocol version 2 or newer. Legacy version 1 negotiation is not implemented.
- Virtual media currently supports ISO images as virtual CD/DVD media.
- Keyboard maps translate a local source layout to a remote US layout. Characters without a defined HID sequence are skipped during clipboard input.
- Compatibility can vary by iLO generation, firmware, licensing, and server configuration.
- This is an independent implementation. Test carefully before relying on it for production recovery workflows.

## Building from source

Requirements:

- Windows
- Go 1.22 or newer

From PowerShell:

```powershell
git clone https://github.com/ThatIsCraZy/HPE-KVM.git
Set-Location HPE-KVM
go test ./...
go build -trimpath -ldflags '-H=windowsgui' -o hpeirc.exe ./cmd/hpeirc
```

The generated `hpeirc.exe` is self-contained. Build products are intentionally not stored in the source tree; published binaries should be attached to GitHub Releases.

## Running

Start `hpeirc.exe` without arguments to open the launcher. Saved passwords are encrypted for the current Windows user with DPAPI.

The client can also be started directly:

```powershell
.\hpeirc.exe -addr 192.0.2.10 -name Administrator -password 'your-password'
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

## Keyboard maps

At startup, the application creates a `keyboard-maps` directory beside the executable. Valid JSON files directly inside that directory become selectable keyboard layouts after the next application restart.

The **Keyboard Layout** menu provides:

- `Default` physical input behavior.
- All valid built-in and external maps.
- `Export built-in German map...`, which writes a loadable JSON template and an accompanying English Markdown guide for humans and LLMs.

Generated references are stored under `keyboard-maps/_examples`. See the exported guide and JSON schema for the complete map format, symbolic HID names, modifiers, inheritance rules, and validation checklist.

## Third-party packages and acknowledgements

HPE-KVM is built with a small set of permissively licensed Go modules:

| Package | How it is used | License | Copyright holder / authors |
|---|---|---|---|
| [`github.com/lxn/walk`](https://github.com/lxn/walk) | Native Windows GUI toolkit | [BSD 3-Clause](https://github.com/lxn/walk/blob/c389da54e794/LICENSE) | The Walk Authors |
| [`github.com/lxn/win`](https://github.com/lxn/win) | Win32 API bindings used by the GUI and input handling | [BSD 3-Clause](https://github.com/lxn/win/blob/a377121e959e/LICENSE) | The win Authors |
| [`golang.org/x/sys`](https://pkg.go.dev/golang.org/x/sys) | Low-level Windows system interfaces, included transitively through Walk | [BSD 3-Clause](https://github.com/golang/sys/blob/v0.25.0/LICENSE) | The Go Authors |
| [`gopkg.in/Knetic/govaluate.v3`](https://github.com/Knetic/govaluate) | Expression evaluation, included transitively through Walk | [MIT](https://github.com/Knetic/govaluate/blob/v3.0.0/LICENSE) | George Lester |

The exact versions are recorded in [`go.mod`](go.mod) and reproducibly verified by [`go.sum`](go.sum). “Transitively” means that HPE-KVM does not import the package directly, but a direct dependency requires it.

Our sincere thanks go to the Walk Authors, the win Authors, the Go Authors, George Lester, and all contributors to these projects. Their work makes this independent open-source client possible. The complete copyright notices and license terms are reproduced in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md); those licenses continue to apply to the respective third-party components.

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
