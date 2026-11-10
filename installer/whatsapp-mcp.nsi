; WhatsApp MCP - Windows installer (NSIS 3, Unicode).
; Build with installer/build.sh, which prepares the files under STAGE.

Unicode true
!ifndef VERSION
  !define VERSION "2.0.1"
!endif
!ifndef STAGE
  !define STAGE "stage"
!endif
!ifndef OUTFILE
  !define OUTFILE "WhatsAppMCP-Setup-${VERSION}.exe"
!endif

!define APP      "WhatsApp MCP"
!define EXE      "whatsapp-mcp.exe"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\WhatsAppMCP"

!include "MUI2.nsh"
!include "x64.nsh"
!include "LogicLib.nsh"

Name "${APP} ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\WhatsAppMCP"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
ShowInstDetails show
ShowUninstDetails show
BrandingText "${APP} ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=1034 "ProductName" "${APP}"
VIAddVersionKey /LANG=1034 "FileDescription" "Instalador de ${APP}"
VIAddVersionKey /LANG=1034 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1034 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1034 "LegalCopyright" "MIT"

!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "Instalar ${APP} ${VERSION}"
!define MUI_WELCOMEPAGE_TEXT "Esto instala el conector de WhatsApp para Claude en este servidor:$\r$\n$\r$\n\
  - Una base de datos PostgreSQL privada, solo para WhatsApp (escucha solo en 127.0.0.1).$\r$\n\
  - Un servicio de Windows que arranca solo y se reinicia si falla.$\r$\n\
  - Acceso solo por Tailscale y con una clave que se genera aquí.$\r$\n$\r$\n\
  No se vincula ningún número de WhatsApp todavía: eso lo haces tú después en el panel web.$\r$\n$\r$\n\
  Si ya estaba instalado, se actualiza y se conservan los mensajes, la sesión, la clave y los ajustes."
!define MUI_FINISHPAGE_TITLE "Instalación terminada"
!define MUI_FINISHPAGE_TEXT "El servicio ya está corriendo.$\r$\n$\r$\nTodo se maneja desde el panel web: menú Inicio > WhatsApp MCP > Panel de WhatsApp MCP. Ahí ves si WhatsApp está vinculado, escaneas el código QR, apruebas números desde tus conversaciones y cambias los ajustes."
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Abrir el panel ahora"
!define MUI_FINISHPAGE_RUN_FUNCTION RunPair
!define MUI_FINISHPAGE_SHOWREADME "$INSTDIR\GUIA.txt"
!define MUI_FINISHPAGE_SHOWREADME_TEXT "Abrir la guía"
!define MUI_FINISHPAGE_SHOWREADME_NOTCHECKED

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Spanish"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "${APP} necesita Windows de 64 bits."
    Abort
  ${EndIf}
  SetRegView 64
  ; Keep the folder of an earlier installation.
  ReadRegStr $0 HKLM "${UNINSTKEY}" "InstallLocation"
  ${If} $0 != ""
    StrCpy $INSTDIR $0
  ${EndIf}
FunctionEnd

Function un.onInit
  SetRegView 64
FunctionEnd

Function RunPair
  Exec '"$INSTDIR\${EXE}" panel --pause-on-error'
FunctionEnd

; Services must be stopped before their files can be replaced.
!macro StopServices
  DetailPrint "Deteniendo los servicios de ${APP} (si estaban corriendo)..."
  nsExec::ExecToLog 'powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "Stop-Service -Name WhatsAppMCP -Force -ErrorAction SilentlyContinue; Stop-Service -Name WhatsAppMCP-Postgres -Force -ErrorAction SilentlyContinue"'
  Pop $0
!macroend

Section "Instalar"
  SetShellVarContext all
  !insertmacro StopServices

  SetOutPath "$INSTDIR"
  File "${STAGE}\${EXE}"
  File "${STAGE}\GUIA.txt"
  File "${STAGE}\LICENSE.txt"
  File "${STAGE}\THIRD-PARTY.txt"
  SetOutPath "$INSTDIR\pgsql"
  File /r "${STAGE}\pgsql\*.*"
  SetOutPath "$INSTDIR"

  DetailPrint "Configurando base de datos y servicio (puede tardar un minuto)..."
  nsExec::ExecToLog '"$INSTDIR\${EXE}" install --pg-bin "$INSTDIR\pgsql\bin"'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_ICONEXCLAMATION "La configuración no terminó bien (código $0).$\r$\n$\r$\nRevisa los detalles de esta ventana. Puedes volver a ejecutar el instalador: no borra nada."
    DetailPrint "Error en la configuración (código $0)."
  ${EndIf}

  RMDir /r "$SMPROGRAMS\${APP}"
  CreateDirectory "$SMPROGRAMS\${APP}"
  CreateShortcut "$SMPROGRAMS\${APP}\Panel de WhatsApp MCP.lnk" "$INSTDIR\${EXE}" "panel"
  CreateShortcut "$SMPROGRAMS\${APP}\Vincular WhatsApp.lnk" "$INSTDIR\${EXE}" "panel --open vincular"
  CreateShortcut "$SMPROGRAMS\${APP}\Panel en modo texto.lnk" "$INSTDIR\${EXE}" "menu"
  CreateShortcut "$SMPROGRAMS\${APP}\Estado.lnk" "$INSTDIR\${EXE}" "status --pause"
  CreateShortcut "$SMPROGRAMS\${APP}\Conectar desde la Mac.lnk" "$INSTDIR\${EXE}" "connection-info --pause"
  CreateShortcut "$SMPROGRAMS\${APP}\Guía.lnk" "$INSTDIR\GUIA.txt"
  CreateShortcut "$SMPROGRAMS\${APP}\Desinstalar.lnk" "$INSTDIR\Desinstalar.exe"

  WriteUninstaller "$INSTDIR\Desinstalar.exe"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayName" "${APP}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTKEY}" "Publisher" "${APP}"
  WriteRegStr HKLM "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\${EXE}"
  WriteRegStr HKLM "${UNINSTKEY}" "UninstallString" '"$INSTDIR\Desinstalar.exe"'
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "EstimatedSize" 120000
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  StrCpy $1 ""
  MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "¿Borrar también los mensajes guardados, la sesión de WhatsApp, la clave y los ajustes?$\r$\n$\r$\nNo (recomendado): se conservan en C:\ProgramData\WhatsAppMCP por si vuelves a instalar.$\r$\nSí: se borra todo y no se puede deshacer." IDNO keep
    StrCpy $1 " --purge"
  keep:
  nsExec::ExecToLog '"$INSTDIR\${EXE}" uninstall$1'
  Pop $0

  RMDir /r "$INSTDIR\pgsql"
  Delete "$INSTDIR\${EXE}"
  Delete "$INSTDIR\GUIA.txt"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\THIRD-PARTY.txt"
  Delete "$INSTDIR\Desinstalar.exe"
  RMDir "$INSTDIR"
  RMDir /r "$SMPROGRAMS\${APP}"
  DeleteRegKey HKLM "${UNINSTKEY}"
SectionEnd
