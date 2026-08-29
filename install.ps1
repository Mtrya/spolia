$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$Repository = "Mtrya/spolia"
$LatestUrl = if ($env:SPOLIA_LATEST_URL) { $env:SPOLIA_LATEST_URL } else { "https://github.com/$Repository/releases/latest" }
$InstallDir = if ($env:SPOLIA_BIN_DIR) { $env:SPOLIA_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\spolia\bin" }

switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    "X64" { $Arch = "amd64" }
    "Arm64" { $Arch = "arm64" }
    default { throw "spolia does not provide a Windows archive for this architecture." }
}

if ($env:SPOLIA_INSTALL_VERSION) {
    $Version = $env:SPOLIA_INSTALL_VERSION.TrimStart("v")
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
        throw "Could not determine the latest spolia release from $ResolvedUrl."
    }
    $Version = $Tag.Substring(1)
}

$DownloadRoot = if ($env:SPOLIA_RELEASE_DOWNLOAD_URL) { $env:SPOLIA_RELEASE_DOWNLOAD_URL.TrimEnd("/") } else { "https://github.com/$Repository/releases/download/$Tag" }
$Asset = "spolia_${Version}_windows_${Arch}.zip"
$ArchiveRoot = [System.IO.Path]::GetFileNameWithoutExtension($Asset)
$Temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("spolia-install-" + [guid]::NewGuid().ToString("N"))

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
    Copy-Item -Path (Join-Path $Temporary "$ArchiveRoot\spolia.exe") -Destination (Join-Path $InstallDir "spolia.exe") -Force
} finally {
    if (Test-Path $Temporary) {
        Remove-Item -Path $Temporary -Recurse -Force
    }
}

Write-Output "Installed spolia $Version to $InstallDir\spolia.exe."
if (($env:PATH -split ';') -notcontains $InstallDir) {
    $UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($null -ne $UserPath -and ($UserPath -split ';') -contains $InstallDir) {
        Write-Output "Your user PATH already includes $InstallDir; open a new terminal, then run: spolia setup"
    } else {
        Write-Output "PATH does not include $InstallDir yet. To add it to your user PATH permanently:"
        Write-Output "  [Environment]::SetEnvironmentVariable('PATH', [Environment]::GetEnvironmentVariable('PATH', 'User') + ';$InstallDir', 'User')"
        Write-Output "Then open a new terminal and run: spolia setup"
    }
}
