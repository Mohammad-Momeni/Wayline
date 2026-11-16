"""Build Ubuntu/Debian packages from cross-compiled Go engines and verified helpers."""

import argparse
import hashlib
import io
from pathlib import Path
import struct
import tarfile


VERSION = "0.5.0"
ARCHES = {
    "amd64": (62, "699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968"),
    "arm64": (183, "436441f76ee2f7fe21b5b2d426d3cb261a480455a704865789ad533e4273ec26"),
}
SOURCE = Path(__file__).resolve().parent.parent


def elf(data, machine):
    if data[:6] != b"\x7fELF\x02\x01" or struct.unpack_from("<H", data, 18)[0] != machine:
        raise ValueError("Unexpected ELF target architecture.")
    return data


def tar(entries):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz", format=tarfile.USTAR_FORMAT) as archive:
        for path, data, mode in entries:
            info = tarfile.TarInfo("./" + path)
            info.size, info.mode = len(data), mode
            info.uname = info.gname = "root"
            archive.addfile(info, io.BytesIO(data))
    return buffer.getvalue()


def ar_member(name, data):
    header = f"{name:<16}{0:<12}{0:<6}{0:<6}{'100644':<8}{len(data):<10}`\n".encode("ascii")
    return header + data + (b"\n" if len(data) % 2 else b"")


def build(arch, build_dir, downloads, output):
    machine, digest = ARCHES[arch]
    release = f"sing-box-1.14.4-linux-{arch}"
    path = downloads / f"{release}.tar.gz"
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise ValueError(f"Invalid digest for {path.name}.")
    with tarfile.open(path, "r:gz") as archive:
        helper = elf(archive.extractfile(f"{release}/sing-box").read(), machine)
        license_text = archive.extractfile(f"{release}/LICENSE").read()
    engine = elf((build_dir / f"wayline-engine-{arch}").read_bytes(), machine)
    doc = "usr/share/doc/wayline/"
    entries = [
        ("usr/bin/wayline", (SOURCE / "linux/wayline").read_bytes(), 0o755),
        ("usr/lib/wayline/wayline-engine", engine, 0o755),
        ("usr/lib/wayline/sing-box", helper, 0o755),
        ("usr/lib/wayline/askpass", (SOURCE / "linux/askpass").read_bytes(), 0o755),
        ("usr/lib/wayline/wayline_gui.py", (SOURCE / "linux/wayline_gui.py").read_bytes(), 0o644),
        ("usr/lib/wayline/wayline.png", (SOURCE / "assets/wayline.png").read_bytes(), 0o644),
        ("usr/share/icons/hicolor/scalable/apps/wayline.svg", (SOURCE / "assets/wayline.svg").read_bytes(), 0o644),
        ("usr/share/applications/wayline.desktop", (SOURCE / "linux/wayline.desktop").read_bytes(), 0o644),
        (doc + "README.md", (SOURCE / "linux/README.md").read_bytes(), 0o644),
        (doc + "copyright", (SOURCE / "THIRD_PARTY_NOTICES.md").read_bytes(), 0o644),
        (doc + "GPL-3.0.txt", (SOURCE / "LICENSE").read_bytes(), 0o644),
        (doc + "sing-box-LICENSE.txt", license_text, 0o644),
    ]
    size = (sum(len(data) for _, data, _ in entries) + 1023) // 1024
    control = f"""Package: wayline
Version: {VERSION}
Architecture: {arch}
Section: net
Priority: optional
Maintainer: Wayline local build <wayline@localhost>
Installed-Size: {size}
Depends: python3 (>= 3.9), libc6, sudo
Recommends: python3-tk, libsecret-tools
Description: Fortinet SSL-VPN client with proxy and VPN modes
 Wayline stores connection profiles, supports SOCKS5 and HTTP proxy
 chains, and can run a selected Xray outbound over the VPN tunnel.
 Use the terminal client or start the optional graphical profile editor.
"""
    artifact = output / f"wayline_{VERSION}_{arch}.deb"
    temporary = artifact.with_suffix(".tmp")
    with temporary.open("wb") as package:
        package.write(b"!<arch>\n")
        package.write(ar_member("debian-binary", b"2.0\n"))
        package.write(ar_member("control.tar.gz", tar([("control", control.encode(), 0o644)])))
        package.write(ar_member("data.tar.gz", tar(entries)))
    temporary.replace(artifact)
    print(artifact.name, artifact.stat().st_size)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", required=True, type=Path)
    parser.add_argument("--downloads", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    for architecture in ARCHES:
        build(architecture, args.build_dir, args.downloads, args.output)
