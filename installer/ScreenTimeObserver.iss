#ifndef TargetArch
  #define TargetArch "x64"
#endif
#ifndef BuildRoot
  #error BuildRoot is required
#endif
#ifndef OutputRoot
  #error OutputRoot is required
#endif

[Setup]
AppId={{C347A3E8-D1B0-4C78-AE64-6382F974CE23}
AppName=ScreenTimeObserver
AppVersion=1.3.1
AppPublisher=ScreenTimeObserver
DefaultDirName={localappdata}\Programs\ScreenTimeObserver
DefaultGroupName=ScreenTimeObserver
DisableProgramGroupPage=yes
DisableDirPage=no
PrivilegesRequired=lowest
MinVersion=10.0
#if TargetArch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
#endif
OutputDir={#OutputRoot}
OutputBaseFilename=ScreenTimeObserver-1.3.1-windows-{#TargetArch}-setup
SetupIconFile=..\assets\logo.ico
UninstallDisplayIcon={app}\assets\logo.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
CloseApplicationsFilter=collector.exe,collector-cli.exe
RestartApplications=no
SetupLogging=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Messages]
SetupWindowTitle=安装 - %1
ButtonBack=< 上一步
ButtonNext=下一步 >
ButtonInstall=安装
ButtonOK=确定
ButtonCancel=取消
ButtonYes=是
ButtonNo=否
ButtonFinish=完成
ButtonBrowse=浏览...
ButtonWizardBrowse=浏览...
ButtonNewFolder=新建文件夹
ClickNext=点击“下一步”继续，或点击“取消”退出安装。
BrowseDialogTitle=选择文件夹
BrowseDialogLabel=请选择文件夹，然后点击“确定”。
WizardSelectDir=程序安装位置
SelectDirDesc=请选择程序的安装目录。
SelectDirLabel3=程序文件将安装到以下位置；采集数据位置在下一页独立选择。
SelectDirBrowseLabel=点击“浏览”更换目录，点击“下一步”继续。
DiskSpaceMBLabel=程序至少需要 [mb] MB 可用空间，采集数据另占空间。
WizardSelectTasks=附加选项
SelectTasksDesc=选择启动方式和快捷方式。
SelectTasksLabel2=采集器需要在当前用户登录后运行，才能记录桌面活动。
WizardReady=准备安装
ReadyLabel1=现在可以开始安装 [name]。
ReadyLabel2a=点击“安装”开始，或点击“上一步”修改选项。
ReadyLabel2b=点击“安装”继续。
ReadyMemoDir=程序目录：
ReadyMemoGroup=开始菜单：
ReadyMemoTasks=附加选项：
WizardPreparing=正在准备安装
WizardInstalling=正在安装
FinishedHeadingLabel=安装完成
FinishedLabel=已安装 [name]。可以从开始菜单或托盘打开仪表盘。
FinishedLabelNoIcons=已安装 [name]。
WizardUninstalling=正在卸载程序（保留采集数据）

[Tasks]
Name: "autostart"; Description: "登录 Windows 后自动启动采集器"; Flags: checkedonce
Name: "desktopicon"; Description: "创建桌面快捷方式"; Flags: unchecked

[Files]
Source: "{#BuildRoot}\collector.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BuildRoot}\collector-cli.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\assets\*.ico"; DestDir: "{app}\assets"; Flags: ignoreversion
Source: "..\assets\states\*.ico"; DestDir: "{app}\assets\states"; Flags: ignoreversion

[Icons]
Name: "{group}\屏幕时间采集器"; Filename: "{app}\collector.exe"; WorkingDir: "{app}"; IconFilename: "{app}\assets\logo.ico"
Name: "{group}\打开仪表盘"; Filename: "http://127.0.0.1:17643/"
Name: "{group}\卸载 ScreenTimeObserver"; Filename: "{uninstallexe}"
Name: "{autodesktop}\屏幕时间采集器"; Filename: "{app}\collector.exe"; WorkingDir: "{app}"; Tasks: desktopicon; IconFilename: "{app}\assets\logo.ico"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "ScreenTimeObserver.Installed"; ValueData: """{app}\collector.exe"""; Tasks: autostart; Flags: uninsdeletevalue
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueName: "ScreenTimeObserver.Installed"; Tasks: not autostart; Flags: deletevalue
Root: HKCU; Subkey: "Software\ScreenTimeObserver\Installer"; ValueType: string; ValueName: "DataDir"; ValueData: "{code:GetDataDir}"; Flags: uninsdeletekey; Check: IsFreshInstall

[Run]
Filename: "{app}\collector.exe"; WorkingDir: "{app}"; Description: "启动屏幕时间采集器"; Flags: nowait postinstall skipifsilent

[Code]
var
  DataPage: TInputDirWizardPage;
  FreshInstall: Boolean;

function IsFreshInstall: Boolean;
begin
  Result := FreshInstall;
end;

function GetDataDir(Param: String): String;
begin
  Result := DataPage.Values[0];
end;

procedure InitializeWizard;
var Previous: String;
begin
  Log('初始化数据页');
  DataPage := CreateInputDirPage(wpSelectDir, '数据保存位置',
    '程序和采集数据可以放在不同的位置',
    '窗口记录、截图、Wi-Fi 和日志仅保存在本机。卸载不会删除采集数据。请选择当前用户可写的本地目录。', False, '');
  Log('数据页已创建');
  DataPage.Add('数据目录：');
  if not RegQueryStringValue(HKCU, 'Software\ScreenTimeObserver\Installer', 'DataDir', Previous) then
    Previous := ExpandConstant('{localappdata}\ScreenTimeObserver\data');
  Log('设置数据页默认值：' + Previous);
  DataPage.Values[0] := ExpandConstant('{param:DATADIR|' + Previous + '}');
  Log('数据页初始化完成');
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  { 升级保留原配置，不把更换路径伪装成数据迁移。 }
  Log('检查是否跳过页');
  Result := (PageID = DataPage.ID) and FileExists(ExpandConstant('{app}\config.json'));
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var Path, Probe: String;
begin
  Log('开始安装前验证');
  Result := '';
  FreshInstall := not FileExists(ExpandConstant('{app}\config.json'));
  if FileExists(ExpandConstant('{app}\config.json')) then exit;
  Path := DataPage.Values[0];
  if (Length(Path) < 3) or (Copy(Path, 2, 2) <> ':\') or (Pos('"', Path) > 0) then begin
    Result := '请选择本地磁盘上的绝对路径，例如 D:\ScreenTimeData。'; exit;
  end;
  if not ForceDirectories(Path) then begin
    Result := '无法创建数据目录，请检查路径和权限。'; exit;
  end;
  Probe := AddBackslash(Path) + '.sto-install-' + GetDateTimeString('yyyymmddhhnnsszzz', '-', ':') + '.tmp';
  if not SaveStringToFile(Probe, 'write check', False) then begin
    Result := '数据目录不可写，请选择其他位置。'; exit;
  end;
  DeleteFile(Probe);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Code: Integer;
begin
  if (CurStep = ssPostInstall) and not FileExists(ExpandConstant('{app}\config.json')) then begin
    if not Exec(ExpandConstant('{app}\collector-cli.exe'),
      'configure-install --data-dir "' + DataPage.Values[0] + '"',
      ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, Code) then
        RaiseException('无法启动配置生成程序。');
    if Code <> 0 then RaiseException('无法生成配置，安装未成功完成；请检查数据目录权限。');
  end;
end;

{ 不设置 UninstallDelete：仅卸载安装器登记的程序文件，不删除用户数据或配置。 }
