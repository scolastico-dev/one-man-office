[CmdletBinding()]
param(
    [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"
$Repository = "scolastico-dev/one-man-office"
$Binary = "omo"

if ($env:OS -ne "Windows_NT") {
    throw "This installer only supports Windows. Use install.sh on Linux or macOS."
}

if (-not $InstallDir) {
    if ($env:OMO_INSTALL_DIR) {
        $InstallDir = $env:OMO_INSTALL_DIR
    } else {
        $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\omo"
    }
}

$WindowsArchitecture = if ($env:PROCESSOR_ARCHITEW6432) {
    $env:PROCESSOR_ARCHITEW6432
} else {
    $env:PROCESSOR_ARCHITECTURE
}
$Architecture = switch ($WindowsArchitecture) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported Windows architecture: $WindowsArchitecture" }
}

$Headers = @{ "User-Agent" = "omo-installer" }
$Release = Invoke-RestMethod `
    -Uri "https://api.github.com/repos/$Repository/releases/latest" `
    -Headers $Headers
$Tag = [string]$Release.tag_name
if (-not $Tag) {
    throw "Could not determine the latest release."
}

$Version = $Tag -replace "^v", ""
$Asset = "$Binary-windows-$Architecture.zip"
$ReleaseUrl = "https://github.com/$Repository/releases/download/$Tag"
$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("omo-install-" + [guid]::NewGuid())
$Archive = Join-Path $TempDir $Asset
$Checksums = Join-Path $TempDir "SHA256SUMS"

try {
    New-Item -ItemType Directory -Path $TempDir | Out-Null
    Invoke-WebRequest -Uri "$ReleaseUrl/$Asset" -OutFile $Archive -Headers $Headers
    Invoke-WebRequest -Uri "$ReleaseUrl/SHA256SUMS" -OutFile $Checksums -Headers $Headers

    $ChecksumPattern = "^([A-Fa-f0-9]{64})\s+\*?\./$([regex]::Escape($Asset))$"
    $Expected = Get-Content $Checksums | ForEach-Object {
        if ($_ -match $ChecksumPattern) { [string]$Matches[1] }
    } | Select-Object -First 1
    if (-not $Expected) {
        throw "Release checksum for $Asset was not found."
    }

    $Actual = [string]((Get-FileHash -Algorithm SHA256 -Path $Archive).Hash)
    if (-not $Actual) {
        throw "Could not calculate the checksum for $Asset."
    }
    if ($Actual -ine $Expected) {
        throw "Checksum verification failed for $Asset."
    }

    Expand-Archive -Path $Archive -DestinationPath $TempDir -Force
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $Target = Join-Path $InstallDir "$Binary.exe"

    $InstalledVersion = ""
    if (Test-Path $Target) {
        $InstalledOutput = & $Target --version 2>$null
        if ($InstalledOutput -match "([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)") {
            $InstalledVersion = $Matches[1]
        }
    }

    if ($InstalledVersion -eq $Version) {
        Write-Host "$Binary $Tag is already installed at $Target"
    } else {
        Copy-Item -Force -Path (Join-Path $TempDir "$Binary.exe") -Destination $Target
        if ($InstalledVersion) {
            Write-Host "Updated $Binary from $InstalledVersion to $Version at $Target"
        } else {
            Write-Host "Installed $Binary $Version at $Target"
        }
    }

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $PathEntries = @($UserPath -split ";" | Where-Object { $_ })
    $AlreadyOnPath = $PathEntries | Where-Object {
        ($_ -replace "\\+$", "") -ieq ($InstallDir -replace "\\+$", "")
    }
    if (-not $AlreadyOnPath) {
        $NewUserPath = if ($UserPath) { "$UserPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("Path", $NewUserPath, "User")
        Write-Host "Added $InstallDir to your user PATH."
    }
    if (-not (($env:Path -split ";") -contains $InstallDir)) {
        $env:Path = "$env:Path;$InstallDir"
    }
} finally {
    if (Test-Path $TempDir) {
        Remove-Item -Recurse -Force $TempDir
    }
}
