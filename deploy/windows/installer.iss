; Varya One — Windows kurulum sihirbazi (Inno Setup 6).
;
; Derleme:
;   iscc //DMyAppVersion=0.3.0 deploy/windows/installer.iss
;
; Once "deploy/windows/build-bundle.sh <version>" calisir; o script hazir kurulum
; agacini (dist/windows/VaryaOne/) uretir ve iscc'yi bu betikle cagirir.
; Cikti: dist/windows/VaryaOne-Setup-<version>.exe

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0-dev"
#endif

#define MyAppName "Varya One"
#define MyAppPublisher "Varya One"
#define MyAppURL "https://varyaone.com/"
#define MyExeName "varyaone.exe"
#define ClientExe "varyaone-client.exe"

; Numeric x.x.x.x for the VERSIONINFO resource (strips a -rc/-alpha suffix and a
; leading v). Pass //DMyNumericVersion=1.2.3.0 to override.
#ifndef MyNumericVersion
  #define MyNumericVersion "0.0.0.0"
#endif

[Setup]
AppId={{7E9B2C41-1A55-4F2E-9C7D-1B2A3C4D5E6F}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={autopf}\VaryaOne
DefaultGroupName=Varya One
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#SourcePath}..\..\dist\windows
OutputBaseFilename=VaryaOne-Setup-{#MyAppVersion}
SetupIconFile={#SourcePath}appicon.ico
UninstallDisplayIcon={app}\appicon.ico
UninstallDisplayName={#MyAppName}
WizardStyle=modern
; Kurulum arayuzu dogrudan Turkce acilir, dil secme penceresi gosterilmez.
ShowLanguageDialog=no
LanguageDetectionMethod=none
Compression=lzma2/max
SolidCompression=yes

; VERSIONINFO — SmartScreen/Defender'a "bilinmeyen yayinci" izlenimini biraz azaltir
; (asil cozum kod imzalama, asagiya bakin).
VersionInfoVersion={#MyNumericVersion}
VersionInfoProductVersion={#MyNumericVersion}
VersionInfoCompany={#MyAppPublisher}
VersionInfoProductName={#MyAppName}
VersionInfoDescription=Varya One Kurulum

; Kod imzalama (opsiyonel): iscc'ye "SignTool" tanimini gec, ornegin
;   iscc "/Ssigntool=signtool sign /fd sha256 /f C:\cert.pfx /p PAROLA $f" ...
; sonra asagidaki satirlarin yorumunu kaldir:
; SignTool=signtool
; SignedUninstaller=yes

[Languages]
Name: "tr"; MessagesFile: "compiler:Languages\Turkish.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"
Name: "autostartpanel"; Description: "Varya Kontrol Paneli Windows acilisinda sistem tepsisinde baslasin"; GroupDescription: "Baslangic"
Name: "lanaccess"; Description: "Agdaki diger bilgisayarlar bu sunucuya erisebilsin (Ag modu)"; GroupDescription: "Ag modu"

[Files]
Source: "{#SourcePath}..\..\dist\windows\VaryaOne\*"; DestDir: "{app}"; Flags: recursesubdirs createallsubdirs ignoreversion
Source: "{#SourcePath}appicon.ico";   DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourcePath}panelicon.ico"; DestDir: "{app}"; Flags: ignoreversion
; Onkosullar (fetch-tools.ps1 indirir). VC++ calisma zamani olmadan gomulu
; PostgreSQL initdb 0xC0000135 verir; WebView2 olmadan kontrol paneli acilmaz.
Source: "{#SourcePath}vc_redist.x64.exe";               Flags: dontcopy
Source: "{#SourcePath}MicrosoftEdgeWebview2Setup.exe";  Flags: dontcopy

[Icons]
; Masaustunde iki ayri kisayol: uygulama ve kontrol paneli (ayni exe, farkli ikon).
Name: "{autodesktop}\Varya One";            Filename: "{app}\{#ClientExe}"; IconFilename: "{app}\appicon.ico";   Comment: "Varya One'u ac"; Tasks: desktopicon
Name: "{autodesktop}\Varya Kontrol Paneli"; Filename: "{app}\{#ClientExe}"; Parameters: "--panel"; IconFilename: "{app}\panelicon.ico"; Comment: "Sunucu durumu ve ag ayarlari"; Tasks: desktopicon
Name: "{group}\Varya One";                  Filename: "{app}\{#ClientExe}"; IconFilename: "{app}\appicon.ico"
Name: "{group}\Varya Kontrol Paneli";       Filename: "{app}\{#ClientExe}"; Parameters: "--panel"; IconFilename: "{app}\panelicon.ico"
Name: "{group}\Varya One Kaldir";           Filename: "{uninstallexe}"
; Windows acilisinda kontrol panelini sistem tepsisinde (gizli) baslat.
Name: "{commonstartup}\Varya Kontrol Paneli"; Filename: "{app}\{#ClientExe}"; Parameters: "--tray"; IconFilename: "{app}\panelicon.ico"; Tasks: autostartpanel

[Code]
// Gomulu PostgreSQL, MSVC calisma zamanina baglidir. Servis kaydindan ONCE,
// sessizce kur. Cikis kodu yok sayilir: 0 = tamam, 1638 = daha yeni surum zaten
// var, 3010 = tamam (yeniden baslatma sonra).
procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
begin
  if CurStep = ssPostInstall then
  begin
    ExtractTemporaryFile('vc_redist.x64.exe');
    Exec(ExpandConstant('{tmp}\vc_redist.x64.exe'), '/install /quiet /norestart',
      '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

    // WebView2 Evergreen runtime for varyaone-client.exe. No-ops if present.
    ExtractTemporaryFile('MicrosoftEdgeWebview2Setup.exe');
    Exec(ExpandConstant('{tmp}\MicrosoftEdgeWebview2Setup.exe'), '/silent /install',
      '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

[Run]
; Kurulu bir onceki surumu birak (yeniden kurulumda zararsizca hata verir, yok sayilir).
Filename: "{app}\{#MyExeName}"; Parameters: "service stop"; Flags: runhidden; StatusMsg: "Onceki servis durduruluyor..."
Filename: "{app}\{#MyExeName}"; Parameters: "service uninstall"; Flags: runhidden
Filename: "{app}\{#MyExeName}"; Parameters: "service install"; Flags: runhidden; StatusMsg: "Windows servisi kaydediliyor..."
; Ag modunu yaz + guvenlik duvari kuralini duzenle (netmode kendisi halleder).
Filename: "{app}\{#MyExeName}"; Parameters: "netmode lan";   Tasks: lanaccess;     Flags: runhidden; StatusMsg: "Ag modu ayarlaniyor..."
Filename: "{app}\{#MyExeName}"; Parameters: "netmode local"; Tasks: not lanaccess; Flags: runhidden; StatusMsg: "Ag modu ayarlaniyor..."
Filename: "{app}\{#MyExeName}"; Parameters: "service start"; Flags: runhidden; StatusMsg: "Varya One servisi baslatiliyor..."
Filename: "{app}\{#ClientExe}"; Description: "Varya One'u ac"; Flags: postinstall skipifsilent nowait runasoriginaluser

[UninstallRun]
Filename: "{app}\{#MyExeName}"; Parameters: "service stop"; Flags: runhidden; RunOnceId: "VaryaOneServiceStop"
Filename: "{app}\{#MyExeName}"; Parameters: "service uninstall"; Flags: runhidden; RunOnceId: "VaryaOneServiceUninstall"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Varya One (8080)"""; Flags: runhidden; RunOnceId: "VaryaOneFirewallDelHTTP"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Varya One (mDNS)"""; Flags: runhidden; RunOnceId: "VaryaOneFirewallDelMDNS"

; Not: kullanici verisi %ProgramData%\VaryaOne altindadir (pgdata, master.key,
; storage, backups). Kaldirma islemi bu klasore DOKUNMAZ.
