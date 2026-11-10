# Run by the installer before it replaces files. It stops the WhatsApp MCP
# services, waits until Windows reports them as stopped, closes any program
# window still running from the install folder, and checks that
# whatsapp-mcp.exe can be overwritten. It never fails the installer: it only
# reports what it did.
param(
  [string]$InstallDir = "",
  [int]$Timeout = 60
)
$ErrorActionPreference = 'Continue'

function Stop-AndWait([string]$Name) {
  $s = Get-Service -Name $Name -ErrorAction SilentlyContinue
  if (-not $s) { Write-Output "Servicio ${Name}: no estaba instalado."; return }
  if ($s.Status -eq 'Stopped') { Write-Output "Servicio ${Name}: ya estaba detenido."; return }
  Write-Output "Servicio ${Name}: deteniendo..."
  try { Stop-Service -Name $Name -Force -ErrorAction Stop } catch { Write-Output "  aviso: $($_.Exception.Message)" }
  try {
    $s.WaitForStatus('Stopped', [TimeSpan]::FromSeconds($Timeout))
    Write-Output "Servicio ${Name}: detenido."
  } catch {
    Write-Output "Servicio ${Name}: no se detuvo en $Timeout segundos; se cierra a la fuerza."
  }
}

function Get-Procs([string[]]$Names, [string]$Under) {
  Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
    ($Names -contains $_.Name) -and
    (-not $Under -or ($_.ExecutablePath -and $_.ExecutablePath.StartsWith($Under, [StringComparison]::OrdinalIgnoreCase)))
  }
}

function Close-Procs([string[]]$Names, [string]$Under, [string]$Label) {
  for ($i = 0; $i -lt 10; $i++) {
    $p = @(Get-Procs $Names $Under)
    if ($p.Count -eq 0) { if ($i -gt 0) { Write-Output "${Label}: cerrados." }; return }
    if ($i -eq 0) { Write-Output "${Label}: cerrando $($p.Count) proceso(s) que seguian abiertos..." }
    foreach ($x in $p) { Stop-Process -Id $x.ProcessId -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Milliseconds 700
  }
  Write-Output "${Label}: algun proceso no se pudo cerrar."
}

# The app first, then its database (the app depends on it).
Stop-AndWait 'WhatsAppMCP'
Close-Procs @('whatsapp-mcp.exe') '' 'Ventanas de WhatsApp MCP'
Stop-AndWait 'WhatsAppMCP-Postgres'
if ($InstallDir) {
  Close-Procs @('postgres.exe', 'pg_ctl.exe') (Join-Path $InstallDir 'pgsql') 'Base de datos'
}

# Wait until the program file is no longer locked.
if ($InstallDir) {
  $exe = Join-Path $InstallDir 'whatsapp-mcp.exe'
  if (Test-Path -LiteralPath $exe) {
    for ($i = 0; $i -lt 20; $i++) {
      try {
        $f = [IO.File]::Open($exe, 'Open', 'ReadWrite', 'None'); $f.Close()
        Write-Output "El programa se puede reemplazar."
        break
      } catch {
        if ($i -eq 19) { Write-Output "El programa sigue en uso; el instalador lo intenta de nuevo." }
        Start-Sleep -Milliseconds 500
      }
    }
  }
}
exit 0
