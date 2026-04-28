@echo off
setlocal enabledelayedexpansion

REM ==================================================================
REM WebHook Engine Load Test Runner
REM ==================================================================
REM This script runs k6 load tests. 
REM Usage: run-load-tests.bat [BASE_URL]
REM Example: run-load-tests.bat http://localhost:8080
REM ==================================================================

set SCRIPT_DIR=%~dp0
set TEST_FILE=tests\load\webhook_load_test.js
set FULL_PATH=%SCRIPT_DIR%%TEST_FILE%

if "%~1"=="" (
    set BASE_URL=http://localhost:8080
) else (
    set BASE_URL=%~1
)

echo Starting Load Test Suite...
echo Target: %BASE_URL%
echo Script: %FULL_PATH%
echo.

REM Check if k6 is installed locally
where k6 >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    echo [INFO] Found k6 installed locally.
    k6 run -e BASE_URL=%BASE_URL% "%FULL_PATH%"
    goto :END
)

REM Fallback to Docker if k6 is not installed
where docker >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    echo [INFO] k6 not found in PATH. Attempting to run via Docker...
    
    REM If the user is on Windows/Mac and targeting localhost, 
    REM we suggest using host.docker.internal
    if "%BASE_URL%"=="http://localhost:8080" (
        echo [TIP] Targeting localhost from Docker? You might need:
        echo       run-load-tests.bat http://host.docker.internal:8080
    )

    docker run --rm -i grafana/k6 run -e BASE_URL=%BASE_URL% - < "%FULL_PATH%"
    goto :END
)

echo [ERROR] k6 or Docker is required to run these tests.
echo Please install k6 from https://k6.io/docs/getting-started/installation/
exit /b 1

:END
echo.
echo Load test execution finished.
endlocal