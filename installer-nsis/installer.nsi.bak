Unicode true
!include "MUI2.nsh"

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef SRCEXE
  !define SRCEXE "MyMonitor.exe"
!endif

!define APPNAME "Hajir Monitor"
!define ARP "Software\Microsoft\Windows\CurrentVersion\Uninstall\MyMonitor"
!define RUNKEY "Software\Microsoft\Windows\CurrentVersion\Run"

Name "${APPNAME}"
!ifndef OUTFILE
  !define OUTFILE "MyMonitor-Setup-windows-amd64.exe"
!endif
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\MyMonitor"
RequestExecutionLevel user
SetCompressor /SOLID lzma

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "CompanyName" "Velox Labs"
VIAddVersionKey "LegalCopyright" "Velox Labs"
VIAddVersionKey "FileDescription" "${APPNAME} Installer"

!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\MyMonitor.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Start ${APPNAME} now"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Install"
  ; Stop any running instance so files aren't locked during upgrade.
  nsExec::Exec 'taskkill /F /IM MyMonitor.exe'

  SetOutPath "$INSTDIR"
  File /oname=MyMonitor.exe "${SRCEXE}"

  ; Start-menu shortcuts
  CreateDirectory "$SMPROGRAMS\${APPNAME}"
  CreateShortcut "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk" "$INSTDIR\MyMonitor.exe"
  CreateShortcut "$SMPROGRAMS\${APPNAME}\Uninstall ${APPNAME}.lnk" "$INSTDIR\uninstall.exe"

  ; Start on login (same HKCU Run value the app self-registers)
  WriteRegStr HKCU "${RUNKEY}" "MyMonitor" '"$INSTDIR\MyMonitor.exe"'

  ; Add/Remove Programs entry (per-user)
  WriteRegStr   HKCU "${ARP}" "DisplayName"     "${APPNAME}"
  WriteRegStr   HKCU "${ARP}" "DisplayVersion"  "${VERSION}"
  WriteRegStr   HKCU "${ARP}" "Publisher"       "Velox Labs"
  WriteRegStr   HKCU "${ARP}" "DisplayIcon"     "$INSTDIR\MyMonitor.exe"
  WriteRegStr   HKCU "${ARP}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr   HKCU "${ARP}" "InstallLocation" "$INSTDIR"
  WriteRegDWORD HKCU "${ARP}" "NoModify" 1
  WriteRegDWORD HKCU "${ARP}" "NoRepair" 1

  WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /F /IM MyMonitor.exe'
  DeleteRegValue HKCU "${RUNKEY}" "MyMonitor"
  DeleteRegKey   HKCU "${ARP}"
  Delete "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk"
  Delete "$SMPROGRAMS\${APPNAME}\Uninstall ${APPNAME}.lnk"
  RMDir  "$SMPROGRAMS\${APPNAME}"
  ; Remove app + its data (data lives under the install dir on Windows)
  RMDir /r "$INSTDIR"
SectionEnd
