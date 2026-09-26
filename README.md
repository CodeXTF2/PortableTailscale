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
   (see [FREERDP.md](FREERDP.md) for the tested version and checksum).
3. Open `PortableTailscaleRDP.exe`.
4. Enter the tailnet computer, Windows username, optional domain, and password.
5. Move to **Connect** and press Enter.

### RDP controls

| Key | Action |
| --- | --- |
| Up / Down | Move between settings |
| Tab / Shift+Tab | Move between settings |
| Enter | Move to the next setting; connect from the Connect row |
| Space | Toggle the selected checkbox |
| Esc | Disconnect, or exit when disconnected |
| Ctrl+C | Exit |

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

The RDP example also needs a Windows x64 build of `sdl-freerdp.exe`, available
from the
[FreeRDP nightly CI](https://ci.freerdp.com/job/freerdp-nightly-windows/lastSuccessfulBuild/arch%3Dwin64%2Clabel%3Dvs2017/artifact/install/bin/sdl-freerdp.exe)
([FreeRDP project](https://github.com/FreeRDP/FreeRDP)). See
[FREERDP.md](FREERDP.md) for the version and SHA-256 used during development.

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
