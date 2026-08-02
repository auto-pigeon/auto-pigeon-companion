; Inno Setup script for auto-pigeon-launcher.
;
; Compiled on a windows-latest CI runner with:
;
;   iscc /DAppVersion=0.1.0 /DSourceBinary=dist\windows-amd64\auto-pigeon-launcher.exe build\windows\installer.iss
;
; Skeleton, not a release artifact: it produces a working installer, but the
; release workflow does not run it yet and nothing here is code-signed. Signing
; is a documented step in README.md, not an implemented one — no certificate
; exists.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef SourceBinary
  #define SourceBinary "..\..\dist\windows-amd64\auto-pigeon-launcher.exe"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist\installer"
#endif
; ArchitecturesAllowed: pass /DTargetArch=arm64 for the arm64 build.
#ifndef TargetArch
  #define TargetArch "x64compatible"
#endif

#define AppName "Auto-Pigeon Launcher"
#define AppPublisher "Andrea D'Intino"
#define AppURL "https://github.com/andrea-dintino/auto-pigeon-launcher"
#define AppExeName "auto-pigeon-launcher.exe"

[Setup]
; A fixed GUID is what makes an upgrade replace the previous install rather than
; sit beside it. Generated once for this application; never change it.
AppId={{4E1B0D2A-5C3F-4A87-9F2E-7B6C1D8E0A45}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
; The app writes only to the user's config and cache directories, so it does not
; need administrator rights at runtime. Installing per-user by default keeps it
; that way and avoids a UAC prompt.
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed={#TargetArch}
ArchitecturesInstallIn64BitMode={#TargetArch}
OutputDir={#OutputDir}
OutputBaseFilename=auto-pigeon-launcher-{#AppVersion}-{#TargetArch}-setup
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
