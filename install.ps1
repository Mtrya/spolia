$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$Repository = "Mtrya/llmloot"
$LatestUrl = if ($env:LLMLOOT_LATEST_URL) { $env:LLMLOOT_LATEST_URL } else { "https://github.com/$Repository/releases/latest" }
$InstallDir = if ($env:LLMLOOT_BIN_DIR) { $env:LLMLOOT_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\llmloot\bin" }

switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    "X64" { $Arch = "amd64" }
    "Arm64" { $Arch = "arm64" }
    default { throw "llmloot does not provide a Windows archive for this architecture." }
}

if ($env:LLMLOOT_INSTALL_VERSION) {
    $Version = $env:LLMLOOT_INSTALL_VERSION.TrimStart("v")
    $Tag = "v$Version"
} else {
    $Response = Invoke-WebRequest -Uri $LatestUrl -UseBasicParsing
    $RequestMessageProperty = $Response.BaseResponse.PSObject.Properties["RequestMessage"]
    if ($null -ne $RequestMessageProperty) {
        $ResolvedUrl = $Response.BaseResponse.RequestMessage.RequestUri.AbsoluteUri
    } else {
        $ResolvedUrl = $Response.BaseResponse.ResponseUri.AbsoluteUri
    }
    $Tag = ($ResolvedUrl.TrimEnd("/") -split "/")[-1]
    if ($Tag -notmatch '^v[0-9A-Za-z.+-]+$') {
        throw "Could not determine the latest llmloot release from $ResolvedUrl."
    }
    $Version = $Tag.Substring(1)
}

$DownloadRoot = if ($env:LLMLOOT_RELEASE_DOWNLOAD_URL) { $env:LLMLOOT_RELEASE_DOWNLOAD_URL.TrimEnd("/") } else { "https://github.com/$Repository/releases/download/$Tag" }
$Asset = "llmloot_${Version}_windows_${Arch}.zip"
$ArchiveRoot = [System.IO.Path]::GetFileNameWithoutExtension($Asset)
$Temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("llmloot-install-" + [guid]::NewGuid().ToString("N"))

try {
    New-Item -ItemType Directory -Path $Temporary | Out-Null
    $ChecksumsPath = Join-Path $Temporary "SHA256SUMS"
    $ArchivePath = Join-Path $Temporary $Asset
    Invoke-WebRequest -Uri "$DownloadRoot/SHA256SUMS" -OutFile $ChecksumsPath -UseBasicParsing
    Invoke-WebRequest -Uri "$DownloadRoot/$Asset" -OutFile $ArchivePath -UseBasicParsing
    $EscapedAsset = [regex]::Escape($Asset)
    $ChecksumLines = @(Get-Content $ChecksumsPath | Where-Object { $_ -match "^([0-9a-fA-F]{64})  $EscapedAsset$" })
    if ($ChecksumLines.Count -ne 1) {
        throw "No unique valid checksum was published for $Asset."
    }
    [void]($ChecksumLines[0] -match '^([0-9a-fA-F]{64})')
    $Expected = $Matches[1].ToLowerInvariant()
    $Actual = (Get-FileHash -Path $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) {
        throw "Checksum verification failed for $Asset."
    }
    Expand-Archive -Path $ArchivePath -DestinationPath $Temporary
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Copy-Item -Path (Join-Path $Temporary "$ArchiveRoot\llmloot.exe") -Destination (Join-Path $InstallDir "llmloot.exe") -Force
} finally {
    if (Test-Path $Temporary) {
        Remove-Item -Path $Temporary -Recurse -Force
    }
}

Write-Output "Installed llmloot $Version to $InstallDir\llmloot.exe."
if (($env:PATH -split ';') -notcontains $InstallDir) {
    Write-Output "Add $InstallDir to your user PATH, then run: llmloot setup"
}
