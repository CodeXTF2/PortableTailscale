# PortableTailscale

PortableTailscale runs a Tailscale node in userspace and provides an
authenticated SOCKS5 proxy on localhost. It is a single Windows executable and
runs as a regular user.

The Tailscale identity is stored beside the executable, so the same folder can
be carried between Windows computers or kept on a removable drive.

## Quick start

1. Run `PortableTailscale.exe`.
2. Complete the Tailscale sign-in in your browser on first use.
3. Copy the SOCKS5 proxy URL shown in the terminal.
4. Keep PortableTailscale running while the proxy is in use.

The proxy URL has this format:

```text
socks5://tsnet:PASSWORD@127.0.0.1:PORT
```

Use it with any application that supports an authenticated SOCKS5 proxy. A new
localhost port and password are generated each time PortableTailscale starts.

## RDP example

`PortableTailscaleRDP.exe` is an example client included with the project. It
starts PortableTailscale, creates a local bridge, and opens the bundled FreeRDP
client.

1. Place `PortableTailscaleRDP.exe` beside `PortableTailscale.exe`.
2. Put `sdl-freerdp.exe` in the `freerdp` folder. Download the official
   64-bit static build from the
   [FreeRDP nightly CI](https://ci.freerdp.com/job/freerdp-nightly-windows/lastSuccessfulBuild/arch%3Dwin64%2Clabel%3Dvs2017/artifact/install/bin/sdl-freerdp.exe)
3. Open `PortableTailscaleRDP.exe`.
   PortableTailscale starts connecting immediately in the background. Complete
   browser sign-in if prompted; you can fill in the form while it connects.
4. Enter the tailnet computer, Windows username, optional domain, and password.
5. Move to **Connect** and press Enter.

### RDP controls

| Key | Action |
| --- | --- |
| Up / Down | Move between settings |
| Tab | Next tab (wraps to the first) |
| Enter | Move to the next setting; connect from the Connect row |
| Esc | Disconnect, or exit when disconnected |
| Ctrl+C | Exit |
| Ctrl+P | Browse and search saved profiles |
| Ctrl+S | Save or update the current connection profile |
| Ctrl+N | Clear the form for a new connection |
| Ctrl+L | Open diagnostics |
| F1 / F2 / F3 / F4 | Connection / Display / Sharing / Advanced tab |
| Ctrl+Left / Ctrl+Right | Previous / next tab |
| Left / Right or Space | Change the selected option |

### Settings tabs

- **Connection:** computer (including optional `host:port`), username, domain,
  and password.
- **Display:** fullscreen, initial resolution, dynamic desktop resizing, and
  desktop scaling. Initial resolution applies when FreeRDP launches; dynamic
  resizing subsequently follows the window size.
- **Sharing:** clipboard, remote audio, microphone, and a local folder path.
  Leave the folder blank to disable folder sharing. A selected folder appears
  in the remote session as `Shared`; it must exist on the current computer.
- **Advanced:** certificate policy, network preset, and connection timeout.
  `Ignore (legacy)` preserves the existing certificate behavior; `Verify / deny`
  rejects untrusted certificates without an interactive prompt.

Tab and Up/Down move within the current tab; Enter advances to Connect / Retry.
Each tab includes a Connect / Retry button. All options are saved with profiles;
profiles created before these tabs receive the existing defaults.

The launcher stays open when FreeRDP closes or a connection fails. Esc during a
connection disconnects and returns to the form; Ctrl+C disconnects and exits.
The Connect / Retry button reuses the current settings.

The Tailscale connection stays ready between RDP sessions. Connect reuses it,
or waits for startup to finish if necessary. Closing the launcher stops both
FreeRDP and its PortableTailscale process. If Tailscale fails to start or stops
unexpectedly, the next Connect attempt retries it.

Saved profiles contain the computer, username, domain, and every Display,
Sharing, and Advanced setting in `data/rdp-profiles.json` beside the launcher. Passwords are never
saved; loading a profile clears the password field. Ctrl+S asks for a profile name,
suggesting the computer name if you leave it blank. Saving an existing profile
name updates it; saving another name creates a separate profile. In the profile
browser, `/` searches, Enter loads, and Backspace opens a deletion confirmation.

Diagnostics retain the latest 250 events in memory. Open them with Ctrl+L and
scroll with arrows or Page Up/Page Down. Ctrl+E exports the redacted events to a
timestamped `data/rdp-diagnostics-*.log` file. Passwords and URLs are redacted;
logs may still include computer names, usernames, IP addresses, and local paths.
FreeRDP warnings and errors are captured, and a running FreeRDP process is
reported separately from a successfully authenticated desktop session.

The layout adapts to terminal size, with a saved-computer sidebar on wide
terminals and a separate profile browser on smaller ones. The minimum supported
size is 44 columns by 18 rows; 80 by 24 or larger is recommended. Colors adapt
to light and dark terminal backgrounds.

Press `Ctrl+Alt+Enter` to leave a fullscreen FreeRDP session. `Alt+Tab` returns
to the launcher, where Esc disconnects the session.

## Build

Requirements:

- Windows x64
- Go 1.26 or newer

Build PortableTailscale:

```powershell
go build -tags=ts_omit_logtail -trimpath -ldflags="-s -w" -o PortableTailscale.exe .
```

Build the optional RDP example:

```powershell
go build -trimpath -ldflags="-s -w" -o PortableTailscaleRDP.exe .\cmd\tui
```

Run launcher checks on Windows:

```powershell
go test ./cmd/tui
go vet ./cmd/tui
.\PortableTailscaleRDP.exe --smoke-test
```

The RDP example also needs a Windows x64 build of `sdl-freerdp.exe`, available
from the
[FreeRDP nightly CI](https://ci.freerdp.com/job/freerdp-nightly-windows/lastSuccessfulBuild/arch%3Dwin64%2Clabel%3Dvs2017/artifact/install/bin/sdl-freerdp.exe)
([FreeRDP project](https://github.com/FreeRDP/FreeRDP)).

## Files

```text
PortableTailscale.exe
data/
  tailscaled.state

# Optional RDP example
PortableTailscaleRDP.exe
freerdp/
  sdl-freerdp.exe
```

The `data` folder is created automatically and contains the Tailscale device
identity. Keep it private. If it is lost or copied, revoke the device from the
Tailscale admin console.

## Options

```text
--state-dir PATH  Store the Tailscale identity in PATH
--hostname NAME   Tailnet device name (default: portable-tailscale)
--json            Write newline-delimited status events
--no-browser      Print the sign-in URL without opening a browser
```

## Acknowledgements

PortableTailscale is built on the following open-source projects:

| Project | Used for | License |
| --- | --- | --- |
| [Tailscale `tsnet`](https://pkg.go.dev/tailscale.com/tsnet) ([source](https://github.com/tailscale/tailscale/tree/main/tsnet)) | Userspace Tailscale node and tailnet networking (`tailscale.com` v1.102.5) | BSD 3-Clause, see [licenses/LICENSE.Tailscale.txt](licenses/LICENSE.Tailscale.txt) |
| [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss) by Charm | Terminal UI of the RDP example | MIT, see [licenses/LICENSE.Charmbracelet.txt](licenses/LICENSE.Charmbracelet.txt) |
| [FreeRDP](https://github.com/FreeRDP/FreeRDP) `sdl-freerdp.exe` | RDP client started by the RDP example ([nightly build](https://ci.freerdp.com/job/freerdp-nightly-windows/)) | Apache 2.0, see [freerdp/LICENSE.FreeRDP.txt](freerdp/LICENSE.FreeRDP.txt) and [FREERDP.md](FREERDP.md) |

Tailscale is a trademark of Tailscale Inc. This project is not affiliated with
or endorsed by Tailscale Inc., Charmbracelet, Inc., or the FreeRDP project.
