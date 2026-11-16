# Building Wayline

## Engine and Windows desktop

Install Go 1.27.3 and the .NET 8 SDK.

```powershell
go -C engine test -mod=vendor ./...
go -C engine build -mod=vendor -buildvcs=false -trimpath -ldflags "-s -w" -o ../dist/windows/engine/wayline-engine.exe .
dotnet build desktop/Wayline.csproj -c Release --configfile desktop/NuGet.Config -o dist/windows
```

VPN mode uses the unmodified sing-box 1.14.4 executable beside the engine. Preserve its license and corresponding source archive. Use the archive hashes pinned in the Linux packager and the release dependency manifests.

## Android

Use a JDK, Python 3, Go, Android API 36's android.jar, Build Tools 36.0.0 and the pinned Xray v26.11.9 Android core. Android Studio and Gradle are unnecessary.

The tool cache layout accepted by the build script is `platform/android-36/android.jar` and `build-tools/android-16`. Standard SDK layouts are also accepted when available.

```powershell
./android/build.ps1 -ToolRoot PATH_TO_ANDROID_TOOLS -XrayArchive PATH_TO_XRAY_ANDROID_ARM64_ZIP -SigningKey PATH_TO_PRIVATE_KEYSTORE
```

The ARM64 archive SHA-256 is pinned in the build script. For ARMv7, build the engine and Xray core from their pinned source with an NDK compiler targeting `armv7a-linux-androideabi26`. Pass `-Architecture armeabi-v7a -Ndk PATH_TO_LLVM_PREBUILT -XrayCore PATH_TO_ARMV7_XRAY -XrayLicense PATH_TO_XRAY_LICENSE`.

For the ARMv7 Xray build, extract `third_party/xray-core-v26.11.9.tar.gz`, use its module dependencies, and run the following from that source directory with Go 1.27.3. Set CC to the NDK LLVM compiler with `--target=armv7a-linux-androideabi26 -mfpu=vfpv3-d16 --sysroot=PATH_TO_NDK_SYSROOT`.

```sh
GOOS=android GOARCH=arm GOARM=7 CGO_ENABLED=1 go build -buildvcs=false -trimpath -ldflags '-s -w -checklinkname=0 -extldflags=-Wl,-z,max-page-size=16384' -o xray-armv7 ./main
```

`-checklinkname=0` is required by the pinned upstream anet v0.0.5 Android interface implementation, which accesses Go's interface-zone cache. This dependency uses private runtime details; a Go toolchain upgrade needs a new compatibility review. The ARMv7 binaries have been statically validated but need hardware testing, including on older CPUs.

Both native binaries must be Android PIE files for the selected ABI. The APK build validates them and writes a versioned APK and a release.json identity record. It compiles Java directly, creates DEX, packages resources, aligns and signs the APK, then verifies signature and alignment.

Set `WAYLINE_SIGNING_PASSWORD` before signing with a private release keystore. If omitted, the script creates a local development key with a development password. Store release keys outside the repository.

## Linux

Cross-compile the engine with `GOOS=linux`, `CGO_ENABLED=0` and `GOARCH=amd64` or `arm64`, using filenames `wayline-engine-amd64` and `wayline-engine-arm64`. Download the official sing-box 1.14.4 Linux release archives, then run:

```sh
python linux/package.py --build-dir PATH_TO_ENGINES --downloads PATH_TO_ARCHIVES --output dist
```

The packager verifies archive hashes and executable architectures and creates Ubuntu/Debian packages.

## Release packaging

Build the Windows desktop into `desktop/final-build`, the Windows/Linux engines into `dist/engines`, and both APKs with `android/build.ps1`. Build the DEBs into `dist/release`, then run:

```sh
python scripts/package_release.py --windows-runtime PATH_TO_VERIFIED_SINGBOX_WINDOWS_DIRECTORY
```

The packager verifies the APK build records and packages source using Git's nonignored file list. Keep its generated `dist/release` directory out of Git. Publish the corresponding source ZIP with the binary downloads and preserve licenses.
