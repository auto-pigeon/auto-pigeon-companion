; Inno Setup script for Auto-Pigeon Companion.
;
; Compile on a windows-latest CI runner (Inno Setup ships preinstalled there):
;
;   iscc /DAppVersion=0.1.0 /DSourceBinary=dist\windows-amd64\companion.exe ^
;        /DOutputDir=dist\installers build\windows\installer.iss
;
; The binary is built beforehand with plain `GOOS=windows GOARCH=amd64 go build`
; — there is no CGO in this project, so no toolchain setup is involved.
;
; Skeleton, not a release artifact: it produces a working installer, but no
; release workflow runs it and nothing here is code-signed. SignTool= directives
; belong here once a certificate exists; until then the installer is unsigned
; and SmartScreen will warn on first run. See README.md.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef SourceBinary
  #define SourceBinary "..\..\dist\windows-amd64\companion.exe"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist\installers"
#endif
; ArchitecturesAllowed: pass /DTargetArch=arm64 for the arm64 build.
#ifndef TargetArch
  #define TargetArch "x64compatible"
#endif

#define AppName "Auto-Pigeon Companion"
#define AppPublisher "Andrea D'Intino"
#define AppURL "https://github.com/auto-pigeon/auto-pigeon-companion"
#define AppExeName "companion.exe"

[Setup]
; A fixed GUID is what makes an upgrade replace the previous install rather than
; sit beside it. This is the Companion's own AppId, generated for it; the
; retired Launcher had a different one, so a machine carrying both keeps two
; entries until the user removes the Launcher. Never change this value.
AppId={{9F1C0F5B-6C4A-4C1E-9E77-4B4A2C6D51A2}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
; The app writes only to the user's config and cache directories, so it does not
; need administrator rights at runtime. Installing per-user by default keeps it
; that way and avoids a UAC prompt.
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed={#TargetArch}
ArchitecturesInstallIn64BitMode={#TargetArch}
OutputDir={#OutputDir}
OutputBaseFilename=auto-pigeon-companion-{#AppVersion}-{#TargetArch}-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
LicenseFile=..\..\LICENSE
; TODO(andrea): the external GPL-2.0 map-building tools are downloaded at first
; run, so nothing of theirs is packaged here. If that ever changes and a tool
; binary is bundled into the installer, its license text and copyright notice
; must be installed alongside it and listed here in [Files]. See
; THIRD_PARTY_NOTICES.md.

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#SourceBinary}"; DestDir: "{app}"; DestName: "{#AppExeName}"; Flags: ignoreversion
Source: "..\..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\README.md"; DestDir: "{app}"; Flags: ignoreversion

[Registry]
; The autopigeon:// handler.
;
; HKCU, under {autopf} or not: a per-user class needs no administrator rights,
; matches PrivilegesRequired=lowest above, and is removed with the user's
; profile. A machine-wide HKLM class would change the scheme for every account
; on the computer, which is not what installing an application for oneself asks
; for.
;
; uninsdeletekey on the root key is what makes an uninstall take the handler
; with it, subkeys included. Without it the scheme would keep pointing at a
; program that is no longer there.
;
; The command is `companion game open "%1"` — no approval flag: opening a link
; records it and raises the Companion's page, and starts nothing. Both the program
; path and %1 are quoted: an unquoted path under "C:\Program Files" invites the
; loader to try "C:\Program.exe", and %1 is a string somebody else chose.
Root: HKCU; Subkey: "Software\Classes\autopigeon"; ValueType: string; ValueName: ""; ValueData: "URL:Auto-Pigeon Companion join link"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\autopigeon"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""
Root: HKCU; Subkey: "Software\Classes\autopigeon\DefaultIcon"; ValueType: string; ValueName: ""; ValueData: "{app}\{#AppExeName},0"
Root: HKCU; Subkey: "Software\Classes\autopigeon\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#AppExeName}"" game open ""%1"""

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExeName}"
Name: "{group}\{cm:UninstallProgram,{#AppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Run]
; No arguments: that is GUI mode — the app starts its local server and opens the
; default browser. nowait so the installer's final page is not blocked by the
; running app.
Filename: "{app}\{#AppExeName}"; Description: "{cm:LaunchProgram,{#StringChange(AppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Downloaded tool binaries live in the user's cache directory and are left
; alone: they are third-party programs under their own licenses, and an
; uninstaller deleting a directory it did not create is how unrelated data gets
; lost. The user can clear the cache themselves.
