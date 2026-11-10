param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$WorkDirectory,
    [Parameter(Mandatory = $true)][string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^v?\d+\.\d+\.\d+$') { throw 'Expected a release version such as 0.3.18.' }
New-Item -ItemType Directory -Path $WorkDirectory -Force | Out-Null
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$work = (Resolve-Path -LiteralPath $WorkDirectory).Path
$output = (Resolve-Path -LiteralPath $OutputDirectory).Path

# This immutable, previously smoke-tested package supplies ONLY third-party
# runtimes. build.ps1 compiles both application executables from this checkout,
# copies this checkout's skills, and installs their locked browser dependencies.
$runtimeURL = 'https://github.com/Alexander4731/ARTEX-God/releases/download/0.3.17/artex-0.3.17-windows-amd64.zip'
$runtimeSHA256 = 'b0955839a03b0315e8496b69351bffa8c09b4db65d5ac8af9d14a5c60a7daaf6'
$archive = Join-Path $work 'runtime-source.zip'
Invoke-WebRequest -Uri $runtimeURL -OutFile $archive
if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $runtimeSHA256) {
    throw 'The pinned Windows runtime source failed SHA-256 verification.'
}
$source = Join-Path $work 'runtime-source'
Expand-Archive -LiteralPath $archive -DestinationPath $source
$runtime = Join-Path $source 'artex-0.3.17-windows-amd64/runtime'
$pythonArchive = Join-Path $work 'python.zip'
Compress-Archive -Path (Join-Path $runtime 'python/*') -DestinationPath $pythonArchive

& (Join-Path $PSScriptRoot 'build.ps1') -Version $Version `
    -PostgreSQLRoot (Join-Path $runtime 'pgsql') `
    -NodeRoot (Join-Path $runtime 'node') `
    -PythonArchive $pythonArchive `
    -ChromeRoot (Join-Path $runtime 'chrome') `
    -VCRuntimeDirectory (Join-Path $runtime 'node') `
    -OutputDirectory $output

$package = Join-Path $output "artex-$($Version.TrimStart('v'))-windows-amd64"
# Package before smoke testing: no generated passwords, config or databases can
# enter the archive. The workflow tests a separate extraction of this exact zip.
foreach ($name in @('config.json', 'data', 'jwt.key')) {
    if (Test-Path -LiteralPath (Join-Path $package $name)) { throw "Unexpected runtime data in package: $name" }
}
Compress-Archive -LiteralPath $package -DestinationPath "$package.zip" -CompressionLevel Optimal
Write-Output "RELEASE_ARCHIVE=$package.zip"
