param(
    [Parameter(Mandatory = $true)][string]$PackageDirectory,
    [Parameter(Mandatory = $true)][string]$OutputDirectory,
    [string]$Address = '127.0.0.1:8787',
    [string]$ProxyAddress = '127.0.0.1:8788'
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath $PackageDirectory).Path
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$result = [ordered]@{ passed = $false; isolated_path = $false }
$originalPath = $env:PATH
$originalPassword = $env:PGPASSWORD
$pgCtl = Join-Path $root 'runtime\pgsql\bin\pg_ctl.exe'
$pgData = Join-Path $root 'data\postgres'
$psql = Join-Path $root 'runtime\pgsql\bin\psql.exe'
$launcher = $null
$url = "http://$Address"

function Start-Portable([string]$LogName) {
    $arguments = '/d /s /c ""{0}" -addr={1} -proxy={2} -no-browser"' -f (Join-Path $root 'start.bat'), $Address, $ProxyAddress
    Start-Process -FilePath cmd.exe -ArgumentList $arguments -WorkingDirectory $root -WindowStyle Hidden -RedirectStandardOutput (Join-Path $OutputDirectory "$LogName.stdout.log") -RedirectStandardError (Join-Path $OutputDirectory "$LogName.stderr.log") -PassThru
}

function Wait-Healthy {
    $deadline = (Get-Date).AddSeconds(90)
    do {
        try {
            $health = Invoke-RestMethod -Uri "$url/api/health" -TimeoutSec 2
            if ($health.ok -and $health.service -eq 'artex') { return $health }
        } catch { }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    throw 'The portable launcher did not start ARTEX.'
}

function Stop-Portable {
    if ($launcher -and -not $launcher.HasExited) {
        & "$env:SystemRoot\System32\taskkill.exe" /PID $launcher.Id /T /F 2>&1 | Out-Null
    }
    if (Test-Path -LiteralPath (Join-Path $pgData 'postmaster.pid')) {
        & $pgCtl -D $pgData -m fast -w -t 20 stop
        if ($LASTEXITCODE -ne 0) { throw 'Stopping the test database failed.' }
    }
}

try {
    foreach ($name in @('config.json', 'data', 'jwt.key')) {
        if (Test-Path -LiteralPath (Join-Path $root $name)) { throw "Package already contains runtime data: $name" }
    }
    $env:PATH = "$env:SystemRoot\System32;$env:SystemRoot;$env:SystemRoot\System32\WindowsPowerShell\v1.0"
    $result.isolated_path = -not [bool](Get-Command node.exe,python.exe,psql.exe,go.exe,chrome.exe -ErrorAction SilentlyContinue)
    if (-not $result.isolated_path) { throw 'External runtime executables are still on PATH.' }
    $launcher = Start-Portable 'launcher'
    $result.health = Wait-Healthy
    $result.setup_page = (Invoke-WebRequest -UseBasicParsing -Uri "$url/setup/" -TimeoutSec 5).StatusCode
    $status = Invoke-RestMethod -Uri "$url/api/auth/status" -TimeoutSec 5
    $result.fresh_admin_setup = -not $status.initialized
    $cfg = Get-Content -LiteralPath (Join-Path $root 'config.json') -Raw | ConvertFrom-Json
    $result.generated_password = $cfg.database.password.Length -ge 32
    $result.local_database_host = $cfg.database.host
    $env:PGPASSWORD = $cfg.database.password
    $env:PGCONNECT_TIMEOUT = '5'
    $sql = & $psql -X -w -h 127.0.0.1 -p $cfg.database.port -U artex -d artex -At -c 'SELECT 1;'
    if ($LASTEXITCODE -ne 0 -or $sql -ne '1') { throw 'The bundled PostgreSQL query failed.' }
    $result.database_query = $true
    $result.node_version = (& (Join-Path $root 'runtime\node\node.exe') --version)
    if ($LASTEXITCODE -ne 0) { throw 'The bundled Node.js did not run.' }
    $result.python_version = (& (Join-Path $root 'runtime\python\python3.exe') --version)
    if ($LASTEXITCODE -ne 0) { throw 'The bundled Python did not run.' }
    & (Join-Path $root 'runtime\python\python3.exe') -c 'import ssl,urllib.request,json; print(ssl.OPENSSL_VERSION)'
    if ($LASTEXITCODE -ne 0) { throw 'The bundled Python standard library did not load.' }
    $browser = & (Join-Path $root 'runtime\node\node.exe') (Join-Path $PSScriptRoot 'browser.cjs') $root $url
    if ($LASTEXITCODE -ne 0) { throw 'The bundled Chrome browser test failed.' }
    $result.browser = $browser | ConvertFrom-Json
    $body = @{password = 'PortableSmoke!2026'} | ConvertTo-Json
    $null = Invoke-RestMethod -Method Post -Uri "$url/api/auth/init" -ContentType 'application/json' -Body $body
    $result.admin_initialized = (Invoke-RestMethod -Uri "$url/api/auth/status").initialized
    $initialPort = $cfg.database.port
    Stop-Portable
    $launcher = Start-Portable 'restart'
    $null = Wait-Healthy
    $cfg2 = Get-Content -LiteralPath (Join-Path $root 'config.json') -Raw | ConvertFrom-Json
    $status2 = Invoke-RestMethod -Uri "$url/api/auth/status"
    $result.restart = $status2.initialized -and $initialPort -eq $cfg2.database.port -and $cfg.database.password -eq $cfg2.database.password
    $result.passed = $result.isolated_path -and $result.fresh_admin_setup -and $result.generated_password -and $result.database_query -and $result.browser.passed -and $result.admin_initialized -and $result.restart -and $result.local_database_host -eq '127.0.0.1'
    if (-not $result.passed) { throw 'A portable package acceptance check failed.' }
} catch {
    $result.error = $_.Exception.Message
    throw
} finally {
    try { Stop-Portable } catch { Write-Warning $_.Exception.Message }
    $env:PATH = $originalPath
    $env:PGPASSWORD = $originalPassword
    Remove-Item Env:\PGCONNECT_TIMEOUT -ErrorAction SilentlyContinue
    $result.finished_at = (Get-Date).ToString('o')
    $result | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $OutputDirectory 'result.json') -Encoding UTF8
}
