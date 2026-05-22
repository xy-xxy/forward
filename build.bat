@echo off
REM Forward build script (Windows)
REM Usage:
REM   build.bat              -> build Windows GUI exe
REM   build.bat linux        -> ALSO cross-compile Linux binary
REM
REM For Windows Server 2012 compatibility, use Go 1.20.x

setlocal

echo [1/5] Go version:
go version
echo.

echo [2/5] go mod tidy
go mod tidy
if errorlevel 1 goto :err

echo [3/5] manifest resource (rsrc_windows.syso)
if not exist rsrc_windows.syso (
    where rsrc >nul 2>nul
    if errorlevel 1 (
        echo   installing rsrc...
        go install github.com/akavel/rsrc@latest
        if errorlevel 1 goto :err
    )
    rsrc -manifest forward.manifest -o rsrc_windows.syso
    if errorlevel 1 goto :err
)

echo [4/5] go build (Windows)
go build -trimpath -ldflags="-H windowsgui -s -w" -o forward.exe .
if errorlevel 1 goto :err

if /I "%~1"=="linux" (
    echo [4b/5] go build (Linux cross-compile)
    set GOOS=linux
    set GOARCH=amd64
    go build -trimpath -ldflags="-s -w" -o forward .
    set GOOS=
    set GOARCH=
    if errorlevel 1 goto :err
)

echo [5/5] UPX compression
if exist upx.exe (
    upx.exe --best --lzma -q forward.exe
    if errorlevel 1 goto :err
    if exist forward (
        upx.exe --best --lzma -q forward
        if errorlevel 1 goto :err
    )
) else (
    echo   upx.exe not found, skipping compression
)

echo.
echo Done:
dir forward.exe 2>nul | findstr forward.exe
if exist forward dir forward 2>nul | findstr /v "<DIR>" | findstr forward
exit /b 0

:err
echo.
echo *** BUILD FAILED ***
exit /b 1
