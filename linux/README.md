# Wayline for Ubuntu and Debian

Install the DEB matching your architecture:

```sh
sudo apt install ./wayline_0.5.0_amd64.deb
# Use wayline_0.5.0_arm64.deb for ARM64.
wayline --gui
```

The optional GUI uses python3-tk. For a headless server, install with `--no-install-recommends` and run `wayline` for the terminal menu, or configure directly:

```sh
wayline configure Work
wayline connect Work --remember
wayline connect Work --mode vpn
```

Proxy mode exposes SOCKS5 and HTTP at 127.0.0.1:1080 by default. Enable remote DNS in SOCKS clients. VPN mode requests sudo for the bundled sing-box TUN helper and requires /dev/net/tun. The client and managed Xray processes run as your regular user. Ctrl+C disconnects. The optional `--remember` flag stores the VPN password using Secret Service through secret-tool; `forget-password` removes it. For unattended use, `--password-stdin` accepts the VPN password followed by authenticated proxy passwords. VPN mode then needs noninteractive sudo access.

## Profiles and views

The desktop has Connection, Chain, Xrays and Logs tabs. Advanced connection fields are collapsed. Rename updates a profile while retaining its ID and credentials. Logs can be copied or cleared separately from connection settings.

`wayline import FILE --name Work` imports a portable profile. `wayline export Work` writes one without passwords, Xray files or device-specific app selections. Exports can still contain gateway addresses and usernames.

Profiles use $XDG_CONFIG_HOME/routeweave or ~/.config/routeweave, a stable storage identifier retained for upgrades.

## Xray and proxy chains

Each Before or After hop has an enable checkbox and runs in displayed order. The Xray library preserves imported names and supports settings changes, deletion and placement in either chain. Settings includes endpoint and credential fields plus full JSON editing. Install a trusted Xray core on PATH or specify its executable in the saved entry.

```sh
wayline import-xray Work --stdin --label "Exit"
wayline import-xray Work --clipboard
wayline xrays Work
wayline xray Work 1 off
wayline xray Work 1 after
wayline hops Work
wayline hop Work after 1 off
```

JSON and VMess, VLESS, Trojan and Shadowsocks share links are supported. Clipboard import needs wl-clipboard, xclip or xsel. Subscription URLs are not imported. Each managed outbound traverses the preceding chain. General UDP is unavailable through proxy chains; device DNS uses the raw VPN.

A running local SOCKS5 exit must be the first After hop in VPN mode and run under your user. Its executable is routed through the raw VPN to avoid a loop. Use a remote proxy or managed Xray for entry hops in VPN mode.

## Per-app VPN routing

Choose all apps, only selected apps, or selected exclusions. The GUI picker searches running executable paths and can browse for programs. Include helper processes that open network connections. Shared system DNS still uses the VPN.

```sh
wayline apps Work
wayline app-filter Work include /usr/lib/firefox/firefox
wayline app-filter Work all
```

The GUI certificate prompt has Proceed and an optional Remember checkbox. Unchecked approval lasts for the current connection; remembered fingerprints belong to that profile. Independently verify fingerprints before approval. Auto reconnect retries transient failures with bounded backoff; rejected authentication and certificates stop for correction.

## Build

See BUILDING.md in the source repository. Cross-compile the Go engines for Linux amd64 and arm64, then use linux/package.py with the pinned official sing-box archives. The packager verifies their hashes and executable targets.
