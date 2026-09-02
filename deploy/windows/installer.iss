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
CloseApplications=yes
RestartApplications=no

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
Source: "{#SourcePath}MicrosoftEdgeWebView2RuntimeInstallerX64.exe"; Flags: dontcopy

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
var
  PrerequisiteRestartRequired: Boolean;

// Upgrade/reinstall: stop the existing service before [Files] tries to replace
// varyaone.exe. Windows keeps a running service executable locked; doing this in
// [Run] is too late because [Run] starts only after all files are copied.
function ServiceStoppedOrMissing(): Boolean;
var
  ResultCode: Integer;
begin
  // First distinguish a missing service from a registered one. Then let
  // findstr turn the textual SCM state into a useful process exit code.
  if not Exec(ExpandConstant('{sys}\sc.exe'), 'query VaryaOne', '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode) then
  begin
    Result := False;
    exit;
  end;
  if ResultCode <> 0 then
  begin
    Result := True;
    exit;
  end;
  Result := Exec(ExpandConstant('{cmd}'),
    '/C "sc.exe query VaryaOne | findstr STOPPED >nul"', '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  I: Integer;
begin
  Result := '';
  // Never execute the old installed helper here: older builds used a service
  // library whose stop waiter could hang forever. sc.exe only submits the stop
  // request; the bounded loop below verifies the real SCM state.
  Exec(ExpandConstant('{sys}\sc.exe'), 'stop VaryaOne', '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode);
  for I := 1 to 100 do
  begin
    if ServiceStoppedOrMissing() then
      exit;
    Sleep(250);
  end;
  Result := 'Varya One servisi 25 saniye içinde durmadı. Kurulum dosyaları güvenle değiştirilemedi; bilgisayarı yeniden başlatıp kurulumu tekrar deneyin.';
end;

procedure ExecAppRequired(Params: String; StatusText: String);
var
  ResultCode: Integer;
  AppExe: String;
begin
  WizardForm.StatusLabel.Caption := StatusText;
  AppExe := ExpandConstant('{app}\{#MyExeName}');
  if not Exec(AppExe, Params, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    RaiseException(Format('%s başlatılamadı: %s', [AppExe, SysErrorMessage(ResultCode)]));
  if ResultCode <> 0 then
    RaiseException(Format('%s %s başarısız oldu (çıkış kodu %d).', [AppExe, Params, ResultCode]));
end;

procedure ExecPrerequisiteRequired(FileName: String; Params: String; DisplayName: String);
var
  ResultCode: Integer;
begin
  WizardForm.StatusLabel.Caption := DisplayName + ' kuruluyor...';
  if not Exec(FileName, Params, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    RaiseException(Format('%s başlatılamadı.', [DisplayName]));
  // 1638: daha yeni sürüm zaten kurulu; 3010: başarılı, yeniden başlatma önerilir.
  if (ResultCode <> 0) and (ResultCode <> 1638) and (ResultCode <> 3010) then
    RaiseException(Format('%s kurulamadı (çıkış kodu %d).', [DisplayName, ResultCode]));
  if ResultCode = 3010 then
    PrerequisiteRestartRequired := True;
end;

function WebView2Installed(): Boolean;
var
  Version: String;
  RuntimeKey: String;
begin
  // Microsoft'un belgelenmiş Evergreen Runtime algılama anahtarı. 64 bit
  // Windows'ta makine kaydı 32 bit registry görünümündedir; kullanıcı kaydı
  // doğrudan HKCU altındadır.
  RuntimeKey := 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  Version := '';
  if RegQueryStringValue(HKLM32, RuntimeKey, 'pv', Version) and
    (Version <> '') and (Version <> '0.0.0.0') then
  begin
    Result := True;
    exit;
  end;
  Version := '';
  Result := RegQueryStringValue(HKCU, RuntimeKey, 'pv', Version) and
    (Version <> '') and (Version <> '0.0.0.0');
end;

procedure InstallWebView2Required();
var
  I: Integer;
begin
  if WebView2Installed() then
    exit;
  ExecPrerequisiteRequired(
    ExpandConstant('{tmp}\MicrosoftEdgeWebView2RuntimeInstallerX64.exe'),
    '/silent /install', 'Microsoft Edge WebView2 çalışma zamanı');
  // Bazı Evergreen sürümleri asıl updater alt süreci bitmeden 0 ile dönebiliyor.
  // Paneli hemen açıp yarışmamak için belgelenmiş registry kaydını bekle.
  for I := 1 to 240 do
  begin
    if WebView2Installed() then
      exit;
    Sleep(250);
  end;
  RaiseException('Microsoft Edge WebView2 çalışma zamanı 60 saniye içinde hazır olmadı.');
end;

function NeedRestart(): Boolean;
begin
  Result := PrerequisiteRestartRequired;
end;

// Gömülü PostgreSQL MSVC çalışma zamanına bağlıdır. Önkoşulları servis
// kaydından önce doğrula; hata çıkışlarını kurulum başarısı gibi gösterme.
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    ExtractTemporaryFile('vc_redist.x64.exe');
    ExecPrerequisiteRequired(ExpandConstant('{tmp}\vc_redist.x64.exe'),
      '/install /quiet /norestart', 'Microsoft Visual C++ çalışma zamanı');

    // Tam Evergreen standalone paketidir; internet olmayan makinede de çalışır.
    ExtractTemporaryFile('MicrosoftEdgeWebView2RuntimeInstallerX64.exe');
    InstallWebView2Required();

    // Ağ ayarını servis başlamadan yaz. Ardından repair eski/stale SCM kaydını
    // durdurur, silinmesini bekler ve yeni executable yoluyla yeniden kurar.
    if WizardIsTaskSelected('lanaccess') then
      ExecAppRequired('netmode lan', 'Ağ modu ayarlanıyor...')
    else
      ExecAppRequired('netmode local', 'Ağ modu ayarlanıyor...');
    ExecAppRequired('service repair', 'Windows servisi kurulup başlatılıyor...');
    ExecAppRequired('service wait-ready', 'Varya One kullanıma hazırlanıyor...');
  end;
end;

[Run]
Filename: "{app}\{#ClientExe}"; Description: "Varya One'u ac"; Flags: postinstall skipifsilent nowait runasoriginaluser

[UninstallRun]
Filename: "{app}\{#MyExeName}"; Parameters: "service stop"; Flags: runhidden; RunOnceId: "VaryaOneServiceStop"
Filename: "{app}\{#MyExeName}"; Parameters: "service uninstall"; Flags: runhidden; RunOnceId: "VaryaOneServiceUninstall"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Varya One (8080)"""; Flags: runhidden; RunOnceId: "VaryaOneFirewallDelHTTP"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Varya One (HTTP)"""; Flags: runhidden; RunOnceId: "VaryaOneFirewallDelHTTPCurrent"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Varya One (mDNS)"""; Flags: runhidden; RunOnceId: "VaryaOneFirewallDelMDNS"

; Not: kullanici verisi %ProgramData%\VaryaOne altindadir (pgdata, master.key,
; storage, backups). Kaldirma islemi bu klasore DOKUNMAZ.
