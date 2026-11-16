"""Package verified Wayline builds without profiles, local keys or build caches."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = '0.5.0'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def package(output, runtime):
    output.mkdir(parents=True, exist_ok=True)
    windows = output / f'Wayline-{VERSION}-Windows'
    windows.mkdir(exist_ok=True)
    (windows / 'engine').mkdir(exist_ok=True)
    for name in ['Wayline.exe', 'Wayline.dll', 'Wayline.deps.json', 'Wayline.runtimeconfig.json']:
        shutil.copyfile(ROOT / 'desktop/final-build' / name, windows / name)
    shutil.copyfile(ROOT / 'dist/engines/wayline-engine.exe', windows / 'engine/wayline-engine.exe')
    for name in ['sing-box.exe', 'libcronet.dll']:
        shutil.copyfile(runtime / name, windows / 'engine' / name)
    for name in ['README.md', 'BUILDING.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'CHANGELOG.md']:
        shutil.copyfile(ROOT / name, windows / name)
    shutil.copytree(ROOT / 'licenses', windows / 'licenses', dirs_exist_ok=True)
    # Only named files are included; never enumerate a potentially used Data folder.
    files = [windows / name for name in ['Wayline.exe', 'Wayline.dll', 'Wayline.deps.json', 'Wayline.runtimeconfig.json', 'README.md', 'BUILDING.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'CHANGELOG.md']]
    files += list((windows / 'licenses').glob('*.txt'))
    files += [windows / 'engine' / name for name in ['wayline-engine.exe', 'sing-box.exe', 'libcronet.dll']]
    with zipfile.ZipFile(output / f'Wayline-{VERSION}-Windows.zip', 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        for file in files:
            archive.write(file, Path(windows.name) / file.relative_to(windows))
    records = []
    for arch in ['arm64-v8a', 'armeabi-v7a']:
        build = ROOT / 'android/build' / arch
        record = json.loads((build / 'release.json').read_text(encoding='utf-8-sig'))
        apk = build / record['apk']
        if record['version'] != VERSION or record['versionCode'] != 6 or digest(apk) != record['sha256']:
            raise ValueError('APK identity mismatch: ' + arch)
        shutil.copyfile(apk, output / apk.name)
        records.append(record)
    # A Git-aware source list follows .gitignore, including uncommitted source.
    names = subprocess.check_output(['git', '-c', f'safe.directory={ROOT.as_posix()}', '-C', str(ROOT), 'ls-files', '--cached', '--others', '--exclude-standard', '-z']).decode().split('\0')
    with zipfile.ZipFile(output / f'Wayline-{VERSION}-source.zip', 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        for name in sorted(set(filter(None, names))):
            file = ROOT / name
            if file.is_file():
                archive.write(file, Path(f'Wayline-{VERSION}-source') / name)
    (output / 'android-release.json').write_text(json.dumps(records, indent=2)+'\n', encoding='utf-8')
    for folder in ['android', 'linux']:
        shutil.copyfile(ROOT / folder / 'README.md', output / f'{folder}-README.md')
    for name in ['README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md']:
        shutil.copyfile(ROOT / name, output / name)
    artifacts = sorted(file for file in output.iterdir() if file.is_file() and file.suffix in {'.zip', '.apk', '.deb'})
    (output / 'SHA256SUMS.txt').write_text(''.join(digest(file)+'  '+file.name+'\n' for file in artifacts), encoding='utf-8')
    for file in artifacts:
        print(file.name, file.stat().st_size)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=ROOT/'dist/release')
    parser.add_argument('--windows-runtime', type=Path, required=True, help='verified sing-box 1.14.4 Windows runtime directory')
    args = parser.parse_args()
    package(args.output.resolve(), args.windows_runtime.resolve())
