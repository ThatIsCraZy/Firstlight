# MCP follow-up backlog

Status: implemented locally on 2026-08-15; live iLO firmware acceptance remains
open. These items were recorded after the first successful parallel HVM
installation through the local stdio MCP bridge on two DL360 Gen10 systems.

## Priority 1

### One-time virtual CD/DVD boot

Implementation: `ilo_console_set_one_time_boot` reuses the authenticated web
session owned by `console_handle`, accepts only `device="cd"`, patches only the
two Redfish BootOverride fields, and verifies the result with a fresh GET. It
does not send a reset. Unsupported firmware and POST-busy responses are reduced
to safe errors without returning the remote response body.

Add `ilo_console_set_one_time_boot(operation_id, console_handle, device,
confirm)`.

- Initially support only `device="cd"` for the virtual CD/DVD device.
- Reuse the authenticated iLO/KVM session represented by `console_handle`; do
  not require a separate Raw-Redfish login.
- Set BootOverride only. Never imply or send a reset.
- Require `operation_id` and `confirm: true`; deduplicate retries through the
  existing per-console operation mechanism.
- Return a structured before/current target, enabled and UEFI/legacy state,
  plus a verified GET result after the change.
- Report unsupported firmware and POST-busy states safely, without credentials,
  session keys, console handles or filesystem paths.

Live motivation: F11 required a BIOS administrator password. One host required
a one-off Raw-Redfish `Cd`/`Once` override, while the other accepted the same
credentials through KVM but rejected them through Raw Redfish.

### Waiting console observation

Implementation: `ilo_console_observe` waits on `state.frame_revision`, caps the
wait at 30 seconds, observes cancellation, and returns the unchanged state on
timeout. The decoder advances the frame revision only for the initial usable
frame or a real framebuffer change, so power/POST events, identical video
blocks, and an initial revision of zero do not create a polling loop.

Complete or verify a compatible waiting form of
`ilo_console_observe(console_handle, after_revision, wait_ms)`.

- Wait for a newer framebuffer revision instead of forcing client-side polling.
- On timeout, return the unchanged structured state.
- An initial revision of zero or a black initial frame must not cause rapid
  polling.
- Cap waits at about 30 seconds and observe context cancellation.

## Priority 2

### Virtual-media health status

Implementation: virtual-media status now distinguishes mounted media,
transport liveness, and firmware device readiness. It also reports ISO payload
bytes read locally and delivered to iLO; only the safe ISO filename is exposed.

Extend the virtual-media status response with:

- transport-alive state;
- device-ready state;
- where available, read/delivered byte counters; and
- a clear distinction between an ISO transport being mounted and firmware
  recognizing a bootable virtual CD device.

Continue returning only safe filenames, never full ISO paths.

### Management status on the existing authenticated session

Implementation: `ilo_console_management_status` reads `PowerState` and the
current BootOverride target, enabled state, and UEFI/legacy mode through the
same authenticated session as the console.

Add a small read-only management-status interface on the same authenticated
console session.

- Read `PowerState` and `BootOverride`.
- Later, consider integrating already-confirmed power actions through that
  interface.
- Avoid making agents manage parallel proprietary web sessions, Redfish Basic
  credentials and KVM sessions for the same iLO target.

## Non-negotiable constraints

- Exactly one virtual-media session per `console_handle`.
- `busy_mode` remains `fail`; no implicit share or seize behavior.
- Console close and TTL reaping unmount and close virtual media.
- Keep local stdio MCP registration.
- Require `operation_id` and `confirm` for state-changing tools.
- Never log or return credentials, session keys, console handles or full ISO
  paths.

## Existing live evidence

The first live acceptance run successfully exercised ten `ilo_console_*` tools:
open, observe, mount, virtual-media status, input, power, unmount and close.
The remaining work is live acceptance of the new boot-override, waiting-observe,
management-status, and virtual-media-health behavior across the supported iLO
firmware generations.
