param(
    [string]$Version = $(if ($env:PIER_VERSION) { $env:PIER_VERSION } else { "latest" }),
    [string]$InstallDir = $(if ($env:PIER_INSTALL_DIR) { $env:PIER_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\Pier\bin" })
)

$ErrorActionPreference = "Stop"
$repo = "Bethel-nz/pier"
$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()

switch ($architecture) {
    "X64" { $arch = "amd64" }
    "Arm64" { $arch = "arm64" }
    default { throw "Pier does not have a Windows release for architecture $architecture." }
}

$asset = "pier_windows_${arch}.zip"
if ($env:PIER_DOWNLOAD_BASE_URL) {
    $baseUrl = ($env:PIER_DOWNLOAD_BASE_URL).TrimEnd("/")
} elseif ($Version -eq "latest") {
    $baseUrl = "https://github.com/$repo/releases/latest/download"
} else {
    if (-not $Version.StartsWith("v")) { $Version = "v$Version" }
    $baseUrl = "https://github.com/$repo/releases/download/$Version"
}

$temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("pier-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temporary | Out-Null

try {
    $archive = Join-Path $temporary $asset
    $checksums = Join-Path $temporary "checksums.txt"
    Invoke-WebRequest "$baseUrl/$asset" -OutFile $archive
    Invoke-WebRequest "$baseUrl/checksums.txt" -OutFile $checksums

    $checksumLine = Get-Content $checksums | Where-Object { $_ -match "\s\*?$([regex]::Escape($asset))$" } | Select-Object -First 1
    if (-not $checksumLine) { throw "Pier could not find $asset in checksums.txt." }
    $expected = ($checksumLine -split "\s+")[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "Pier checksum verification failed for $asset." }

    $expanded = Join-Path $temporary "expanded"
    Expand-Archive -Path $archive -DestinationPath $expanded
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Force (Join-Path $expanded "pier.exe") (Join-Path $InstallDir "pier.exe")

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $pathEntries = @($userPath -split ";" | Where-Object { $_ })
    if ($pathEntries -notcontains $InstallDir) {
        $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        $env:Path = "$env:Path;$InstallDir"
        Write-Host "Added $InstallDir to your user PATH."
    }

    Write-Host "Pier installed to $(Join-Path $InstallDir 'pier.exe')"
} finally {
    Remove-Item -Recurse -Force $temporary -ErrorAction SilentlyContinue
}
