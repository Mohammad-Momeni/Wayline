$ErrorActionPreference = 'Stop'
$projectRoot = $PSScriptRoot
$buildOutput = Join-Path $projectRoot 'dist'
New-Item -ItemType Directory -Force $buildOutput,(Join-Path $buildOutput 'engine') | Out-Null
Push-Location (Join-Path $projectRoot 'engine')
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Engine tests failed' }
    go build -buildvcs=false -trimpath -ldflags '-s -w' -o (Join-Path $buildOutput 'engine/wayline-engine.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Engine build failed' }
} finally { Pop-Location }
dotnet build (Join-Path $projectRoot 'desktop/Wayline.csproj') -c Release --configfile (Join-Path $projectRoot 'desktop/NuGet.Config') -o $buildOutput
if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
$toolCache = Join-Path $buildOutput 'tools'
New-Item -ItemType Directory -Force $toolCache | Out-Null
$download = Join-Path $toolCache 'sing-box-1.14.4-windows-amd64.zip'
Invoke-WebRequest 'https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-windows-amd64.zip' -OutFile $download
if ((Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant() -ne '99185d75d314a07dbba4a130c8ad986cbeb629a07927975636b341ca61f991df') { throw 'sing-box checksum mismatch' }
$unpack = Join-Path $toolCache 'sing-box-runtime'
Expand-Archive -LiteralPath $download -DestinationPath $unpack -Force
Copy-Item -LiteralPath (Join-Path $unpack 'sing-box-1.14.4-windows-amd64/sing-box.exe'),(Join-Path $unpack 'sing-box-1.14.4-windows-amd64/libcronet.dll') -Destination (Join-Path $buildOutput 'engine')
Copy-Item -LiteralPath (Join-Path $projectRoot 'README.md'),(Join-Path $projectRoot 'LICENSE'),(Join-Path $projectRoot 'THIRD_PARTY_NOTICES.md') -Destination $buildOutput
Copy-Item -LiteralPath (Join-Path $projectRoot 'licenses') -Destination $buildOutput -Recurse -Force
Write-Host "Built $buildOutput. Requires Go 1.27.3+ and .NET 8 SDK."
