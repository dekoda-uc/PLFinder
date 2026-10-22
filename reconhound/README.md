# reconHound

**A fast, concurrent IP range / port / HTTP server scanner with a native desktop window.**

reconHound sweeps CIDR blocks or IP ranges across a list of ports, fingerprints the HTTP server on each open port, checks for specific resources (e.g. `/robots.txt`, `/admin.php`), and lets you view or save exactly what it found — all from a single self-contained binary with its own native window (WebKit on Linux/macOS, WebView2/Edge on Windows). No browser tab, no internet connection required at runtime.

> ⚠️ **Use responsibly.** Only scan networks and hosts you own or are explicitly authorized to test. Scanning infrastructure you don't have permission to probe may be illegal in your jurisdiction.

---

## Features

- **Concurrent scanning** — configurable connection pool (goroutine workers), scans large ranges fast.
- **Flexible range input** — CIDR (`192.168.1.0/24`), explicit ranges (`10.0.0.1-10.0.0.50`), short ranges (`10.0.0.1-50`), or single IPs, one per line.
- **JSON range import** — paste in a path to a BGP/ASN-style range export (`{"ranges":[{"start":"...","end":"..."}]}`) and it's parsed into editable `start - end` lines automatically.
- **HTTP server fingerprinting** — reads the `Server` header on every open port, with substring matching to flag servers you care about.
- **Resource probing** — check for the existence of specific paths (`/hello.txt`, `/admin`, etc.) across every open host, with configurable "OK" status codes.
- **View & save what you found** — click "View resource" to see the actual response body as code (JSON/HTML/TXT, detected automatically) right inside the window; "Save resource" exports the real files (not just metadata) as a `.zip`, for whichever rows you select — only resources that actually returned an OK status get included.
- **Live progress** — current scan target, a progress bar, and running counters (scanned / open / matches), updating in real time over Server-Sent Events.
- **Pause / resume / stop** — without losing results already found.
- **Command-line preloading** — launch with `-ranges`, `-ports`, and friends to open the window with the form already filled in, ready to hit "Start search". See [Command-line flags](#command-line-flags).

## Quick start

Requires Go 1.22+ and, since this app uses a native window (cgo), a C/C++ compiler. See the per-OS instructions below, then:

```bash
go build -o reconhound .      # reconhound.exe on Windows
./reconhound
```

---

## Building from source

<a name="linux"></a>
### Linux

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
go build -o reconhound .
```

> On distros that only ship `webkit2gtk-4.1` and not `webkit2gtk-4.0` (Ubuntu 24.04+, Debian 13+, recent Fedora), the `webview_go` library still asks for the `4.0` pkg-config name. Run the included helper script to create a compatibility shim pointing at your real `4.1` libraries:
>
> ```bash
> ./linux-pkgconfig-shim.sh
> PKG_CONFIG_PATH=/tmp/pkgconfig-shim CGO_ENABLED=1 go build -o reconhound .
> ```
>
> If `pkg-config --exists webkit2gtk-4.0` already succeeds on your system, skip this — just `go build` directly.

<a name="windows"></a>
### Windows

Install Go and a C/C++ compiler — either:

- [MSYS2](https://www.msys2.org/) → `pacman -S mingw-w64-ucrt-x86_64-gcc` (recommended), or
- [TDM-GCC](https://jmeubank.github.io/tdm-gcc/)

Make sure the compiler's `bin` folder is on your `PATH`, then from PowerShell/cmd in the project folder:

```powershell
$env:CGO_ENABLED=1
go build -ldflags "-H=windowsgui" -o reconhound.exe .
```

`-H=windowsgui` is important — without it, Windows also opens a console window behind the app. If you see `build constraints exclude all Go files` for `webview_go`, it means CGO is disabled (`go env CGO_ENABLED` returns `0`); the `$env:CGO_ENABLED=1` line above fixes that for the current shell, or run `go env -w CGO_ENABLED=1` to make it permanent.

<a name="macos"></a>
### macOS

Only Xcode's command line tools are needed — Apple's WebKit framework is already available, nothing extra to install:

```bash
xcode-select --install   # if you don't have them yet
go build -o reconhound .
```

---

## Using the app

1. **Ranges** — one per line: CIDR, `start-end`, `start-lastOctet`, or a single IP.
2. **Ports** — comma/range list, e.g. `80,443,8000-8010`.
3. **Connections** — concurrent workers (400 is a reasonable default).
4. **Server match** — substring to look for in the `Server` header (case-insensitive) → matching rows get tagged **MATCH**.
5. **Resources to check** — comma-separated paths, e.g. `/hello.txt,/admin.php`. Each one checked shows its status code; if it's an "OK" code, a **View resource** button appears to inspect the real content, and the row gets tagged **RESOURCE**.
6. **Start search / Pause / Stop** — the live indicator and progress bar show scanned/total and the current target.
7. **All IPs / Selected** + **Save CSV...** — export the results table.
8. **Save resource** — download the real content of found resources (zip), filtered to the current selection.
9. **Copy selected** — copies `scheme://ip:port` URLs of the chosen rows to the clipboard.

Row highlighting: a soft green means either the server matched **or** a resource was found (one of the two); a strong green means **both** matched on the same host.

## Command-line flags

Launch with any of these to preload the form (the scan is never started automatically — you still click "Start search"):

| Flag | Description |
|---|---|
| `-ranges` | Path to a file, or the value directly. Accepts plain text (same format as the Ranges box) or JSON: `{"ranges":[{"start":"1.2.3.0","end":"1.2.3.255","cidr":"1.2.3.0/24"}]}`. Detected automatically from content, regardless of file extension. Each entry becomes an editable `start - end` line (falls back to `cidr` if an entry has no start/end). |
| `-ports` | Path to a file, or the value directly, e.g. `-ports 80,443,8000-8010` |
| `-connections` | Connections to preload (0 = keep the form default) |
| `-timeout` | Timeout in seconds to preload (0 = keep the default) |
| `-scheme` | `http`, `https`, or `both` |
| `-match` | Server match string to preload |
| `-resources` | Path to a file, or the value directly, e.g. `-resources /hello.txt,/admin.php` |
| `-okcodes` | OK status codes to preload, e.g. `200,204` |
| `-tcponly` | Preloads "TCP only" checked |
| `-addr` | Fixed address for the internal server (e.g. `127.0.0.1:8787`); defaults to an auto-picked free port |
| `-debug` | Opens WebKit's dev tools inside the window |

```bash
./reconhound -ranges bgp_export.json -ports 80,443,8080 -connections 300 -timeout 5 -match nginx -resources /hello.txt,/hello.html
```

## How it works

reconHound runs a small HTTP server bound to `127.0.0.1` only (never reachable from the network) and loads its UI into a native WebKit/WebView2 window pointed at that local address. The Go backend does the actual scanning — a worker pool of goroutines dials each `ip:port`, and for open ports optionally fetches the `/` response (for the `Server` header) and each configured resource path, caching the response body (up to 1 MiB per resource) so "View resource" and "Save resource" don't need to hit the target again. Results stream to the window live via Server-Sent Events.

## License

This project is licensed under the MIT License. See the `LICENSE` file for more information.
