@echo off
rem  打包发布用的 dist\yogod-v<版本>-windows-amd64.zip —— 解开即用,不用装。只有发布时需要跑这个。
rem  沿用原项目 YogoDot 的发布约定(免安装 zip),但 Go 版全部资源都
rem  embed 进了单个 exe,不再需要 _internal 那 248MB 运行时。
setlocal
cd /d "%~dp0"

echo [1/3] 构建 yogod.exe ...
if not exist dist\yogod mkdir dist\yogod
go build -trimpath -ldflags="-s -w -H windowsgui" -o dist\yogod\yogod.exe .\cmd\yogod || goto :err

echo [2/3] 收文档 ...
copy /y README.md dist\yogod\ >nul || goto :err
copy /y LICENSE dist\yogod\ >nul || goto :err

rem 版本号从 cmd\yogod\main.go 的 const version 读,zip 名跟着版本走
for /f "tokens=4 delims= " %%v in ('findstr /b /c:"const version" cmd\yogod\main.go') do set VER=%%~v
if "%VER%"=="" (echo 没在 main.go 里找到 const version& exit /b 1)
set ZIP=dist\yogod-v%VER%-windows-amd64.zip

echo [3/3] 打 zip ...
powershell -NoProfile -Command "if (Test-Path '%ZIP%') { Remove-Item '%ZIP%' -Force }; Compress-Archive -Path dist\yogod -DestinationPath '%ZIP%'" || goto :err

echo.
echo 好了: %ZIP%
goto :eof

:err
echo.
echo 打包失败,看上面的报错。
exit /b 1
