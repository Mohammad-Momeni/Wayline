# Wayline

A desktop and Android client for Fortinet-compatible SSL VPNs, local application proxies and ordered Xray/proxy chains.

**0.5.0 is a pre-release.** Windows, Android ARM64/ARMv7 and Ubuntu/Debian builds share the Go protocol engine. Phone runtime behavior and Linux routing still need hardware testing.

## Features

- Saved VPN profiles with OS-backed credential storage.
- Local SOCKS5/HTTP proxy mode and device VPN mode.
- Ordered Before/After hops, each with an enable checkbox.
- A named Xray library with selection, editing and deletion. Add saved configurations as Before or After hops and move them within the chain.
- VMess, VLESS, Trojan and Shadowsocks share-link imports, JSON files and clipboard import.
- Optional reconnect with exponential backoff. Manual disconnect cancels retries. Rejected credentials and certificates require user action.
- Connection, Chain, Xrays and Logs pages, collapsible advanced settings, and clear/copy log controls.
- Named configuration editors, profile rename, and preserved share-link names.
- Certificate approval for the current connection, or an optional saved fingerprint for the profile.
- Per-app VPN routing: all apps, selected apps only, or selected exclusions, with searchable app pickers.
- Dark/light appearance, Windows startup integration, and Linux GUI/CLI operation.
- Android ARM64 and 32-bit ARMv7 packages for Android 8.0 or newer.

Multiple enabled Xray configurations run in the displayed chain order. DNS uses the raw VPN path. Proxy chains carry TCP. The Fortinet transport carries IPv4. VPN mode rejects UDP 443 to encourage video clients to use TCP.

## Start

Create a connection and enter your gateway, port and username. All connection fields start blank except standard ports. Start with Proxy mode, then point an application at SOCKS5 or HTTP on `127.0.0.1:1080`. Use remote DNS with SOCKS.

Windows requires the .NET 8 Desktop Runtime. Extract the full Windows ZIP before starting Wayline.exe.

For device VPN mode, Windows requires elevation, Android requests VPN permission, and Linux uses a privileged sing-box helper. If the gateway certificate needs a trusted fingerprint, verify it independently before saving it.

Import an Xray configuration into the library and give it a recognizable name. Its checkbox enables or disables its existing chain references. The Before/After actions add it to the corresponding chain. Each hop has its own checkbox and ordering controls.

Choose the app-routing policy on the Connection page. Android selects installed packages by name and icon. Desktop builds select executable paths. Desktop DNS is shared by the system and continues to use the VPN; data from excluded apps goes directly. Helpers and local proxy providers may require their own routing entries.

Enable Auto reconnect before connecting. Android holds an active-session wake lock and monitors network changes. The checkbox recovers interrupted sessions while the app service is running.

The Windows **Start with Windows** checkbox registers this executable for the current user and opens it minimized at login. Enable it after moving the application to its permanent location.

## Linux

```sh
sudo apt install ./wayline_0.5.0_amd64.deb
wayline --gui
wayline configure Work
wayline connect Work --remember
wayline connect Work --mode vpn
wayline xrays Work
wayline xray Work 1 off
wayline xray Work 1 after
wayline import-xray Work --stdin --label "Exit"
```

For a headless host, install the DEB with `--no-install-recommends`. Python Tk and Secret Service integration are optional. VPN mode needs `/dev/net/tun` and sudo access. The executable is `/usr/bin/wayline`.

## Profiles

Windows stores metadata under `%LOCALAPPDATA%/Routeweave` and remembered passwords in Credential Manager. Android uses private application storage and Android Keystore. Linux uses `$XDG_CONFIG_HOME/routeweave` or `~/.config/routeweave`, with optional Secret Service password storage.

The Android application ID and private storage identifiers remain stable across name changes, so upgrades retain profiles and credentials. Profile exports omit passwords, local Xray configuration files and device-specific app selections. Exports can include gateway addresses and usernames, so keep them private.

## Build

See [BUILDING.md](BUILDING.md). Android builds use command-line tools. The source includes pinned Go dependencies and the corresponding sing-box/Xray source archives. Generated binaries, local profiles and signing keys are excluded from Git.

The Windows client targets .NET 8. The shared engine uses Go 1.27.3. The native Android ARMv7 build additionally uses an Android NDK compiler.

## Compatibility

SSL VPN over TLS and PPP with password authentication. IPsec, prompted MFA, SAML, client certificates and endpoint posture workflows need a compatible client. Wayline has no persistent kill switch.

## License

GPL-3.0. Retained upstream source notices and binary components are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
