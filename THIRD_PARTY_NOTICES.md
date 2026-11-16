# Third-party components

Wayline is an independent VPN and proxy client, unaffiliated with Fortinet or the upstream projects.

- Fortix, GPL-3.0: selected SSL-VPN HTTP/TLS framing, XML parsing and PPP source, with tests, from commit f20bc127240b4e804e96facd32325a5579ab4283. https://github.com/avhn/fortix . Modified transport export and package paths; retained protocol behavior. Full GPL text in LICENSE.
- go-ocproxy, MIT: netstack source from commit c79c0fad1f62594b60023194bea18de68791d1ae. https://github.com/awkj/go-ocproxy . License in licenses/go-ocproxy-MIT.txt.
- gVisor, Apache-2.0: userspace network stack, version pinned in engine/go.mod and go.sum. https://github.com/google/gvisor . Source and notices included in engine/vendor.
- Go x/sys, x/exp, x/time and google/btree: pinned dependencies, source licenses in engine/vendor.
- sing-box 1.14.4, GPL-3.0-or-later with upstream name restriction: unmodified official Windows executable and libcronet.dll used for TUN mode. https://github.com/SagerNet/sing-box/releases/tag/v1.14.4 . Upstream source archive included in third_party. See licenses/sing-box-NOTICE.txt, upstream source go.mod/go.sum and licensing files for dependencies and embedded driver notices.
- Linux VPN mode uses the official sing-box 1.14.4 Linux executable. The same corresponding source archive is in the Wayline source bundle under third_party.
- Android bundles Xray-core v26.11.9 under MPL 2.0: the official ARM64 executable, or an ARMv7 executable built from the pinned source archive in third_party. Both APKs include its license and source reference. See BUILDING.md for the ARMv7 build flags and dependency notes. Corresponding source: https://github.com/XTLS/Xray-core/tree/v26.11.9 .

Wayline's adapted engine is distributed under GPL-3.0. The accompanying desktop source and build scripts are supplied under GPL-3.0 too. No Fortinet binaries, extracted secrets or company password are included.
