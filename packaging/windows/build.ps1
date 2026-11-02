param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$PostgreSQLRoot,
    [Parameter(Mandatory = $true)][string]$NodeRoot,
    [Parameter(Mandatory = $true)][string]$PythonArchive,
    [string]$ChromeArchive,
    [string]$ChromeRoot,
    [string]$OutputDirectory,
    [string]$VCRuntimeDirectory = "$env:SystemRoot\System32"
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $repo 'dist' }
$versionName = $Version.TrimStart('v')
$name = "artex-$versionName-windows-amd64"
$package = Join-Path $OutputDirectory $name
if (Test-Path -LiteralPath $package) { throw "Package directory already exists: $package" }
if (-not $ChromeRoot -and -not $ChromeArchive) { throw 'Supply ChromeRoot or ChromeArchive.' }
foreach ($path in @($PostgreSQLRoot, $NodeRoot, $PythonArchive, $(if ($ChromeRoot) { $ChromeRoot } else { $ChromeArchive }))) {
    if (-not (Test-Path -LiteralPath $path)) { throw "Runtime input does not exist: $path" }
}
if (-not (Test-Path -LiteralPath (Join-Path $repo 'server\webui\dist\index.html'))) {
    throw 'Build the embedded frontend in server/webui/dist first.'
}
New-Item -ItemType Directory -Path $package -Force | Out-Null
$runtime = Join-Path $package 'runtime'
New-Item -ItemType Directory -Path $runtime | Out-Null
& robocopy.exe $PostgreSQLRoot (Join-Path $runtime 'pgsql') /E /R:0 /W:0 /MT:8 /NFL /NDL /NJH /NJS /NP
if ($LASTEXITCODE -ge 8) { throw 'Copying PostgreSQL failed.' }
$node = Join-Path $runtime 'node'
New-Item -ItemType Directory -Path $node | Out-Null
foreach ($file in @('node.exe', 'nodevars.bat', 'npm', 'npm.cmd', 'npx', 'npx.cmd', 'LICENSE', 'README.md')) {
    Copy-Item -LiteralPath (Join-Path $NodeRoot $file) -Destination (Join-Path $node $file)
}
& robocopy.exe (Join-Path $NodeRoot 'node_modules\npm') (Join-Path $node 'node_modules\npm') /E /R:0 /W:0 /MT:8 /NFL /NDL /NJH /NJS /NP
if ($LASTEXITCODE -ge 8) { throw 'Copying npm failed.' }
$python = Join-Path $runtime 'python'
Expand-Archive -LiteralPath $PythonArchive -DestinationPath $python
Copy-Item -LiteralPath (Join-Path $python 'python.exe') -Destination (Join-Path $python 'python3.exe')
if ($ChromeRoot) {
    & robocopy.exe $ChromeRoot (Join-Path $runtime 'chrome') /E /R:0 /W:0 /MT:8 /NFL /NDL /NJH /NJS /NP
    if ($LASTEXITCODE -ge 8) { throw 'Copying Chrome failed.' }
} else {
    $chromeStage = Join-Path $runtime 'chrome-stage'
    Expand-Archive -LiteralPath $ChromeArchive -DestinationPath $chromeStage
    Move-Item -LiteralPath (Join-Path $chromeStage 'chrome-win64') -Destination (Join-Path $runtime 'chrome')
    Remove-Item -LiteralPath $chromeStage
}
foreach ($directory in @((Join-Path $runtime 'pgsql\bin'), (Join-Path $runtime 'node'), $python, (Join-Path $runtime 'chrome'))) {
    foreach ($dll in @('vcruntime140.dll', 'vcruntime140_1.dll', 'msvcp140.dll', 'msvcp140_1.dll', 'msvcp140_2.dll', 'msvcp140_atomic_wait.dll', 'msvcp140_codecvt_ids.dll')) {
        $target = Join-Path $directory $dll
        if (-not (Test-Path -LiteralPath $target)) {
            $source = Join-Path $VCRuntimeDirectory $dll
            if (-not (Test-Path -LiteralPath $source)) { throw "Visual C++ runtime DLL is missing: $dll" }
            Copy-Item -LiteralPath $source -Destination $target
        }
    }
}
$skillFiles = & git.exe -C $repo ls-files -- skills
if ($LASTEXITCODE -ne 0) { throw 'Listing the skill sources failed.' }
foreach ($file in $skillFiles) {
    $target = Join-Path $package $file
    New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $repo $file) -Destination $target
}
$scripts = Join-Path $package 'skills\api-recon\scripts'
$env:PATH = (Join-Path $runtime 'node') + ';' + $env:PATH
& (Join-Path $runtime 'node\npm.cmd') ci --prefix $scripts --omit=dev --ignore-scripts
if ($LASTEXITCODE -ne 0) { throw 'Installing the bundled browser dependencies failed.' }
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'start.bat') -Destination (Join-Path $package 'start.bat')
Copy-Item -LiteralPath (Join-Path $repo 'start.bat') -Destination (Join-Path $package 'start-engine.bat')
Copy-Item -LiteralPath (Join-Path $repo 'LICENSE') -Destination (Join-Path $package 'LICENSE')
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
Push-Location $repo
try {
    & go.exe build -mod=readonly -tags embedui -trimpath -ldflags "-s -w -buildid= -X main.version=$versionName -X github.com/Autumn-27/artex/selfupdate.Repo=Alexander4731/ARTEX-God" -o (Join-Path $package 'artex.exe') ./cmd/artex
    if ($LASTEXITCODE -ne 0) { throw 'Building ARTEX failed.' }
    & go.exe build -mod=readonly -trimpath -ldflags '-s -w -buildid=' -o (Join-Path $package 'artex-launcher.exe') ./packaging/windows
    if ($LASTEXITCODE -ne 0) { throw 'Building the portable launcher failed.' }
} finally {
    Pop-Location
}
Write-Output "PACKAGE_DIRECTORY=$package"
