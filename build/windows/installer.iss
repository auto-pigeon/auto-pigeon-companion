; Inno Setup script for Auto-Pigeon Companion.
;
; Compile on a windows-latest CI runner (Inno Setup ships preinstalled there):
;
;   iscc /DAppVersion=0.1.0 /DBinaryPath=dist\windows-amd64\companion.exe ^
;        /DOutputDir=dist\installers build\windows\installer.iss
;
; The binary is built beforehand with plain `GOOS=windows GOARCH=amd64 go build`
; — there is no CGO in this project, so no toolchain setup is involved.
;
; Not implemented in this session: code signing. SignTool= directives belong
; here once a certificate exists; until then the installer is unsigned and
; SmartScreen will warn on first run. See README.md.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef BinaryPath
  #define BinaryPath "..\..\dist\windows-amd64\companion.exe"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist\installers"
#endif

#define AppName "Auto-Pigeon Companion"
#define AppPublisher "Andrea D'Intino"
#define AppExeName "companion.exe"

[Setup]
AppId={{9F1C0F5B-6C4A-4C1E-9E77-4B4A2C6D51A2}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
DefaultDirName={autopf}\Auto-Pigeon Companion
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
; Per-user install by default: no elevation prompt, and the app writes only to
; the user's own config directory anyway.
PrivilegesRequiredOverridesAllowed=dialog
OutputDir={#OutputDir}
OutputBaseFilename=auto-pigeon-companion-{#AppVersion}-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
; arm64 installers are produced by pointing BinaryPath at the arm64 build and
; adding ArchitecturesAllowed=arm64 in that invocation.

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "{#BinaryPath}"; DestDir: "{app}"; DestName: "{#AppExeName}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExeName}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts:"; Flags: unchecked

[Run]
; Launching with no arguments is GUI mode: it starts the local server and opens
; the default browser.
Filename: "{app}\{#AppExeName}"; Description: "Launch {#AppName}"; Flags: nowait postinstall skipifsilent
