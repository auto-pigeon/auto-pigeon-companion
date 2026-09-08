# Auto-Pigeon Companion — the native operator acceptance kit, PowerShell half.
#
# Run this on the machine the artifact is for. It produces a small result
# bundle you can send back; nothing is uploaded from here.
#
#   .\run-acceptance.ps1
#   .\run-acceptance.ps1 -ToolPath C:\ericw-tools -Out C:\Users\me\aucom-bundle
#   .\run-acceptance.ps1 -Engine C:\Quake\ironwail.exe -GameRoot C:\Quake
#
# Windows may refuse to run a downloaded script. Either unblock this file
# (Unblock-File .\run-acceptance.ps1) or run it once with:
#
#   powershell -ExecutionPolicy Bypass -File .\run-acceptance.ps1
#
# # What this script is for, and what it deliberately is not
#
# The lanes are in the program, once, so a Windows run and a Linux run are the
# same run. Two hand-written harnesses in two shell languages would be two
# contracts that agree until the day they do not — and only one of them would
# ever be executed by the person maintaining them.
#
# What is HERE is the half a program cannot do for itself: verify the
# artifact's checksums BEFORE starting it, and read what the operating system
# calls this machine — so an amd64 binary under WOW64 or a Prism-emulated x64
# binary on an arm64 device is not reported as native evidence for the
# architecture it is emulating.
#
# # It never touches your game data
#
# -GameRoot is passed through and nothing else. No lane copies, archives,
# hashes wholesale or uploads a byte of it, and there is no parameter here that
# makes one.
[CmdletBinding()]
param(
    # --out
    [string]$Out = "",
    # --tool-path
    [string]$ToolPath = "",
    # --engine
    [string]$Engine = "",
    # --game-root
    [string]$GameRoot = "",
    # --game-family
    [string]$GameFamily = "",
    # --only
    [string]$Only = "",
    # --skip
    [string]$Skip = "",
    # --companion
    [string]$Companion = "",
    # --no-checksums
    [switch]$NoChecksums,
    # --help
    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Show-Usage {
    Write-Host "usage: run-acceptance.ps1 [options]"
    Write-Host ""
    Write-Host "  -Out <dir>           where the result bundle is written"
    Write-Host "  -ToolPath <dir>      the ROOT of an EricW build you already have"
    Write-Host "  -Engine <path>       an engine executable you already have"
    Write-Host "  -GameRoot <dir>      a game installation you already own"
    Write-Host "  -GameFamily <name>   quake1 (default), quake2 or quake3"
    Write-Host "  -Only <a,b>          run only these lanes"
    Write-Host "  -Skip <a,b>          run everything but these lanes"
    Write-Host "  -Companion <path>    the Companion to run"
    Write-Host "  -NoChecksums         skip the checksum check and RECORD that it was skipped"
    Write-Host "  -Help                this list"
    Write-Host ""
    Write-Host "No option here copies, archives, hashes or uploads game data."
}

if ($Help) { Show-Usage; exit 0 }

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
if ([string]::IsNullOrWhiteSpace($Companion)) {
    $Companion = Join-Path $scriptDir "companion.exe"
    if (-not (Test-Path -LiteralPath $Companion)) {
        $Companion = Join-Path $scriptDir "companion"
    }
}
if (-not (Test-Path -LiteralPath $Companion)) {
    Write-Error "no Companion at $Companion. Unpack the release artifact and run this script from inside it, or pass -Companion <path>."
    exit 2
}
if ([string]::IsNullOrWhiteSpace($Out)) {
    $Out = Join-Path $scriptDir "acceptance-bundle"
}

# --- the half a program cannot do for itself --------------------------------
#
# A binary cannot vouch for its own bytes: by the time it is running, whatever
# was going to happen has happened. So the check is here, it runs BEFORE the
# program starts, and its answer is passed in as a fact rather than assumed.
$checksums = "not_available"
$checksumDetail = "no SHA256SUMS beside the artifact"
$sums = Join-Path (Split-Path -Parent $Companion) "SHA256SUMS"
if ($NoChecksums) {
    $checksumDetail = "the operator passed -NoChecksums, so nothing was verified"
} elseif (Test-Path -LiteralPath $sums) {
    $name = Split-Path -Leaf $Companion
    $actual = (Get-FileHash -LiteralPath $Companion -Algorithm SHA256).Hash.ToLower()
    $expected = ""
    foreach ($line in Get-Content -LiteralPath $sums) {
        $fields = $line -split '\s+', 2
        if ($fields.Count -eq 2) {
            $named = $fields[1].Trim().TrimStart('*')
            if ($named -eq $name) { $expected = $fields[0].Trim().ToLower() }
        }
    }
    if ([string]::IsNullOrWhiteSpace($expected)) {
        $checksumDetail = "SHA256SUMS names no line for this artifact"
    } elseif ($expected -eq $actual) {
        $checksums = "pass"
        $checksumDetail = "the artifact matches its published SHA256SUMS line"
    } else {
        $checksums = "fail"
        $checksumDetail = "the artifact does NOT match its published SHA256SUMS line"
    }
}

if ($checksums -eq "fail") {
    Write-Error "$checksumDetail`nDo not run an artifact whose bytes are not the published ones. Download it again."
    exit 1
}

# What the operating system calls this machine. PROCESSOR_ARCHITEW6432 is set
# only inside a 32-bit process on a 64-bit Windows, and PROCESSOR_ARCHITECTURE
# then reports the emulated architecture rather than the real one — which is
# exactly the confusion this value exists to resolve.
$hostArch = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($hostArch)) { $hostArch = $env:PROCESSOR_ARCHITECTURE }
if ([string]::IsNullOrWhiteSpace($hostArch)) { $hostArch = "unknown" }
$shellId = "PowerShell $($PSVersionTable.PSVersion)"

$arguments = @(
    "acceptance", "run",
    "--out", $Out,
    "--entry-point", "powershell",
    "--shell", $shellId,
    "--host-arch", $hostArch,
    "--checksums", $checksums,
    "--checksum-detail", $checksumDetail
)
if (-not [string]::IsNullOrWhiteSpace($ToolPath))   { $arguments += @("--tool-path", $ToolPath) }
if (-not [string]::IsNullOrWhiteSpace($Engine))     { $arguments += @("--engine", $Engine) }
if (-not [string]::IsNullOrWhiteSpace($GameRoot))   { $arguments += @("--game-root", $GameRoot) }
if (-not [string]::IsNullOrWhiteSpace($GameFamily)) { $arguments += @("--game-family", $GameFamily) }
if (-not [string]::IsNullOrWhiteSpace($Only))       { $arguments += @("--only", $Only) }
if (-not [string]::IsNullOrWhiteSpace($Skip))       { $arguments += @("--skip", $Skip) }

Write-Host "== Auto-Pigeon Companion native acceptance =="
Write-Host "artifact   $Companion"
Write-Host "checksums  $checksums ($checksumDetail)"
Write-Host "machine    $hostArch"
Write-Host ""

# An argument ARRAY, never a composed command line: a path with a space in it is
# one argument, and nothing here is interpolated into a shell.
& $Companion @arguments
$status = $LASTEXITCODE

Write-Host ""
Write-Host "Send back the whole of $Out."
Write-Host "It holds no game data, no credential, no log and no absolute path;"
Write-Host "check that for yourself with:  $Companion acceptance verify $Out"
exit $status
